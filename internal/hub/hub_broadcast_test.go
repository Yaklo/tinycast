package hub

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Broadcast 必须在同一把锁里完成"分配 seq + 生成快照 + 投递"。
//
// 回归背景（审查 M3）：旧实现先 Snapshot()（RLock 内自增 seq）再另外加锁投递，
// 并发调用时订阅者可能先收到 seq 7、再收到 seq 6。
//
// 这里用可控时钟把第一帧卡在"已经分配 seq、还没投递"的位置：旧实现此刻只持有读锁，
// 第二帧可以先完成快照并排到写锁上（sync.RWMutex 会把锁直接交给已经排队的写者），
// 于是先投递大号；修复后第一帧持有写锁，第二帧只能排在它后面。
func TestBroadcastDeliversInSequenceOrder(t *testing.T) {
	h, clock := newTestHub()
	ch, release := h.Subscribe()
	defer release()

	base := clock.now
	entered := make(chan struct{})
	gate := make(chan struct{})
	var calls atomic.Int32
	h.now = func() time.Time {
		if calls.Add(1) == 1 {
			close(entered)
			<-gate
		}
		return base()
	}

	done := make(chan struct{}, 2)
	go func() { h.Broadcast(); done <- struct{}{} }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("第一帧应停在快照生成阶段")
	}

	go func() { h.Broadcast(); done <- struct{}{} }()
	// 给第二帧足够时间走到锁上排队，再放行第一帧。
	time.Sleep(50 * time.Millisecond)
	close(gate)

	read := func() Snapshot {
		t.Helper()
		select {
		case snap := <-ch:
			return snap
		case <-time.After(2 * time.Second):
			t.Fatal("订阅者应收到两帧快照")
			return Snapshot{}
		}
	}
	first, second := read(), read()
	if first.Seq >= second.Seq {
		t.Fatalf("订阅者收到的 seq 必须严格递增，实际先 %d 后 %d", first.Seq, second.Seq)
	}

	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("两帧广播都应在超时前结束")
		}
	}
}

// 并发广播时订阅者收到的 seq 必须单调递增。
//
// 通道只有 8 帧缓冲，订阅者跟不上时会丢帧，因此只断言"不倒退"，不断言连续。
func TestBroadcastSequenceMonotonicUnderConcurrency(t *testing.T) {
	h, _ := newTestHub()
	mustBind(t, h, "alex", "测试直播", testToken, "s1")
	h.ConfirmLive("alex", nil)
	ch, release := h.Subscribe()
	defer release()

	var last uint64
	seen := 0
	drain := func() {
		for {
			select {
			case snap := <-ch:
				if snap.Seq <= last {
					t.Fatalf("订阅者先收到 seq %d，又收到 %d", last, snap.Seq)
				}
				last = snap.Seq
				seen++
			default:
				return
			}
		}
	}

	for round := 0; round < 100; round++ {
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				h.Broadcast()
			}()
		}
		wg.Wait()
		drain()
	}
	if seen == 0 {
		t.Fatal("订阅者应至少收到一帧快照")
	}
}
