package hub

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// fakeClock 是可手动推进的测试时钟。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestHub(t *testing.T) (*Hub, *fakeClock) {
	t.Helper()
	clock := newClock()
	return New(Options{Grace: 5 * time.Minute, Now: clock.Now}), clock
}

const (
	testToken = "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_abcd"
	otherTokn = "ZzYyXxWwVvUuTtSsRrQqPpOoNnMmLlKkJJii9876-"
)

func mustPublish(t *testing.T, h *Hub, username, room, token, clientID string) PublishResult {
	t.Helper()
	res := h.Publish(PublishInput{Username: username, RoomName: room, Token: token, ClientID: clientID})
	if !res.Allowed {
		t.Fatalf("Publish(%s) 应被放行，实际被拒绝: %s (%s)", username, res.Reason, res.Message)
	}
	return res
}

// 推流成功后应当出现一个 LIVE 房间。
func TestPublishCreatesLiveRoom(t *testing.T) {
	h, _ := newTestHub(t)

	res := mustPublish(t, h, "alice", "爱丽丝的直播间", testToken, "c1")
	if res.Room.State != StateLive {
		t.Fatalf("房间状态应为 LIVE，实际为 %s", res.Room.State)
	}
	if res.Room.Room != "爱丽丝的直播间" {
		t.Fatalf("房间名称不匹配: %q", res.Room.Room)
	}
	if res.Room.GraceUntil != 0 {
		t.Fatalf("LIVE 房间不应带释放时间，实际为 %d", res.Room.GraceUntil)
	}

	snap := h.Snapshot()
	if len(snap.Rooms) != 1 {
		t.Fatalf("快照应包含 1 个房间，实际 %d 个", len(snap.Rooms))
	}
}

// 房间名称为空时回退到用户名，保证列表里永远有可读的名称。
func TestPublishFallsBackToUsername(t *testing.T) {
	h, _ := newTestHub(t)
	res := mustPublish(t, h, "alice", "", testToken, "c1")
	if res.Room.Room != "alice" {
		t.Fatalf("空房间名应回退为用户名，实际 %q", res.Room.Room)
	}
}

func TestPublishRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name     string
		username string
		room     string
		token    string
		want     Reason
	}{
		{"用户名过短", "ab", "房间", testToken, ReasonInvalidUsername},
		{"用户名含非法字符", "al ice", "房间", testToken, ReasonInvalidUsername},
		{"用户名带路径分隔符", "al/ice", "房间", testToken, ReasonInvalidUsername},
		{"令牌过短", "alice", "房间", "short", ReasonInvalidToken},
		{"令牌含空格", "alice", "房间", "aaaaaaaaaaaaaaaa aaaaa", ReasonInvalidToken},
		{"房间名过长", "alice", string(make([]rune, MaxRoomNameRunes+1)), testToken, ReasonInvalidRoom},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _ := newTestHub(t)
			res := h.Publish(PublishInput{Username: tc.username, RoomName: tc.room, Token: tc.token, ClientID: "c1"})
			if res.Allowed {
				t.Fatalf("%s 应被拒绝，实际被放行", tc.name)
			}
			if res.Reason != tc.want {
				t.Fatalf("%s 的拒绝原因应为 %s，实际 %s", tc.name, tc.want, res.Reason)
			}
			if len(h.Snapshot().Rooms) != 0 {
				t.Fatal("被拒绝的推流不应创建房间")
			}
			if h.HasPublisher(tc.username) {
				t.Fatal("被拒绝的推流不应写入身份绑定")
			}
		})
	}
}

// 规则 1：用户名不能绑定多个不同的推流令牌。
func TestUsernameCannotBindTwoTokens(t *testing.T) {
	h, clock := newTestHub(t)
	mustPublish(t, h, "alice", "房间", testToken, "c1")

	// 断流进入 GRACE 后仍然占用用户名，不允许换令牌。
	h.Unpublish("alice", "c1")
	clock.Advance(time.Minute)

	res := h.Publish(PublishInput{Username: "alice", RoomName: "房间", Token: otherTokn, ClientID: "c2"})
	if res.Allowed {
		t.Fatal("用户名已绑定其他令牌，应被拒绝")
	}
	if res.Reason != ReasonUsernameTokenConflict {
		t.Fatalf("拒绝原因应为 %s，实际 %s", ReasonUsernameTokenConflict, res.Reason)
	}
}

// 规则 2：同一个推流令牌不能绑定多个不同的用户名。
func TestTokenCannotBindTwoUsernames(t *testing.T) {
	h, _ := newTestHub(t)
	mustPublish(t, h, "alice", "房间", testToken, "c1")

	res := h.Publish(PublishInput{Username: "bob", RoomName: "房间", Token: testToken, ClientID: "c2"})
	if res.Allowed {
		t.Fatal("令牌已绑定其他用户名，应被拒绝")
	}
	if res.Reason != ReasonTokenUsernameConflict {
		t.Fatalf("拒绝原因应为 %s，实际 %s", ReasonTokenUsernameConflict, res.Reason)
	}
	if len(h.Snapshot().Rooms) != 1 {
		t.Fatalf("不应创建新房间，实际房间数 %d", len(h.Snapshot().Rooms))
	}
}

// 规则 3：相同用户名与相同令牌不能同时建立多个推流连接。
func TestSameIdentityCannotPublishTwice(t *testing.T) {
	h, _ := newTestHub(t)
	mustPublish(t, h, "alice", "房间", testToken, "c1")

	res := h.Publish(PublishInput{Username: "alice", RoomName: "房间", Token: testToken, ClientID: "c2"})
	if res.Allowed {
		t.Fatal("同一身份的第二条推流连接应被拒绝")
	}
	if res.Reason != ReasonAlreadyPublishing {
		t.Fatalf("拒绝原因应为 %s，实际 %s", ReasonAlreadyPublishing, res.Reason)
	}
}

// 同一条连接的重复 on_publish 回调必须幂等，否则 SRS 重试会导致推流被拒。
func TestRepeatedPublishCallbackIsIdempotent(t *testing.T) {
	h, _ := newTestHub(t)
	mustPublish(t, h, "alice", "房间", testToken, "c1")
	mustPublish(t, h, "alice", "房间", testToken, "c1")

	if n := len(h.Snapshot().Rooms); n != 1 {
		t.Fatalf("重复回调不应产生额外房间，实际 %d 个", n)
	}
}

// 并发推流校验必须是原子的：同一身份只有一个赢家。
func TestConcurrentPublishSameIdentity(t *testing.T) {
	h, _ := newTestHub(t)

	const workers = 32
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		allowed  int
		rejected int
	)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res := h.Publish(PublishInput{
				Username: "alice",
				RoomName: "房间",
				Token:    testToken,
				ClientID: fmt.Sprintf("c%d", i),
			})
			mu.Lock()
			if res.Allowed {
				allowed++
			} else {
				rejected++
			}
			mu.Unlock()
		}(i)
	}
	close(start)
	wg.Wait()

	if allowed != 1 {
		t.Fatalf("同一身份的并发推流应恰好 1 个成功，实际 %d 个", allowed)
	}
	if rejected != workers-1 {
		t.Fatalf("其余 %d 个应全部被拒绝，实际被拒 %d 个", workers-1, rejected)
	}
}

// 并发推流不同身份时应全部成功。
func TestConcurrentPublishManyIdentities(t *testing.T) {
	h, _ := newTestHub(t)

	const workers = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			res := h.Publish(PublishInput{
				Username: fmt.Sprintf("user%d", i),
				RoomName: "房间",
				// 每个身份一个独立令牌（32 位以上，满足格式要求）。
				Token:    fmt.Sprintf("%032d-abcdefghij", i),
				ClientID: fmt.Sprintf("c%d", i),
			})
			if !res.Allowed {
				t.Errorf("user%d 应被放行，实际被拒绝: %s", i, res.Reason)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if n := len(h.Snapshot().Rooms); n != workers {
		t.Fatalf("应有 %d 个房间，实际 %d 个", workers, n)
	}
}

// 断流后进入 GRACE，并带上释放时间。
func TestUnpublishEntersGrace(t *testing.T) {
	h, clock := newTestHub(t)
	mustPublish(t, h, "alice", "房间", testToken, "c1")
	clock.Advance(10 * time.Minute)

	if !h.Unpublish("alice", "c1") {
		t.Fatal("on_unpublish 应生效")
	}

	view, ok := h.Room("alice")
	if !ok {
		t.Fatal("GRACE 房间应继续存在")
	}
	if view.State != StateGrace {
		t.Fatalf("状态应为 GRACE，实际 %s", view.State)
	}
	if want := clock.Now().Add(5 * time.Minute).UnixMilli(); view.GraceUntil != want {
		t.Fatalf("释放时间应为 %d，实际 %d", want, view.GraceUntil)
	}
	if !h.HasPublisher("alice") {
		t.Fatal("GRACE 期间应保留身份绑定，允许原身份重连")
	}
}

// 过期回调（旧连接迟到的 on_unpublish）不能把已恢复的推流再次打断。
func TestUnpublishIgnoresStaleClient(t *testing.T) {
	h, _ := newTestHub(t)
	mustPublish(t, h, "alice", "房间", testToken, "c1")
	h.Unpublish("alice", "c1")
	mustPublish(t, h, "alice", "房间", testToken, "c2")

	if h.Unpublish("alice", "c1") {
		t.Fatal("来自旧连接的 on_unpublish 应被忽略")
	}
	view, _ := h.Room("alice")
	if view.State != StateLive {
		t.Fatalf("房间应仍为 LIVE，实际 %s", view.State)
	}
}

// 规则 4：保留期内原身份可以重新推流并恢复原房间。
func TestRepublishWithinGraceRestoresRoom(t *testing.T) {
	h, clock := newTestHub(t)
	mustPublish(t, h, "alice", "旧房间名", testToken, "c1")
	h.Unpublish("alice", "c1")

	clock.Advance(4 * time.Minute)
	res := mustPublish(t, h, "alice", "旧房间名", testToken, "c2")

	if res.Room.State != StateLive {
		t.Fatalf("重推后状态应为 LIVE，实际 %s", res.Room.State)
	}
	if res.Room.GraceUntil != 0 {
		t.Fatalf("重推后应清除释放时间，实际 %d", res.Room.GraceUntil)
	}
	if n := len(h.Snapshot().Rooms); n != 1 {
		t.Fatalf("重推不应产生新房间，实际 %d 个", n)
	}
}

// 规则 5：超过保留期后房间与身份绑定被释放，用户名与令牌可被重新占用。
func TestExpireGraceReleasesIdentity(t *testing.T) {
	h, clock := newTestHub(t)
	mustPublish(t, h, "alice", "房间", testToken, "c1")
	h.Unpublish("alice", "c1")

	// 未到期时不应释放。
	clock.Advance(4 * time.Minute)
	if released := h.ExpireGrace(); len(released) != 0 {
		t.Fatalf("未到期的 GRACE 房间不应被释放，实际释放 %v", released)
	}
	if _, ok := h.Room("alice"); !ok {
		t.Fatal("未到期的房间应继续展示在首页")
	}

	// 到期后释放。
	clock.Advance(2 * time.Minute)
	released := h.ExpireGrace()
	if len(released) != 1 || released[0] != "alice" {
		t.Fatalf("应释放 alice，实际 %v", released)
	}
	if _, ok := h.Room("alice"); ok {
		t.Fatal("释放后房间应消失")
	}
	if h.HasPublisher("alice") {
		t.Fatal("释放后应删除身份绑定")
	}

	// 用户名与令牌都已被释放，可以自由重新绑定。
	mustPublish(t, h, "alice", "房间", otherTokn, "c9")
	mustPublish(t, h, "bob", "房间", testToken, "c10")
}

// 规则 6：房间名称不要求全局唯一。
func TestRoomNameIsNotUnique(t *testing.T) {
	h, _ := newTestHub(t)
	mustPublish(t, h, "alice", "同一个名字", testToken, "c1")
	mustPublish(t, h, "bob", "同一个名字", otherTokn, "c2")

	if n := len(h.Snapshot().Rooms); n != 2 {
		t.Fatalf("同名房间应共存，实际 %d 个", n)
	}
}

// 观看人数以 SRS 客户端 ID 去重，重复回调不会重复计数。
func TestViewerCountingIsIdempotent(t *testing.T) {
	h, _ := newTestHub(t)
	mustPublish(t, h, "alice", "房间", testToken, "c1")

	if !h.Play("alice", "v1") {
		t.Fatal("首个观看会话应计数")
	}
	if h.Play("alice", "v1") {
		t.Fatal("重复的 on_play 回调不应再次计数")
	}
	h.Play("alice", "v2")

	view, _ := h.Room("alice")
	if view.ViewerCount != 2 {
		t.Fatalf("观看人数应为 2，实际 %d", view.ViewerCount)
	}

	if !h.Stop("v1") {
		t.Fatal("on_stop 应移除观看会话")
	}
	if h.Stop("v1") {
		t.Fatal("重复的 on_stop 回调应为无操作")
	}
	view, _ = h.Room("alice")
	if view.ViewerCount != 1 {
		t.Fatalf("退出后观看人数应为 1，实际 %d", view.ViewerCount)
	}
}

// 同一个观看客户端切换到另一个房间时，应只计在最新房间上。
func TestViewerSwitchingRoom(t *testing.T) {
	h, _ := newTestHub(t)
	mustPublish(t, h, "alice", "房间", testToken, "c1")
	mustPublish(t, h, "bob", "房间", otherTokn, "c2")

	h.Play("alice", "v1")
	h.Play("bob", "v1")

	alice, _ := h.Room("alice")
	bob, _ := h.Room("bob")
	if alice.ViewerCount != 0 || bob.ViewerCount != 1 {
		t.Fatalf("观看人数应为 alice=0 bob=1，实际 %d/%d", alice.ViewerCount, bob.ViewerCount)
	}
}

// 未推流的房间不接受观看会话。
func TestPlayUnknownRoom(t *testing.T) {
	h, _ := newTestHub(t)
	if h.Play("ghost", "v1") {
		t.Fatal("不存在的房间不应记录观看会话")
	}
	if h.Stop("v1") {
		t.Fatal("未知客户端的 on_stop 应为无操作")
	}
}

// 房间过期释放时，其观看会话索引也要一并清理。
func TestExpireGraceClearsViewers(t *testing.T) {
	h, clock := newTestHub(t)
	mustPublish(t, h, "alice", "房间", testToken, "c1")
	h.Play("alice", "v1")
	h.Unpublish("alice", "c1")

	clock.Advance(6 * time.Minute)
	h.ExpireGrace()

	if h.Stop("v1") {
		t.Fatal("房间释放后观看会话索引应被清理")
	}
}

// 僵尸推流连接（回调丢失）应被核对任务打回 GRACE，年轻的会话不受影响。
func TestReconcileClients(t *testing.T) {
	h, clock := newTestHub(t)
	mustPublish(t, h, "alice", "房间", testToken, "c1")
	h.Play("alice", "v1")

	alive := map[string]bool{"c1": false, "v1": false}

	// 会话太年轻：SRS 的客户端列表可能还没刷新，此时不做任何修改。
	if un, pr := h.ReconcileClients(func(string, string) bool { return true }, 10*time.Second); un != 0 || pr != 0 {
		t.Fatalf("年轻的会话不应被核对，实际 unpublished=%d pruned=%d", un, pr)
	}

	clock.Advance(30 * time.Second)
	un, pr := h.ReconcileClients(func(_, id string) bool { return alive[id] }, 10*time.Second)
	if un != 1 {
		t.Fatalf("应判定 1 条推流已断开，实际 %d", un)
	}
	if pr != 1 {
		t.Fatalf("应清掉 1 条僵尸观看会话，实际 %d", pr)
	}
	view, _ := h.Room("alice")
	if view.State != StateGrace {
		t.Fatalf("状态应为 GRACE，实际 %s", view.State)
	}
	if view.ViewerCount != 0 {
		t.Fatalf("僵尸观看会话应被清理，实际人数 %d", view.ViewerCount)
	}
}

// 快照排序：LIVE 在前，同状态按开始时间倒序。
func TestSnapshotOrdering(t *testing.T) {
	h, clock := newTestHub(t)

	mustPublish(t, h, "old", "房间", testToken, "c1")
	clock.Advance(time.Minute)
	mustPublish(t, h, "new", "房间", otherTokn, "c2")
	clock.Advance(time.Minute)
	mustPublish(t, h, "third", "房间", "token-third-0123456789abcdef", "c3")
	h.Unpublish("third", "c3")

	snap := h.Snapshot()
	got := make([]string, 0, len(snap.Rooms))
	for _, r := range snap.Rooms {
		got = append(got, r.Username+":"+string(r.State))
	}
	want := []string{"new:LIVE", "old:LIVE", "third:GRACE"}
	if len(got) != len(want) {
		t.Fatalf("房间数量不匹配: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("排序不匹配:\n got=%v\nwant=%v", got, want)
		}
	}
}

// 订阅者应当收到变更后的快照，取消订阅后通道关闭。
func TestSubscribeReceivesSnapshots(t *testing.T) {
	h, _ := newTestHub(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)

	ch, release := h.Subscribe()
	defer release()

	if h.SubscriberCount() != 1 {
		t.Fatalf("订阅者数量应为 1，实际 %d", h.SubscriberCount())
	}
	mustPublish(t, h, "alice", "房间", testToken, "c1")

	select {
	case snap := <-ch:
		if snap.Seq == 0 {
			t.Fatal("广播的快照应带有递增序号")
		}
		if len(snap.Rooms) != 1 || snap.Rooms[0].Username != "alice" {
			t.Fatalf("快照内容不正确: %+v", snap.Rooms)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("等待快照超时")
	}

	release()
	if h.SubscriberCount() != 0 {
		t.Fatalf("取消订阅后订阅者数量应为 0，实际 %d", h.SubscriberCount())
	}
	if _, ok := <-ch; ok {
		t.Fatal("取消订阅后通道应被关闭")
	}
}

// HashToken 必须稳定且不暴露原文。
func TestHashToken(t *testing.T) {
	a := HashToken(testToken)
	b := HashToken(testToken)
	if a != b {
		t.Fatal("同一令牌的摘要应稳定")
	}
	if len(a) != 64 {
		t.Fatalf("SHA-256 十六进制摘要应为 64 字符，实际 %d", len(a))
	}
	if a == testToken || HashToken(otherTokn) == a {
		t.Fatal("不同令牌不应得到相同摘要")
	}
}
