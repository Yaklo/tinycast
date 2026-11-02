package httpapi

import (
	"sync"
	"time"
)

// fixedWindowLimiter 是一个很小的固定窗口限流器，用来挡住单人狂刷观看心跳。
//
// 不引入第三方限流库：这里只需要"同一 IP 每 10 秒最多 N 次"，
// 固定窗口足够用，而且实现只有几十行。
type fixedWindowLimiter struct {
	mu        sync.Mutex
	limit     int
	window    time.Duration
	now       func() time.Time
	entries   map[string]windowEntry
	lastSweep time.Time
}

type windowEntry struct {
	start time.Time
	count int
}

func newFixedWindowLimiter(limit int, window time.Duration) *fixedWindowLimiter {
	return &fixedWindowLimiter{
		limit:   limit,
		window:  window,
		now:     time.Now,
		entries: make(map[string]windowEntry),
	}
}

func (l *fixedWindowLimiter) allow(key string) bool {
	if l == nil || l.limit <= 0 || l.window <= 0 {
		return true
	}
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	// 每个窗口最多全表扫一次，清掉过期 key。
	// 不能写成「len > 4096 就每次 allow 都扫」：那样在 5 万个 key 时
	// 每个请求都会持锁扫描全表，限流器本身反而成了瓶颈。
	if now.Sub(l.lastSweep) >= l.window {
		for k, e := range l.entries {
			if now.Sub(e.start) >= l.window {
				delete(l.entries, k)
			}
		}
		l.lastSweep = now
	}

	e, ok := l.entries[key]
	if !ok || now.Sub(e.start) >= l.window {
		l.entries[key] = windowEntry{start: now, count: 1}
		return true
	}
	if e.count >= l.limit {
		return false
	}
	e.count++
	l.entries[key] = e
	return true
}
