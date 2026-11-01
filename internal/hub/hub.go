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
	"time"
)

// 用户名同时是 MediaMTX 的流名，因此只允许 URL 路径里的安全字符。
var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)

// 推流令牌是前端用 crypto.getRandomValues 生成的 base64url 串，至少 256 bit。
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,256}$`)

const (
	// maxRoomNameRunes 是房间展示名长度上限，避免首页卡片被超长文本撑坏。
	maxRoomNameRunes = 64
)

// publisher 是一条已经通过鉴权的推流身份。
type publisher struct {
	username  string
	roomName  string
	tokenHash string
	sessionID string
	ip        string
	boundAt   time.Time
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

	grace    time.Duration
	seq      uint64
	watchers map[chan Snapshot]struct{}
}

// New 创建一个以 grace 作为断流保留时长的 Hub。
func New(grace time.Duration) *Hub {
	return &Hub{
		now:        time.Now,
		byUsername: make(map[string]*publisher),
		byToken:    make(map[string]*publisher),
		rooms:      make(map[string]*room),
		grace:      grace,
		watchers:   make(map[chan Snapshot]struct{}),
	}
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
		return BindResult{Reason: ReasonInvalidRoom, Message: "房间名称不能超过 64 个字符"}
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
	if rm, ok := h.rooms[username]; ok && rm.state == StateLive {
		prev, hasPrev := h.byUsername[username]
		if hasPrev && prev.sessionID != "" && in.SessionID != "" && prev.sessionID != in.SessionID {
			return BindResult{
				Reason:  ReasonAlreadyPublishing,
				Message: "该身份正在推流中，请先停止现有的推流连接",
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

	// 房间若处于 GRACE，这里只更新展示名，等真正推流成功再回到 LIVE。
	if rm, ok := h.rooms[username]; ok {
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
// 返回 true 表示状态发生了变化（用于决定是否广播快照）。
func (h *Hub) ConfirmLive(username string, media *MediaInfo) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	pub, hasPub := h.byUsername[username]
	roomName := username
	if hasPub {
		roomName = pub.roomName
	}

	rm, exists := h.rooms[username]
	if !exists {
		// 这段分支同时承担"接管"职责：后端比 MediaMTX 晚启动、或后端刚重启时，
		// 媒体其实已经在流动，但内存里还没有任何身份。此时先按路径创建房间，
		// 让观众能进来看；等主播下次重新推流触发鉴权时 Bind 会把展示名与令牌补上。
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
		rm.startedAt = h.now()
	}
	rm.state = StateLive
	rm.roomName = roomName
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
	// 断流后原编码参数不再可信，重推时会重新探测。
	rm.media = nil
	return true
}

// Watch 记录一次观看心跳：同一个 viewerID 重复调用只会刷新时间，不会重复计数。
func (h *Hub) Watch(username, viewerID string) bool {
	if viewerID == "" {
		return false
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	rm, ok := h.rooms[username]
	if !ok {
		return false
	}
	rm.viewers[viewerID] = h.now()
	return true
}

// Unwatch 移除观看会话（播放器主动离开或关闭页面）。
func (h *Hub) Unwatch(username, viewerID string) {
	if viewerID == "" {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if rm, ok := h.rooms[username]; ok {
		delete(rm.viewers, viewerID)
	}
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
func (h *Hub) Snapshot() Snapshot {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.seq++
	snap := Snapshot{Seq: h.seq, ServerTime: h.now().UnixMilli()}
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
func (h *Hub) Broadcast() {
	snap := h.Snapshot()
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
