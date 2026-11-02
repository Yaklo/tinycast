// Package hub 是 tinycast 的唯一内存状态中心。
//
// 它维护四张表：用户名 → 推流身份、令牌哈希 → 推流身份、用户名 → 房间、房间 → 观看会话。
// 所有读写都在同一把 RWMutex 内完成，"检查冲突 + 占用身份"是原子操作，
// 因此两个几乎同时到达的推流请求不可能同时通过鉴权。
//
// 设计取舍：进程重启即丢失全部状态（需求明确允许）。真实媒体状态以 MediaMTX 为准，
// 重启后正在推流的连接会在下一次鉴权时重新绑定，房间随之重建。
package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// 用户名同时是 MediaMTX 的流名，因此只允许 URL 路径里的安全字符。
var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)

// 推流令牌是前端用 crypto.getRandomValues 生成的 base64url 串，至少 256 bit。
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,256}$`)

const (
	// maxRoomNameRunes 是房间展示名长度上限，避免首页卡片被超长文本撑坏。
	// 与前端 web/src/lib/validate.ts 的 MAX_ROOM_NAME_RUNES 保持一致。
	maxRoomNameRunes = 40

	// MaxViewersPerRoom 是单个房间的观看会话上限。
	// 正常直播远达不到这个量级；设上限只是为了不让伪造的 viewerId 无限占内存。
	MaxViewersPerRoom = 5000

	// defaultStalePublisherTimeout 是"旧推流会话失效判定窗"的默认值。
	//
	// 巡检每个周期都会调用 ConfirmLive 刷新 publisher.lastConfirmed，正常直播里
	// 这个时窗的年龄始终小于一个巡检周期；只有真的没人再推流时才会超过它。
	// 唯一知道巡检间隔的组件是 monitor，它会用 SetStalePublisherTimeout 把窗口对齐到
	// 几个周期；这里的默认值按默认 1 秒巡检取值，只服务于不经 monitor 的调用方
	// （单元测试、将来可能的其它嵌入场景）。
	defaultStalePublisherTimeout = 3 * time.Second
)

// publisher 是一条已经通过鉴权的推流身份。
type publisher struct {
	username  string
	roomName  string
	tokenHash string
	sessionID string
	ip        string
	boundAt   time.Time
	// lastConfirmed 是巡检最后一次确认"这条身份仍在推流"的时间。
	//
	// 用途见 Bind 的规则 3：断流后 1 秒内重连时，巡检还没来得及把房间打成 GRACE，
	// 旧会话号仍然占着身份；靠这个时间判断旧会话是否真的已经失效。
	// 零值表示"还没被确认过"，此时一律按新鲜处理——宁可拒绝一次重连，
	// 也不能误伤一条正在推流的连接。
	lastConfirmed time.Time
}

// room 是一间真实存在过的房间，只有推流成功（MediaMTX 中路径 ready）才会创建。
type room struct {
	username   string
	roomName   string
	state      State
	startedAt  time.Time
	graceUntil time.Time
	media      *MediaInfo
	// viewers 记录观看会话 ID → 最近一次心跳时间。
	viewers map[string]time.Time
}

// Hub 汇总全部业务状态。
type Hub struct {
	// now 可在测试中替换，避免依赖真实时钟。
	now func() time.Time

	mu         sync.RWMutex
	byUsername map[string]*publisher
	byToken    map[string]*publisher
	rooms      map[string]*room

	grace time.Duration
	seq   atomic.Uint64
	// staleAfter 见 SetStalePublisherTimeout，为 0 时不做任何旧会话替换。
	staleAfter time.Duration
	watchers   map[chan Snapshot]struct{}
}

// New 创建一个以 grace 作为断流保留时长的 Hub。
func New(grace time.Duration) *Hub {
	return &Hub{
		now:        time.Now,
		byUsername: make(map[string]*publisher),
		byToken:    make(map[string]*publisher),
		rooms:      make(map[string]*room),
		grace:      grace,
		staleAfter: defaultStalePublisherTimeout,
		watchers:   make(map[chan Snapshot]struct{}),
	}
}

// SetStalePublisherTimeout 设置"旧推流会话多久没有被巡检确认就视为失效"的时间窗。
//
// 巡检组件（monitor）在启动时按自己的轮询间隔调用它；d <= 0 时退回默认值。
// 这只是让断流后的重连少等一会儿，不会放宽"同一身份不能双推"：真正的推流每个
// 巡检周期都会刷新 lastConfirmed，年龄永远落在窗口内，因此不可能被顶掉。
func (h *Hub) SetStalePublisherTimeout(d time.Duration) {
	if d <= 0 {
		d = defaultStalePublisherTimeout
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.staleAfter = d
}

// HashToken 返回推流令牌的 SHA-256 十六进制摘要。
//
// 令牌本身绝不落内存明文，也不出现在任何接口响应里；
// 同一个用户名重复推流时用哈希做等值比较即可。
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Bind 处理一次推流鉴权：校验用户名/令牌/房间名，检查身份冲突并占用身份。
//
// 注意这里**不创建房间**——房间必须等到媒体真正流动起来才存在，
// 这样"浏览器打开首页就能看到房间但主播其实没推成功"的情况不会发生。
func (h *Hub) Bind(in BindInput) BindResult {
	username := strings.TrimSpace(in.Username)
	if !usernamePattern.MatchString(username) {
		return BindResult{Reason: ReasonInvalidUsername, Message: "用户名需为 3-32 位字母、数字、下划线或短横线"}
	}

	token := strings.TrimSpace(in.Token)
	if !tokenPattern.MatchString(token) {
		return BindResult{Reason: ReasonInvalidToken, Message: "推流令牌格式不正确，请重新生成推流配置"}
	}

	roomName := strings.TrimSpace(in.RoomName)
	if roomName == "" {
		// 房间名只用于展示：允许留空，退回用户名，避免 OBS 手抖漏参数就推不上去。
		roomName = username
	}
	if runes := []rune(roomName); len(runes) > maxRoomNameRunes {
		return BindResult{Reason: ReasonInvalidRoom, Message: "房间名称不能超过 40 个字符"}
	}

	tokenHash := HashToken(token)
	now := h.now()

	h.mu.Lock()
	defer h.mu.Unlock()

	// 规则 1：同一用户名不能绑定多个不同令牌。
	if existing, ok := h.byUsername[username]; ok && existing.tokenHash != tokenHash {
		return BindResult{
			Reason:  ReasonUsernameTokenConflict,
			Message: "该用户名已绑定其他推流令牌，请重新生成推流配置或换一个用户名",
		}
	}
	// 规则 2：同一令牌不能绑定多个不同用户名。
	if existing, ok := h.byToken[tokenHash]; ok && existing.username != username {
		return BindResult{
			Reason:  ReasonTokenUsernameConflict,
			Message: "该推流令牌已绑定其他用户名，请重新生成推流令牌",
		}
	}
	// 规则 3：同一身份不能同时存在两条推流连接。
	//
	// 缺少会话 ID 时也要拒绝：MediaMTX 正常都会带 id，空值只可能来自伪造请求；
	// 旧代码要求双方都非空，反而给了“空 id 绕过并发限制”的口子。
	//
	// 例外：断流后 1 秒内重连时，巡检还没来得及把房间打成 GRACE，旧会话号仍然占着
	// 身份，重连会被永久拒绝（控制接口不可用时尤其致命，巡检根本不会标记断流）。
	// 因此以"最后一次确认仍在推流的时间"为据：旧会话超过 staleAfter 没有任何确认，
	// 就认为它已经失效，允许新会话接管。正在推流的连接每个巡检周期都会被确认，
	// 年龄永远落在窗口内，所以"同身份不能双推"的约束不变。
	if rm, ok := h.rooms[username]; ok && rm.state == StateLive {
		if prev, hasPrev := h.byUsername[username]; hasPrev {
			sameSession := prev.sessionID != "" && in.SessionID != "" && prev.sessionID == in.SessionID
			stale := h.staleAfter > 0 && !prev.lastConfirmed.IsZero() && now.Sub(prev.lastConfirmed) > h.staleAfter
			// 没有会话 ID 的请求即使旧会话看起来已过期也不能接管：MediaMTX 一定会带 id，
			// 空值只可能来自伪造，放行等于把"抢占身份"的口子重新打开。
			if !sameSession && !(stale && in.SessionID != "") {
				return BindResult{
					Reason:  ReasonAlreadyPublishing,
					Message: "该身份正在推流中，请先停止现有的推流连接",
				}
			}
		}
	}

	pub := &publisher{
		username:  username,
		roomName:  roomName,
		tokenHash: tokenHash,
		sessionID: in.SessionID,
		ip:        in.IP,
		boundAt:   now,
	}
	// 令牌换绑（同一用户名换了新令牌）时要先清掉旧的反向索引。
	if old, ok := h.byUsername[username]; ok && old.tokenHash != tokenHash {
		delete(h.byToken, old.tokenHash)
	}
	h.byUsername[username] = pub
	h.byToken[tokenHash] = pub

	// 直播中的房间不允许被后续鉴权改写展示名：攻击者拿到一个格式合法的令牌，
	// 就能把别人正在播的房间标题改掉。只有 GRACE（主播自己重推）才更新标题。
	if rm, ok := h.rooms[username]; ok && rm.state != StateLive {
		rm.roomName = roomName
	}

	view := h.viewLocked(username)
	if view.Username == "" {
		// 鉴权发生在推流成功之前，此时还没有房间；先回一份带展示名的最小视图，
		// 便于调用方写日志、也避免调用方误以为拿到了半个房间。
		view = RoomView{Username: username, Room: roomName}
	}
	return BindResult{Allowed: true, Room: view}
}

// ConfirmLive 把房间标记为 LIVE（推流已真正开始）。
//
// 首次调用会创建房间；GRACE 期间重推则恢复原房间并清除过期时间。
// 每次调用都会刷新绑定的 lastConfirmed，这是 Bind 判断旧会话是否失效的依据。
// 返回 true 表示状态发生了变化（用于决定是否广播快照）。
func (h *Hub) ConfirmLive(username string, media *MediaInfo) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	pub, hasPub := h.byUsername[username]
	if !hasPub {
		// 没有鉴权绑定的路径一律不建房间。
		//
		// 早先这里会"接管"后端启动前就已经在推的流，但那会让任何拿到格式合法
		// 令牌的人抢先绑上一个无主房间，再把真正的主播挡在门外。现在服务重启后
		// 需要主播重新推流（OBS 点一次停止再开始）才能恢复展示——重启丢状态本来
		// 就是需求允许的取舍。
		return false
	}
	// 巡检看到 path 就绪 → 这条身份此刻确实在推流。
	now := h.now()
	pub.lastConfirmed = now
	roomName := pub.roomName

	rm, exists := h.rooms[username]
	if !exists {
		rm = &room{
			username: username,
			roomName: roomName,
			viewers:  make(map[string]time.Time),
		}
		h.rooms[username] = rm
	}

	// 只有从"未开播"切换到 LIVE 时才重置开播时间；巡检每秒都会调用本方法，
	// 若无条件刷新 startedAt，首页的"已开播时长"就会永远停在 0。
	changed := rm.state != StateLive || !exists
	if changed {
		rm.startedAt = now
		// 只在状态变化时写展示名：直播中的房间若被后续鉴权换了绑定，
		// 巡检每秒调用本方法，无条件覆盖会把攻击者选的标题写上去。
		rm.roomName = roomName
	}
	rm.state = StateLive
	rm.graceUntil = time.Time{}
	if media != nil {
		if rm.media == nil || *rm.media != *media {
			copied := *media
			rm.media = &copied
		}
	}
	return changed
}

// MarkGrace 在推流断开后把房间置为 GRACE 并开始倒计时。
func (h *Hub) MarkGrace(username string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	rm, ok := h.rooms[username]
	if !ok || rm.state != StateLive {
		return false
	}
	rm.state = StateGrace
	rm.graceUntil = h.now().Add(h.grace)
	// 断流后播放器已经离场，旧观看会话不再代表"正在观看"。
	// 立即清空并让快照人数归零，避免首页/详情页在保留期里显示一个
	// 永远不会再心跳的假人数；重推后观众会重新上报。
	clear(rm.viewers)
	// 断流后原编码参数不再可信，重推时会重新探测。
	rm.media = nil
	return true
}

// Watch 记录一次观看心跳：同一个 viewerID 重复调用只会刷新时间，不会重复计数。
// 返回值表示房间是否存在、是否处于 LIVE 且人数未超限；重复心跳同样返回 true。
// GRACE 等非 LIVE 状态一律返回 false。
func (h *Hub) Watch(username, viewerID string) bool {
	_, ok := h.watch(username, viewerID)
	return ok
}

// WatchChanged 与 Watch 相同，但额外返回"本次是否新增了观看会话"。
// 心跳刷新不应该触发全量 SSE 广播，只有人数变化时才需要。
func (h *Hub) WatchChanged(username, viewerID string) (added, ok bool) {
	return h.watch(username, viewerID)
}

func (h *Hub) watch(username, viewerID string) (added, ok bool) {
	if viewerID == "" {
		return false, false
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	rm, exists := h.rooms[username]
	if !exists {
		return false, false
	}
	// 只有 LIVE 才接受观看心跳：GRACE 期间播放器已经卸载，继续计数只会
	// 让保留倒计时旁边挂着一个假人数；重推恢复 LIVE 后前端会重新上报。
	if rm.state != StateLive {
		return false, false
	}
	if _, exists := rm.viewers[viewerID]; !exists {
		if len(rm.viewers) >= MaxViewersPerRoom {
			return false, false
		}
		added = true
	}
	rm.viewers[viewerID] = h.now()
	return added, true
}

// Unwatch 移除观看会话（播放器主动离开或关闭页面），返回是否确实删除了一个会话。
func (h *Hub) Unwatch(username, viewerID string) bool {
	if viewerID == "" {
		return false
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	rm, ok := h.rooms[username]
	if !ok {
		return false
	}
	if _, exists := rm.viewers[viewerID]; !exists {
		return false
	}
	delete(rm.viewers, viewerID)
	return true
}

// PruneViewers 清理超过 timeout 没有心跳的观看会话，返回清理数量。
//
// 观众直接关掉浏览器、断网或崩溃时不会发送离开事件，只能靠心跳超时兜底。
func (h *Hub) PruneViewers(timeout time.Duration) int {
	deadline := h.now().Add(-timeout)

	h.mu.Lock()
	defer h.mu.Unlock()

	removed := 0
	for _, rm := range h.rooms {
		for id, seenAt := range rm.viewers {
			if seenAt.Before(deadline) {
				delete(rm.viewers, id)
				removed++
			}
		}
	}
	return removed
}

// ExpireGrace 释放保留期已结束的房间，返回被释放的用户名。
func (h *Hub) ExpireGrace() []string {
	now := h.now()

	h.mu.Lock()
	defer h.mu.Unlock()

	var released []string
	for username, rm := range h.rooms {
		if rm.state != StateGrace || rm.graceUntil.IsZero() || rm.graceUntil.After(now) {
			continue
		}
		delete(h.rooms, username)
		released = append(released, username)
		// 房间释放时身份绑定一并释放，用户名与令牌回到可用状态。
		if pub, ok := h.byUsername[username]; ok {
			delete(h.byToken, pub.tokenHash)
			delete(h.byUsername, username)
		}
	}
	return released
}

// ExpireBindings 清理"鉴权通过但始终没有推流成功"的身份绑定。
//
// 典型场景：OBS 点了开始推流、鉴权通过，但随后编码或网络出错导致流没建立；
// 若不清理，这个用户名会被长期占住，用户再也推不上去。
func (h *Hub) ExpireBindings(timeout time.Duration) []string {
	deadline := h.now().Add(-timeout)

	h.mu.Lock()
	defer h.mu.Unlock()

	var expired []string
	for username, pub := range h.byUsername {
		if _, hasRoom := h.rooms[username]; hasRoom {
			continue
		}
		if pub.boundAt.After(deadline) {
			continue
		}
		delete(h.byToken, pub.tokenHash)
		delete(h.byUsername, username)
		expired = append(expired, username)
	}
	return expired
}

// Room 返回单个房间视图。
func (h *Hub) Room(username string) (RoomView, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if _, ok := h.rooms[username]; !ok {
		return RoomView{}, false
	}
	return h.viewLocked(username), true
}

// Rooms 返回全部 LIVE 与 GRACE 房间，最近开播的排在前面。
func (h *Hub) Rooms() []RoomView {
	h.mu.RLock()
	defer h.mu.RUnlock()

	rooms := make([]RoomView, 0, len(h.rooms))
	for username := range h.rooms {
		rooms = append(rooms, h.viewLocked(username))
	}
	sort.Slice(rooms, func(i, j int) bool {
		if rooms[i].StartedAt != rooms[j].StartedAt {
			return rooms[i].StartedAt > rooms[j].StartedAt
		}
		return rooms[i].Username < rooms[j].Username
	})
	return rooms
}

// Snapshot 生成一份完整快照并递增版本号。
//
// 用写锁而不是读锁：版本号的分配必须与快照内容、以及 Broadcast 的投递建立全序，
// 否则两个快照可能"先拿到大号、后看到旧内容"，订阅者也就无法再用 seq 判断新旧。
// 临界区只是遍历几百个房间，代价可以忽略。
func (h *Hub) Snapshot() Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.snapshotLocked()
}

// snapshotLocked 组装快照并递增版本号，调用方必须已经持有写锁。
//
// 拆出来是为了让 Broadcast 能在同一把锁里完成"分配 seq + 生成快照 + 投递"，
// 同时避免 RWMutex 不可重入导致的自我死锁。
func (h *Hub) snapshotLocked() Snapshot {
	snap := Snapshot{Seq: h.seq.Add(1), ServerTime: h.now().UnixMilli()}
	snap.Rooms = make([]RoomView, 0, len(h.rooms))
	for username := range h.rooms {
		snap.Rooms = append(snap.Rooms, h.viewLocked(username))
	}
	sort.Slice(snap.Rooms, func(i, j int) bool {
		if snap.Rooms[i].StartedAt != snap.Rooms[j].StartedAt {
			return snap.Rooms[i].StartedAt > snap.Rooms[j].StartedAt
		}
		return snap.Rooms[i].Username < snap.Rooms[j].Username
	})
	return snap
}

// Broadcast 先把房间变化写入快照并推给全部 SSE 订阅者。
//
// 房间数量在个人直播场景下很少，直接"全量广播快照"比维护增量补丁简单得多，
// 前端也永远只会看到一致的完整状态。
//
// 分配 seq、生成快照、投递三步必须在同一把写锁内完成：早先的写法先 Snapshot()
// （RLock 内自增 seq）再单独加锁投递，两个并发调用会让订阅者先收到 seq 7 再收到
// seq 6，前端再也无法用 seq 判断新旧。注意不能在这里调用 Snapshot()——
// RWMutex 不可重入，会自我死锁。
func (h *Hub) Broadcast() {
	// 发送期间必须持锁：Subscribe / 注销函数会并发增删并 close 通道，
	// 无锁遍历 map 会触发 runtime 的 "concurrent map iteration and map write"
	// 直接退出进程，向已关闭通道发送则会 panic。
	// 发送是非阻塞的（缓冲满就丢帧），持锁时间可以忽略。
	h.mu.Lock()
	defer h.mu.Unlock()

	snap := h.snapshotLocked()
	for ch := range h.watchers {
		select {
		case ch <- snap:
		default:
			// 订阅者处理不过来就丢弃这一帧：下一次快照是全量的，不会丢状态。
		}
	}
}

// Subscribe 注册一个 SSE 订阅者，返回快照通道与注销函数。
//
// 通道带 8 帧缓冲：某个浏览器标签页卡顿时不会阻塞其它订阅者。
func (h *Hub) Subscribe() (<-chan Snapshot, func()) {
	ch := make(chan Snapshot, 8)

	h.mu.Lock()
	h.watchers[ch] = struct{}{}
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		if _, ok := h.watchers[ch]; ok {
			delete(h.watchers, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

// RoomCount 返回房间数量。健康检查不应该为了一个数字构造整份快照。
func (h *Hub) RoomCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.rooms)
}

// SubscriberCount 返回当前 SSE 订阅者数量（用于健康检查与排障）。
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.watchers)
}

// viewLocked 组装房间视图，调用方必须已经持有锁。
func (h *Hub) viewLocked(username string) RoomView {
	rm, ok := h.rooms[username]
	if !ok {
		return RoomView{}
	}
	view := RoomView{
		Username:    rm.username,
		Room:        rm.roomName,
		State:       rm.state,
		ViewerCount: len(rm.viewers),
		StartedAt:   rm.startedAt.UnixMilli(),
	}
	if rm.state == StateGrace && !rm.graceUntil.IsZero() {
		view.GraceUntil = rm.graceUntil.UnixMilli()
	}
	if rm.media != nil {
		copied := *rm.media
		view.Media = &copied
	}
	return view
}
