package hub

import (
	"sort"
	"sync"
)

// subscriber 是一个快照订阅通道。
type subscriber struct {
	ch   chan Snapshot
	once sync.Once
}

func (s *subscriber) close() {
	s.once.Do(func() { close(s.ch) })
}

// Subscribe 注册一个快照订阅者。
//
// 返回的 release 必须在读取协程退出后调用；它会关闭通道，
// 因此读取方用 `for snap := range ch` 即可自然结束。
func (h *Hub) Subscribe() (<-chan Snapshot, func()) {
	s := &subscriber{ch: make(chan Snapshot, 1)}

	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()

	return s.ch, func() {
		h.mu.Lock()
		delete(h.subs, s)
		s.close()
		h.mu.Unlock()
	}
}

// SubscriberCount 返回当前订阅者数量（用于测试与监控）。
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}

func sortSlice(rooms []RoomView) {
	sort.SliceStable(rooms, func(i, j int) bool {
		a, b := rooms[i], rooms[j]
		if (a.State == StateLive) != (b.State == StateLive) {
			return a.State == StateLive
		}
		if a.StartedAt != b.StartedAt {
			return a.StartedAt > b.StartedAt
		}
		return a.Username < b.Username
	})
}
