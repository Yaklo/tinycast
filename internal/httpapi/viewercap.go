package httpapi

import (
	"sync"
	"time"
)

// maxViewersPerRoomIP 是「同一房间 + 同一客户端」允许出现的不同 viewerId 上限。
//
// 人数按 viewerId 去重，而 viewerId 完全由浏览器生成：不封顶时，一个人换着 ID
// 发心跳就能把人数一路顶到房间上限（5000），把真实观众挤出房间。
// 真实家庭带宽或 NAT 出口同时看同一个房间的独立观众远少于 16，
// 重复心跳命中的还是同一个 viewerId，所以不会误伤正常观众。
const maxViewersPerRoomIP = 16

// viewerCap 按「房间 + 客户端」记录一段时间内出现过的不同 viewerId。
//
// 条目按 ttl 过期，与 hub 里观看会话的超时口径一致：观众关了页面、心跳停了，
// 两边差不多同时忘记这个 viewerId。整组空闲后直接删除，
// map 不会随房间数与 IP 数无界增长。
type viewerCap struct {
	mu        sync.Mutex
	limit     int
	ttl       time.Duration
	now       func() time.Time
	groups    map[string]map[string]time.Time
	lastSweep time.Time
}

func newViewerCap(limit int, ttl time.Duration) *viewerCap {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &viewerCap{
		limit:  limit,
		ttl:    ttl,
		now:    time.Now,
		groups: make(map[string]map[string]time.Time),
	}
}

// allow 记录一次心跳，返回是否放行，以及本次是否新增了一个 viewerId。
//
// 超过上限的 viewerId 不会入表，调用方不必回滚；万一新增后 hub 记账失败
// （房间刚好释放或被占满），调用方用 forget 撤回即可。
func (c *viewerCap) allow(key, viewerID string) (allowed, added bool) {
	if c == nil || c.limit <= 0 {
		return true, false
	}
	now := c.now()

	c.mu.Lock()
	defer c.mu.Unlock()

	c.sweep(now)

	seen := c.groups[key]
	if seen == nil {
		seen = make(map[string]time.Time, c.limit)
		c.groups[key] = seen
	}
	// 组内逐条过期：一个 NAT 出口会长期存在，但出口后面的观众是会更替的。
	for id, at := range seen {
		if now.Sub(at) >= c.ttl {
			delete(seen, id)
		}
	}
	if _, ok := seen[viewerID]; ok {
		seen[viewerID] = now
		return true, false
	}
	if len(seen) >= c.limit {
		return false, false
	}
	seen[viewerID] = now
	return true, true
}

// forget 撤回一次记账：心跳最终没被 hub 采纳时不能白占一个配额。
func (c *viewerCap) forget(key, viewerID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := c.groups[key]
	if seen == nil {
		return
	}
	delete(seen, viewerID)
	if len(seen) == 0 {
		delete(c.groups, key)
	}
}

// sweep 每个 ttl 最多全表扫一次，与 fixedWindowLimiter 的做法一致：
// 否则 key 一多，每个请求都要持锁扫描全表，计数本身反而成了瓶颈。
func (c *viewerCap) sweep(now time.Time) {
	if now.Sub(c.lastSweep) < c.ttl {
		return
	}
	for key, seen := range c.groups {
		active := false
		for _, at := range seen {
			if now.Sub(at) < c.ttl {
				active = true
				break
			}
		}
		if !active {
			delete(c.groups, key)
		}
	}
	c.lastSweep = now
}
