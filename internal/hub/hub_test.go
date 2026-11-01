package hub

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock 让保留期相关的用例不依赖真实时间。
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestHub() (*Hub, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	h := New(5 * time.Minute)
	h.now = clock.now
	return h, clock
}

const testToken = "MPA49PxJv0mJd4Wq7yTn1sB8kRc2hVfLgXe6uZa9Q"

func mustBind(t *testing.T, h *Hub, username, room, token, session string) BindResult {
	t.Helper()
	res := h.Bind(BindInput{Username: username, RoomName: room, Token: token, SessionID: session, IP: "127.0.0.1"})
	if !res.Allowed {
		t.Fatalf("鉴权应通过，实际被拒: %s %s", res.Reason, res.Message)
	}
	return res
}

func TestBindValidatesInput(t *testing.T) {
	h, _ := newTestHub()

	cases := []struct {
		name string
		in   BindInput
		want Reason
	}{
		{"用户名过短", BindInput{Username: "ab", Token: testToken}, ReasonInvalidUsername},
		{"用户名含空格", BindInput{Username: "a b", Token: testToken}, ReasonInvalidUsername},
		{"令牌过短", BindInput{Username: "alex", Token: "short"}, ReasonInvalidToken},
		{"令牌含非法字符", BindInput{Username: "alex", Token: "not a token at all!"}, ReasonInvalidToken},
		{"房间名过长", BindInput{Username: "alex", Token: testToken, RoomName: strings.Repeat("房", 65)}, ReasonInvalidRoom},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.Bind(tc.in)
			if res.Allowed {
				t.Fatal("应被拒绝")
			}
			if res.Reason != tc.want {
				t.Fatalf("拒绝原因应为 %s，实际 %s", tc.want, res.Reason)
			}
			if res.Message == "" {
				t.Fatal("拒绝时应带可读提示")
			}
		})
	}
}

// 房间名只用于展示：留空时退回用户名，避免 OBS 漏参数就推不上去。
func TestBindFallsBackToUsernameWhenRoomEmpty(t *testing.T) {
	h, _ := newTestHub()
	res := mustBind(t, h, "alex", "", testToken, "s1")
	if res.Room.Room != "alex" {
		t.Fatalf("空房间名应退回用户名，实际 %q", res.Room.Room)
	}
}

// 鉴权通过不代表房间存在：必须等真正推流成功才创建房间。
func TestBindDoesNotCreateRoom(t *testing.T) {
	h, _ := newTestHub()
	mustBind(t, h, "alex", "测试直播", testToken, "s1")

	if _, ok := h.Room("alex"); ok {
		t.Fatal("鉴权通过后不应创建房间")
	}
	if len(h.Rooms()) != 0 {
		t.Fatalf("房间列表应为空，实际 %d 条", len(h.Rooms()))
	}
}

// 规则 1：同一用户名不能绑定两个不同令牌。
func TestBindRejectsSecondTokenForUsername(t *testing.T) {
	h, _ := newTestHub()
	mustBind(t, h, "alex", "测试直播", testToken, "s1")

	res := h.Bind(BindInput{Username: "alex", RoomName: "另一个房间", Token: testToken + "x", SessionID: "s2"})
	if res.Allowed {
		t.Fatal("同一用户名换令牌应被拒绝")
	}
	if res.Reason != ReasonUsernameTokenConflict {
		t.Fatalf("原因应为 %s，实际 %s", ReasonUsernameTokenConflict, res.Reason)
	}
}

// 规则 2：同一令牌不能绑定两个不同用户名。
func TestBindRejectsSecondUsernameForToken(t *testing.T) {
	h, _ := newTestHub()
	mustBind(t, h, "alex", "测试直播", testToken, "s1")

	res := h.Bind(BindInput{Username: "bob", RoomName: "测试直播", Token: testToken, SessionID: "s2"})
	if res.Allowed {
		t.Fatal("同一令牌换用户名应被拒绝")
	}
	if res.Reason != ReasonTokenUsernameConflict {
		t.Fatalf("原因应为 %s，实际 %s", ReasonTokenUsernameConflict, res.Reason)
	}
}

// 规则 3：同一身份不能同时存在两条推流连接。
func TestBindRejectsConcurrentPublisher(t *testing.T) {
	h, _ := newTestHub()
	mustBind(t, h, "alex", "测试直播", testToken, "session-a")
	h.ConfirmLive("alex", &MediaInfo{VideoCodec: "H264"})

	res := h.Bind(BindInput{Username: "alex", RoomName: "测试直播", Token: testToken, SessionID: "session-b"})
	if res.Allowed {
		t.Fatal("直播中同一身份再次推流应被拒绝")
	}
	if res.Reason != ReasonAlreadyPublishing {
		t.Fatalf("原因应为 %s，实际 %s", ReasonAlreadyPublishing, res.Reason)
	}
}

// 同一条连接重复回调（MediaMTX 可能重试）必须幂等放行。
func TestBindIsIdempotentForSameSession(t *testing.T) {
	h, _ := newTestHub()
	mustBind(t, h, "alex", "测试直播", testToken, "session-a")
	h.ConfirmLive("alex", nil)

	res := h.Bind(BindInput{Username: "alex", RoomName: "测试直播", Token: testToken, SessionID: "session-a"})
	if !res.Allowed {
		t.Fatalf("同一连接重复鉴权应放行，实际 %s", res.Reason)
	}
}

// 服务重启后内存里没有绑定，MediaMTX 却可能仍在推流；此时不能凭路径清单
// 凭空建房间，否则任何人都能用自选令牌接管一间无主直播。
func TestConfirmLiveDoesNotAdoptUnboundStream(t *testing.T) {
	h, _ := newTestHub()

	if h.ConfirmLive("alex", &MediaInfo{VideoCodec: "H264"}) {
		t.Fatal("没有绑定的流不应被确认")
	}
	if _, ok := h.Room("alex"); ok {
		t.Fatal("没有绑定就不应创建房间")
	}
	if len(h.Rooms()) != 0 {
		t.Fatalf("房间列表应为空，实际 %d 条", len(h.Rooms()))
	}
}

// 直播中的房间不能被后续鉴权改写标题：巡检每秒都会调用 ConfirmLive，
// 若无条件用绑定里的 roomName 覆盖，攻击者换绑后下一秒标题就变了。
func TestConfirmLiveKeepsLiveRoomName(t *testing.T) {
	h, _ := newTestHub()
	mustBind(t, h, "alex", "原房间", testToken, "s1")
	if !h.ConfirmLive("alex", nil) {
		t.Fatal("首次确认应报告状态变化")
	}

	// 同一身份同一会话再次鉴权，只改展示名：LIVE 状态下应被忽略。
	mustBind(t, h, "alex", "被改掉的标题", testToken, "s1")
	h.ConfirmLive("alex", nil)

	view, _ := h.Room("alex")
	if view.Room != "原房间" {
		t.Fatalf("直播中的标题不应被改写，实际 %q", view.Room)
	}
}

func TestConfirmLiveCreatesAndRestoresRoom(t *testing.T) {
	h, clock := newTestHub()
	mustBind(t, h, "alex", "测试直播", testToken, "s1")

	if changed := h.ConfirmLive("alex", &MediaInfo{VideoCodec: "H264", Width: 1920, Height: 1080}); !changed {
		t.Fatal("首次确认应报告状态变化")
	}
	view, ok := h.Room("alex")
	if !ok {
		t.Fatal("房间应已创建")
	}
	if view.State != StateLive || view.Room != "测试直播" {
		t.Fatalf("房间状态异常：%+v", view)
	}
	if view.Media == nil || view.Media.Width != 1920 {
		t.Fatalf("应带上编码参数：%+v", view.Media)
	}

	// 同一轮直播里巡检反复调用不应刷新开播时间，也不应重复报告变化。
	if changed := h.ConfirmLive("alex", view.Media); changed {
		t.Fatal("重复确认不应报告状态变化")
	}
	clock.advance(2 * time.Second)
	h.ConfirmLive("alex", view.Media)
	if again, _ := h.Room("alex"); again.StartedAt != view.StartedAt {
		t.Fatalf("开播时间不应被巡检刷新：%d -> %d", view.StartedAt, again.StartedAt)
	}

	// 断流 → GRACE；重推 → 恢复 LIVE 并清空保留期。
	if !h.MarkGrace("alex") {
		t.Fatal("断流应报告状态变化")
	}
	grace, _ := h.Room("alex")
	if grace.State != StateGrace || grace.GraceUntil == 0 {
		t.Fatalf("应进入 GRACE 并带释放时间：%+v", grace)
	}
	if grace.Media != nil {
		t.Fatal("断流后不应再展示编码参数")
	}

	clock.advance(time.Minute)
	h.ConfirmLive("alex", nil)
	live, _ := h.Room("alex")
	if live.State != StateLive || live.GraceUntil != 0 {
		t.Fatalf("重推后应回到 LIVE：%+v", live)
	}
}

func TestExpireGraceReleasesRoomAndBinding(t *testing.T) {
	h, clock := newTestHub()
	mustBind(t, h, "alex", "测试直播", testToken, "s1")
	h.ConfirmLive("alex", nil)
	h.MarkGrace("alex")

	clock.advance(4 * time.Minute)
	if released := h.ExpireGrace(); len(released) != 0 {
		t.Fatalf("保留期内不应释放，实际释放 %v", released)
	}
	if _, ok := h.Room("alex"); !ok {
		t.Fatal("保留期内房间仍在")
	}

	clock.advance(2 * time.Minute)
	if released := h.ExpireGrace(); len(released) != 1 || released[0] != "alex" {
		t.Fatalf("超过保留期应释放 alex，实际 %v", released)
	}
	if _, ok := h.Room("alex"); ok {
		t.Fatal("房间应已删除")
	}
	// 身份一并释放：原令牌可以重新绑定到新房间。
	res := h.Bind(BindInput{Username: "alex", RoomName: "新房间", Token: testToken, SessionID: "s9"})
	if !res.Allowed {
		t.Fatalf("释放后原身份应可重新使用，实际 %s", res.Reason)
	}
}

// 鉴权通过但始终没推流的绑定必须过期，否则用户名会被永久占住。
func TestExpireBindings(t *testing.T) {
	h, clock := newTestHub()
	mustBind(t, h, "alex", "测试直播", testToken, "s1")

	clock.advance(30 * time.Second)
	if expired := h.ExpireBindings(time.Minute); len(expired) != 0 {
		t.Fatalf("未超时不应清理，实际 %v", expired)
	}
	clock.advance(time.Minute)
	if expired := h.ExpireBindings(time.Minute); len(expired) != 1 {
		t.Fatalf("超时应清理绑定，实际 %v", expired)
	}
	// 已建房间的绑定不受影响。
	mustBind(t, h, "bob", "测试直播", "bob-token-0123456789abcdef", "s2")
	h.ConfirmLive("bob", nil)
	clock.advance(10 * time.Minute)
	if expired := h.ExpireBindings(time.Minute); len(expired) != 0 {
		t.Fatalf("已开播的身份不应被清理，实际 %v", expired)
	}
}

func TestWatchCountsUniqueViewers(t *testing.T) {
	h, clock := newTestHub()
	mustBind(t, h, "alex", "测试直播", testToken, "s1")
	h.ConfirmLive("alex", nil)

	if !h.Watch("alex", "viewer-1") {
		t.Fatal("房间存在时应记录观看")
	}
	h.Watch("alex", "viewer-1") // 心跳重复上报不能重复计数
	h.Watch("alex", "viewer-2")
	if view, _ := h.Room("alex"); view.ViewerCount != 2 {
		t.Fatalf("应统计到 2 人，实际 %d", view.ViewerCount)
	}

	h.Unwatch("alex", "viewer-2")
	if view, _ := h.Room("alex"); view.ViewerCount != 1 {
		t.Fatalf("离开后应剩 1 人，实际 %d", view.ViewerCount)
	}

	// 关掉浏览器不发离开事件，只能靠心跳超时兜底。
	clock.advance(40 * time.Second)
	if removed := h.PruneViewers(30 * time.Second); removed != 1 {
		t.Fatalf("应清理 1 个僵尸会话，实际 %d", removed)
	}
	if view, _ := h.Room("alex"); view.ViewerCount != 0 {
		t.Fatalf("清理后应为 0 人，实际 %d", view.ViewerCount)
	}

	if h.Watch("nobody", "viewer-3") {
		t.Fatal("房间不存在时不应记录观看")
	}
}

func TestWatchChangedReportsOnlyNewViewers(t *testing.T) {
	h, _ := newTestHub()
	mustBind(t, h, "alex", "测试直播", testToken, "s1")
	h.ConfirmLive("alex", nil)

	if added, ok := h.WatchChanged("alex", "viewer-1"); !added || !ok {
		t.Fatalf("首次观看应新增，实际 added=%v ok=%v", added, ok)
	}
	if added, ok := h.WatchChanged("alex", "viewer-1"); added || !ok {
		t.Fatalf("重复心跳不应算新增，实际 added=%v ok=%v", added, ok)
	}
	if _, ok := h.WatchChanged("nobody", "viewer-2"); ok {
		t.Fatal("房间不存在时应返回 false")
	}
	if h.Unwatch("alex", "not-there") {
		t.Fatal("移除不存在的会话应返回 false")
	}
	if !h.Unwatch("alex", "viewer-1") {
		t.Fatal("移除存在的会话应返回 true")
	}
}

// 单个房间的观看会话必须有上限，避免伪造 viewerId 无限占用内存。
func TestWatchRejectsViewersBeyondLimit(t *testing.T) {
	h, _ := newTestHub()
	mustBind(t, h, "alex", "测试直播", testToken, "s1")
	h.ConfirmLive("alex", nil)

	for i := 0; i < MaxViewersPerRoom; i++ {
		if _, ok := h.WatchChanged("alex", "viewer-"+strconv.Itoa(i)); !ok {
			t.Fatalf("第 %d 个观众不应被拒绝", i+1)
		}
	}
	if _, ok := h.WatchChanged("alex", "one-too-many"); ok {
		t.Fatal("超过上限后应拒绝新观众")
	}
}

func TestViewHidesToken(t *testing.T) {
	h, _ := newTestHub()
	mustBind(t, h, "alex", "测试直播", testToken, "s1")
	h.ConfirmLive("alex", nil)

	rooms := h.Rooms()
	if len(rooms) != 1 {
		t.Fatalf("应有 1 个房间，实际 %d", len(rooms))
	}
	data, err := json.Marshal(rooms[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), testToken) {
		t.Fatalf("房间视图泄露了推流令牌: %s", data)
	}
	if strings.Contains(string(data), HashToken(testToken)) {
		t.Fatalf("房间视图泄露了令牌哈希: %s", data)
	}
}

func TestBindStoresTokenHashOnly(t *testing.T) {
	h, _ := newTestHub()
	mustBind(t, h, "alex", "测试直播", testToken, "s1")

	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, pub := range h.byUsername {
		if pub.tokenHash == testToken {
			t.Fatal("内存里不应保存令牌明文")
		}
		if pub.tokenHash != HashToken(testToken) {
			t.Fatalf("应保存令牌哈希，实际 %s", pub.tokenHash)
		}
	}
	if _, ok := h.byToken[testToken]; ok {
		t.Fatal("令牌索引应以哈希为键")
	}
	if _, ok := h.byToken[HashToken(testToken)]; !ok {
		t.Fatal("令牌索引缺少哈希键")
	}
}

func TestBroadcastNotifiesSubscribers(t *testing.T) {
	h, _ := newTestHub()
	ch, release := h.Subscribe()
	defer release()

	mustBind(t, h, "alex", "测试直播", testToken, "s1")
	h.ConfirmLive("alex", nil)
	h.Broadcast()

	select {
	case snap := <-ch:
		if len(snap.Rooms) != 1 || snap.Rooms[0].Username != "alex" {
			t.Fatalf("快照内容异常：%+v", snap)
		}
		if snap.Seq == 0 || snap.ServerTime == 0 {
			t.Fatalf("快照应带版本号与时间：%+v", snap)
		}
	case <-time.After(time.Second):
		t.Fatal("订阅者应收到快照")
	}
	if h.SubscriberCount() != 1 {
		t.Fatalf("订阅者数量应为 1，实际 %d", h.SubscriberCount())
	}
}

// 广播与订阅者增删并发时不能崩溃。
//
// 回归背景：Broadcast 曾经在锁外遍历 h.watchers，而注销函数会在锁内
// delete + close，运行时会以 fatal error: concurrent map iteration and
// map write 直接退出进程（recover 拦不住）；未及时关闭时还会 panic: send on
// closed channel。本用例在 -race 下必须保持干净。
func TestBroadcastConcurrentWithSubscribe(t *testing.T) {
	h, _ := newTestHub()
	mustBind(t, h, "alex", "测试直播", testToken, "s1")
	h.ConfirmLive("alex", nil)

	stop := make(chan struct{})
	var broadcaster sync.WaitGroup
	broadcaster.Add(1)
	go func() {
		defer broadcaster.Done()
		for {
			select {
			case <-stop:
				return
			default:
				h.Broadcast()
			}
		}
	}()

	var subscribers sync.WaitGroup
	for i := 0; i < 8; i++ {
		subscribers.Add(1)
		go func() {
			defer subscribers.Done()
			for j := 0; j < 300; j++ {
				ch, release := h.Subscribe()
				select {
				case <-ch:
				default:
				}
				release()
			}
		}()
	}
	subscribers.Wait()
	close(stop)
	broadcaster.Wait()

	if n := h.SubscriberCount(); n != 0 {
		t.Fatalf("全部注销后订阅者应为 0，实际 %d", n)
	}
}

// 房间按开播时间倒序，最近开播的排在最前面。
func TestRoomsSortedByStartedAt(t *testing.T) {
	h, clock := newTestHub()
	mustBind(t, h, "alex", "旧房间", testToken, "s1")
	h.ConfirmLive("alex", nil)
	clock.advance(time.Minute)

	mustBind(t, h, "bob", "新房间", "bob-token-0123456789abcdef", "s2")
	h.ConfirmLive("bob", nil)

	rooms := h.Rooms()
	if len(rooms) != 2 || rooms[0].Username != "bob" {
		t.Fatalf("应按开播时间倒序，实际 %+v", rooms)
	}
}
