package monitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"tinycast/internal/hub"
	"tinycast/internal/mtx"
)

// fakeMediaMTX 是一个可随时改写返回值的 /v3/paths/list 假服务。
type fakeMediaMTX struct {
	mu   sync.Mutex
	body string
	code int
}

func (f *fakeMediaMTX) set(paths string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.body = paths
	f.code = http.StatusOK
}

func (f *fakeMediaMTX) fail() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.code = http.StatusInternalServerError
}

func (f *fakeMediaMTX) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	body, code := f.body, f.code
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(body))
}

func pathsJSON(items string) string {
	return `{"itemCount":1,"pageCount":1,"items":[` + items + `]}`
}

const livePath = `{"name":"live/alex","ready":true,"tracks":["H264"],"tracks2":[{"codec":"H264","codecProps":{"width":1280,"height":720,"profile":"High","level":"3.1"}},{"codec":"MPEG-4 Audio","codecProps":{"sampleRate":48000,"channelCount":2}}]}`

func newTestMonitor(t *testing.T, grace time.Duration) (*Monitor, *hub.Hub, *fakeMediaMTX) {
	t.Helper()
	fake := &fakeMediaMTX{code: http.StatusOK}
	fake.set(`{"itemCount":0,"pageCount":0,"items":[]}`)
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)

	h := hub.New(grace)
	m := New(h, mtx.New(srv.URL, "", ""), Options{
		App:            "live",
		Interval:       10 * time.Millisecond,
		ViewerTimeout:  50 * time.Millisecond,
		BindingTimeout: 50 * time.Millisecond,
	})
	return m, h, fake
}

func bind(t *testing.T, h *hub.Hub) {
	t.Helper()
	res := h.Bind(hub.BindInput{Username: "alex", RoomName: "测试直播", Token: "MPA49PxJv0mJd4Wq7yTn1sB8Rc2hVfLgXe6uZa9Q", SessionID: "s1"})
	if !res.Allowed {
		t.Fatalf("鉴权失败：%s", res.Reason)
	}
}

// 鉴权通过不等于开播：只有 MediaMTX 里出现 ready 的路径才算真正推流成功。
func TestTickConfirmsLiveOnlyForReadyPath(t *testing.T) {
	m, h, fake := newTestMonitor(t, time.Minute)
	bind(t, h)

	if m.Tick(context.Background()) {
		t.Fatal("没有路径时不应报告变化")
	}
	if _, ok := h.Room("alex"); ok {
		t.Fatal("没有路径时不应创建房间")
	}

	fake.set(pathsJSON(livePath))
	if !m.Tick(context.Background()) {
		t.Fatal("路径就绪应报告变化")
	}
	view, ok := h.Room("alex")
	if !ok || view.State != hub.StateLive {
		t.Fatalf("应创建 LIVE 房间：%+v", view)
	}
	if view.Media == nil || view.Media.VideoCodec != "H264" || view.Media.Width != 1280 {
		t.Fatalf("应带上编码参数：%+v", view.Media)
	}
	if view.Media.AudioCodec != "AAC" {
		t.Fatalf("音频编码应归一化为 AAC，实际 %q", view.Media.AudioCodec)
	}

	// 路径消失 → 进入保留期。
	fake.set(`{"itemCount":0,"pageCount":0,"items":[]}`)
	if !m.Tick(context.Background()) {
		t.Fatal("路径消失应报告变化")
	}
	grace, _ := h.Room("alex")
	if grace.State != hub.StateGrace || grace.GraceUntil == 0 {
		t.Fatalf("应进入 GRACE：%+v", grace)
	}
}

// MediaMTX 重启或接口临时不可用时，不能把正在直播的房间全打成 GRACE。
func TestTickKeepsStateWhenAPIFails(t *testing.T) {
	m, h, fake := newTestMonitor(t, time.Minute)
	bind(t, h)
	fake.set(pathsJSON(livePath))
	m.Tick(context.Background())

	fake.fail()
	if m.Tick(context.Background()) {
		t.Fatal("接口失败不应报告状态变化")
	}
	if view, _ := h.Room("alex"); view.State != hub.StateLive {
		t.Fatalf("接口失败时应保持 LIVE，实际 %s", view.State)
	}
}

// 控制接口长时间不可用时，不能让页面一直显示“正在直播”。
func TestTickDegradesLiveRoomsAfterPersistentAPIFailure(t *testing.T) {
	m, h, fake := newTestMonitor(t, time.Minute)
	bind(t, h)
	fake.set(pathsJSON(livePath))
	m.Tick(context.Background())

	fake.fail()
	for i := 1; i <= maxAPIFailures; i++ {
		m.Tick(context.Background())
		view, _ := h.Room("alex")
		if i < maxAPIFailures && view.State != hub.StateLive {
			t.Fatalf("第 %d 次失败不应降级，实际 %s", i, view.State)
		}
	}
	if view, _ := h.Room("alex"); view.State != hub.StateGrace {
		t.Fatalf("连续 %d 次失败后应降级为 GRACE，实际 %s", maxAPIFailures, view.State)
	}

	// 恢复后继续按真实清单同步，流还在就回到 LIVE。
	fake.set(pathsJSON(livePath))
	m.Tick(context.Background())
	if view, _ := h.Room("alex"); view.State != hub.StateLive {
		t.Fatalf("接口恢复且流仍在时应回到 LIVE，实际 %s", view.State)
	}
}

// 降级期间不能回收房间与绑定：MediaMTX 恢复后仍在推的流必须能回到 LIVE。
func TestTickKeepsBindingsWhileDegraded(t *testing.T) {
	m, h, fake := newTestMonitor(t, 30*time.Millisecond)
	bind(t, h)
	fake.set(pathsJSON(livePath))
	m.Tick(context.Background())

	fake.fail()
	for i := 0; i < maxAPIFailures; i++ {
		m.Tick(context.Background())
	}
	if view, _ := h.Room("alex"); view.State != hub.StateGrace {
		t.Fatalf("降级后应为 GRACE，实际 %s", view.State)
	}

	// 让保留期与绑定超时都过去；降级期间两者都不应被回收。
	time.Sleep(50 * time.Millisecond)
	m.Tick(context.Background())
	if _, ok := h.Room("alex"); !ok {
		t.Fatal("降级期间不应释放房间")
	}

	// 恢复且流仍在：凭保留的绑定直接回到 LIVE。
	fake.set(pathsJSON(livePath))
	m.Tick(context.Background())
	if view, _ := h.Room("alex"); view.State != hub.StateLive {
		t.Fatalf("恢复后应回到 LIVE，实际 %s", view.State)
	}
}

func TestTickIgnoresOtherAppsAndNotReadyPaths(t *testing.T) {
	m, h, fake := newTestMonitor(t, time.Minute)
	bind(t, h)

	fake.set(pathsJSON(`{"name":"other/alex","ready":true,"tracks2":[{"codec":"H264"}]}`))
	m.Tick(context.Background())
	if _, ok := h.Room("alex"); ok {
		t.Fatal("别的应用下的路径不应建房")
	}

	fake.set(pathsJSON(`{"name":"live/alex","ready":false,"tracks2":[{"codec":"H264"}]}`))
	m.Tick(context.Background())
	if _, ok := h.Room("alex"); ok {
		t.Fatal("未就绪的路径不应建房")
	}
}

// 保留期结束后监控循环负责释放房间，并顺带清掉没有心跳的观看会话。
func TestTickReleasesExpiredGraceAndStaleViewers(t *testing.T) {
	m, h, fake := newTestMonitor(t, 40*time.Millisecond)
	bind(t, h)
	fake.set(pathsJSON(livePath))
	m.Tick(context.Background())
	h.Watch("alex", "viewer-1")

	fake.set(`{"itemCount":0,"pageCount":0,"items":[]}`)
	m.Tick(context.Background())

	time.Sleep(60 * time.Millisecond)
	m.Tick(context.Background())
	if _, ok := h.Room("alex"); ok {
		t.Fatal("保留期结束后房间应被释放")
	}
	// 身份也一并释放：原用户名+令牌可以重新绑定。
	res := h.Bind(hub.BindInput{Username: "alex", RoomName: "新房间", Token: "MPA49PxJv0mJd4Wq7yTn1sB8Rc2hVfLgXe6uZa9Q", SessionID: "s2"})
	if !res.Allowed {
		t.Fatalf("释放后应能重新鉴权：%s", res.Reason)
	}
}

// 一直没推流成功的绑定必须过期，否则用户名会被永久占用。
func TestTickExpiresIdleBindings(t *testing.T) {
	m, h, _ := newTestMonitor(t, time.Minute)
	bind(t, h)

	time.Sleep(60 * time.Millisecond)
	m.Tick(context.Background())

	// 绑定已释放，换个令牌也能通过（说明旧绑定没占着用户名）。
	res := h.Bind(hub.BindInput{Username: "alex", RoomName: "测试直播", Token: "another-token-0123456789abcdef", SessionID: "s2"})
	if !res.Allowed {
		t.Fatalf("空闲绑定应已释放：%s", res.Reason)
	}
}

func TestTickBroadcastsChanges(t *testing.T) {
	m, h, fake := newTestMonitor(t, time.Minute)
	bind(t, h)
	ch, release := h.Subscribe()
	defer release()

	fake.set(pathsJSON(livePath))
	m.Tick(context.Background())

	select {
	case snap := <-ch:
		if len(snap.Rooms) != 1 || snap.Rooms[0].Username != "alex" {
			t.Fatalf("快照内容异常：%+v", snap.Rooms)
		}
	case <-time.After(time.Second):
		t.Fatal("状态变化应广播快照")
	}
}

func TestMediaOf(t *testing.T) {
	if got := mediaOf(mtx.Path{}); got != nil {
		t.Fatalf("没有轨道时应返回 nil，实际 %+v", got)
	}

	hevc := mediaOf(mtx.Path{Tracks2: []mtx.Track{{Codec: "H265", CodecProps: mtx.TrackProps{Width: 3840, Height: 2160, Profile: "Main", Level: "5.1"}}}})
	if hevc == nil || hevc.VideoCodec != "H265" || hevc.Height != 2160 {
		t.Fatalf("H265 解析错误：%+v", hevc)
	}

	audioOnly := mediaOf(mtx.Path{Tracks2: []mtx.Track{{Codec: "OPUS", CodecProps: mtx.TrackProps{SampleRate: 48000}}}})
	if audioOnly == nil || audioOnly.VideoCodec != "" || audioOnly.AudioCodec != "Opus" {
		t.Fatalf("纯音频解析错误：%+v", audioOnly)
	}
}

func TestNormalizeCodec(t *testing.T) {
	cases := map[string]string{
		"MPEG-4 Audio": "AAC",
		"mpeg-4 audio": "AAC",
		"H264":         "H264",
		"h265":         "H265",
		"AV1":          "AV1",
		"OPUS":         "Opus",
		"G711":         "G711",
		"未知编码":         "未知编码",
	}
	for in, want := range cases {
		if got := normalizeCodec(in); got != want {
			t.Fatalf("normalizeCodec(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestDescribe(t *testing.T) {
	m, _, _ := newTestMonitor(t, time.Minute)
	if got := m.Describe(); got == "" {
		t.Fatal("Describe 不应为空")
	}
}

// 确保 ticker 循环能被 context 取消（Run 不阻塞 goroutine 泄漏）。
func TestRunStopsOnContextCancel(t *testing.T) {
	m, _, _ := newTestMonitor(t, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run 应在 context 取消后退出")
	}
}

var _ = json.Marshal
