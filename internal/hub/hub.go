// Package hub 是 tinycast 的唯一业务状态中心。
//
// 全部状态保存在内存中：服务重启即丢失，这是需求明确接受的设计取舍。
// 所有读写都经过一把 sync.RWMutex，"校验 + 占用"这类复合操作在写锁内完成，
// 从而保证同一身份的并发推流校验是原子的。
package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// MaxRoomNameRunes 限制房间展示名长度（按字符计）。
	MaxRoomNameRunes = 40
	// MaxRoomNameBytes 防止多字节房间名占用过多内存。
	MaxRoomNameBytes = 256
	// maybeCoalesce 是快照广播的合并窗口：观看人数抖动时不会刷屏。
	maybeCoalesce = 30 * time.Millisecond
)

var (
	// 用户名同时是 SRS 的流名，必须能安全地放进 RTMP 路径段。
	usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{2,31}$`)
	// 令牌是前端用 crypto.getRandomValues 生成的 base64url 串（32 字节 → 43 字符）。
	tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,256}$`)
)

// publisher 是一条身份绑定记录：用户名与令牌哈希一一对应（规则 1、2）。
type publisher struct {
	Username  string
	TokenHash string
	CreatedAt time.Time
}

// room 是房间的内部状态。
type room struct {
	username   string
	name       string
	state      State
	startedAt  time.Time
	graceUntil time.Time
	// publishID 是当前 LIVE 连接的 SRS 客户端 ID，用于识别过期/重复回调。
	publishID string
	// warning 是当前推流的健康告警码，换一次推流就重新计算。
	warning   string
	viewers   map[string]time.Time
	updatedAt time.Time
}

// Options 是 Hub 的构造参数。
type Options struct {
	// Grace 是断流保留时长，默认 5 分钟。
	Grace time.Duration
	// Now 可在测试中替换时钟。
	Now func() time.Time
}

// Hub 是并发安全的内存状态中心。
type Hub struct {
	mu    sync.RWMutex
	grace time.Duration
	now   func() time.Time

	publishersByUsername map[string]*publisher
	publishersByToken    map[string]*publisher
	rooms                map[string]*room
	// viewersByClient 以 SRS 客户端 ID 为键，便于 on_stop 只带 client_id 时也能定位房间。
	viewersByClient map[string]string

	seq   uint64
	subs  map[*subscriber]struct{}
	dirty chan struct{}
}

// New 创建一个空的 Hub。
func New(opts Options) *Hub {
	grace := opts.Grace
	if grace <= 0 {
		grace = 5 * time.Minute
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Hub{
		grace:                grace,
		now:                  now,
		publishersByUsername: make(map[string]*publisher),
		publishersByToken:    make(map[string]*publisher),
		rooms:                make(map[string]*room),
		viewersByClient:      make(map[string]string),
		subs:                 make(map[*subscriber]struct{}),
		dirty:                make(chan struct{}, 1),
	}
}

// Grace 返回断流保留时长。
func (h *Hub) Grace() time.Duration { return h.grace }

// HashToken 返回推流令牌的 SHA-256 十六进制摘要。
//
// 令牌本身是 256bit 随机串，不存在被猜测/爆破的空间，因此无需 bcrypt 之类的慢哈希；
// 后端只保存摘要，任何接口都不会回显令牌或摘要。
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Publish 处理一次 on_publish：校验身份、写入绑定、创建或恢复房间。
//
// 校验与占用在同一把写锁内完成，因此并发的同一身份推流只会有一个成功。
func (h *Hub) Publish(in PublishInput) PublishResult {
	username := strings.TrimSpace(in.Username)
	roomName := strings.TrimSpace(in.RoomName)
	token := strings.TrimSpace(in.Token)

	if !usernamePattern.MatchString(username) {
		return reject(ReasonInvalidUsername, "用户名需为 3-32 位字母、数字、下划线或短横线")
	}
	if !tokenPattern.MatchString(token) {
		return reject(ReasonInvalidToken, "推流令牌格式非法")
	}
	if roomName == "" {
		roomName = username
	}
	if utf8.RuneCountInString(roomName) > MaxRoomNameRunes || len(roomName) > MaxRoomNameBytes {
		return reject(ReasonInvalidRoom, "房间名称过长")
	}

	tokenHash := HashToken(token)
	now := h.now()

	h.mu.Lock()
	defer h.mu.Unlock()

	// 规则 1：用户名不能绑定多个不同的推流令牌。
	if p, ok := h.publishersByUsername[username]; ok && p.TokenHash != tokenHash {
		return reject(ReasonUsernameTokenConflict, "该用户名已绑定其他推流令牌")
	}
	// 规则 2：同一个推流令牌不能绑定多个不同的用户名。
	if p, ok := h.publishersByToken[tokenHash]; ok && p.Username != username {
		return reject(ReasonTokenUsernameConflict, "该推流令牌已被其他用户名使用")
	}

	rm, exists := h.rooms[username]
	if exists {
		// 规则 3：同一身份不能同时存在多条推流连接。
		// 同一个 client_id 的重复回调视为幂等，直接放行。
		if rm.state == StateLive && rm.publishID != "" && rm.publishID != in.ClientID {
			return reject(ReasonAlreadyPublishing, "该身份正在推流中")
		}
		if rm.state == StateLive && rm.publishID == in.ClientID && rm.name == roomName {
			return PublishResult{Allowed: true, Room: h.viewLocked(rm)}
		}
	} else {
		rm = &room{username: username, viewers: make(map[string]time.Time)}
		h.rooms[username] = rm
	}

	// 首次推流创建绑定；GRACE 内重连则沿用原绑定（规则 4）。
	if _, ok := h.publishersByUsername[username]; !ok {
		p := &publisher{Username: username, TokenHash: tokenHash, CreatedAt: now}
		h.publishersByUsername[username] = p
		h.publishersByToken[tokenHash] = p
	}

	rm.name = roomName
	rm.warning = ""
	if in.StreamUnparsed {
		rm.warning = WarnStreamUnparsed
	}
	rm.state = StateLive
	rm.startedAt = now
	rm.graceUntil = time.Time{}
	rm.publishID = in.ClientID
	rm.updatedAt = now

	h.notifyLocked()
	return PublishResult{Allowed: true, Room: h.viewLocked(rm)}
}

// Unpublish 处理 on_unpublish：把房间置为 GRACE 并设置释放时间。
//
// 只接受来自当前 LIVE 连接的回调，过期回调（例如重连后旧连接的 on_unpublish）
// 会被忽略，避免把刚恢复的房间再次打成 GRACE。
func (h *Hub) Unpublish(username, clientID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	rm, ok := h.rooms[username]
	if !ok || rm.state != StateLive {
		return false
	}
	if clientID != "" && rm.publishID != "" && rm.publishID != clientID {
		return false
	}

	now := h.now()
	rm.state = StateGrace
	rm.graceUntil = now.Add(h.grace)
	rm.publishID = ""
	rm.updatedAt = now

	h.notifyLocked()
	return true
}

// Play 记录一条观看会话；重复回调按幂等处理，不会重复计数。
func (h *Hub) Play(username, clientID string) bool {
	if clientID == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	rm, ok := h.rooms[username]
	if !ok {
		return false
	}
	if prev, ok := h.viewersByClient[clientID]; ok {
		if prev == username {
			return false // 重复回调
		}
		// 同一个客户端切换了房间：先从旧房间摘除。
		if prevRoom, ok := h.rooms[prev]; ok {
			delete(prevRoom.viewers, clientID)
			prevRoom.updatedAt = h.now()
		}
	}
	rm.viewers[clientID] = h.now()
	rm.updatedAt = h.now()
	h.viewersByClient[clientID] = username

	h.notifyLocked()
	return true
}

// Stop 移除一条观看会话。
func (h *Hub) Stop(clientID string) bool {
	if clientID == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	username, ok := h.viewersByClient[clientID]
	if !ok {
		return false
	}
	delete(h.viewersByClient, clientID)
	if rm, ok := h.rooms[username]; ok {
		delete(rm.viewers, clientID)
		rm.updatedAt = h.now()
	}
	h.notifyLocked()
	return true
}

// ExpireGrace 释放所有超过保留期的 GRACE 房间，返回被释放的用户名。
//
// 释放动作会同时删除房间、身份绑定与残留的观看会话记录。
func (h *Hub) ExpireGrace() []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := h.now()
	var released []string
	for username, rm := range h.rooms {
		if rm.state != StateGrace || now.Before(rm.graceUntil) {
			continue
		}
		released = append(released, username)
		for clientID := range rm.viewers {
			delete(h.viewersByClient, clientID)
		}
		delete(h.rooms, username)
		if p, ok := h.publishersByUsername[username]; ok {
			delete(h.publishersByUsername, username)
			delete(h.publishersByToken, p.TokenHash)
		}
	}
	if len(released) > 0 {
		h.notifyLocked()
	}
	return released
}

// ReconcileClients 依据 SRS 返回的在线客户端列表修复状态。
//
// alive 返回 true 表示该 client_id 仍存活于 SRS。
// 不在列表中的推流连接会被判定为"回调丢失"并打回 GRACE，僵尸观看会话则直接清掉。
// minAge 用于跳过刚建立不久的会话：SRS 的客户端列表存在短暂延迟，
// 过早核对会把正常的推流误判为已断开。
func (h *Hub) ReconcileClients(alive func(username, clientID string) bool, minAge time.Duration) (unpublished, pruned int) {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := h.now()
	changed := false

	for username, rm := range h.rooms {
		if rm.state == StateLive && rm.publishID != "" && now.Sub(rm.startedAt) >= minAge && !alive(username, rm.publishID) {
			rm.state = StateGrace
			rm.graceUntil = now.Add(h.grace)
			rm.publishID = ""
			rm.updatedAt = now
			unpublished++
			changed = true
		}
		for clientID, joinedAt := range rm.viewers {
			if now.Sub(joinedAt) < minAge || alive(username, clientID) {
				continue
			}
			delete(rm.viewers, clientID)
			delete(h.viewersByClient, clientID)
			rm.updatedAt = now
			pruned++
			changed = true
		}
	}
	if changed {
		h.notifyLocked()
	}
	return unpublished, pruned
}

// Room 返回指定用户名的房间视图。
func (h *Hub) Room(username string) (RoomView, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	rm, ok := h.rooms[username]
	if !ok {
		return RoomView{}, false
	}
	return h.viewLocked(rm), true
}

// Snapshot 返回当前完整状态快照。
func (h *Hub) Snapshot() Snapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.snapshotLocked()
}

// HasPublisher 报告用户名是否已被绑定（仅用于测试与调试）。
func (h *Hub) HasPublisher(username string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.publishersByUsername[username]
	return ok
}

func reject(reason Reason, msg string) PublishResult {
	return PublishResult{Allowed: false, Reason: reason, Message: msg}
}

func (h *Hub) viewLocked(rm *room) RoomView {
	view := RoomView{
		Username:    rm.username,
		Room:        rm.name,
		State:       rm.state,
		ViewerCount: len(rm.viewers),
		StartedAt:   rm.startedAt.UnixMilli(),
		Warning:     rm.warning,
	}
	if rm.state == StateGrace {
		view.GraceUntil = rm.graceUntil.UnixMilli()
	}
	return view
}

func (h *Hub) snapshotLocked() Snapshot {
	rooms := make([]RoomView, 0, len(h.rooms))
	for _, rm := range h.rooms {
		rooms = append(rooms, h.viewLocked(rm))
	}
	// LIVE 房间排在前面，同状态按开始时间倒序，保证列表稳定。
	sortSlice(rooms)
	return Snapshot{
		Seq:        h.seq,
		ServerTime: h.now().UnixMilli(),
		Rooms:      rooms,
	}
}

func (h *Hub) notifyLocked() {
	select {
	case h.dirty <- struct{}{}:
	default:
	}
}

// Run 启动快照广播循环，直到 ctx 被取消。
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.dirty:
		}
		// 合并窗口内的连续变更，避免观看人数抖动导致刷屏。
	coalesce:
		for {
			select {
			case <-h.dirty:
			case <-time.After(maybeCoalesce):
				break coalesce
			case <-ctx.Done():
				return
			}
		}
		h.broadcast()
	}
}

// broadcast 生成一次快照并推送给全部订阅者。
func (h *Hub) broadcast() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.seq++
	snap := h.snapshotLocked()
	for s := range h.subs {
		// 每个订阅者只有一个槽位：新快照总是覆盖尚未消费的旧快照。
		select {
		case s.ch <- snap:
		default:
			select {
			case <-s.ch:
			default:
			}
			select {
			case s.ch <- snap:
			default:
			}
		}
	}
}
