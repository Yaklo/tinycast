package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tinycast/internal/config"
	"tinycast/internal/hub"
)

const testToken = "MPA49PxJv0mJd4Wq7yTn1sB8Rc2hVfLgXe6uZa9Q"
const bobToken = "bobToken0123456789abcdefXYZ"

func mustCIDRs(t *testing.T, raw ...string) []*net.IPNet {
	t.Helper()
	out := make([]*net.IPNet, 0, len(raw))
	for _, item := range raw {
		_, network, err := net.ParseCIDR(item)
		if err != nil {
			t.Fatalf("网段 %s 非法: %v", item, err)
		}
		out = append(out, network)
	}
	return out
}

func newTestServer(t *testing.T, tweak func(*config.Config)) (*Server, *hub.Hub) {
	t.Helper()
	cfg := &config.Config{
		HTTPAddr:        ":0",
		RoomGrace:       5 * time.Minute,
		ViewerTimeout:   30 * time.Second,
		PendingTimeout:  60 * time.Second,
		MonitorInterval: time.Second,
		MediaMTX: config.MediaMTXConfig{
			App:             "live",
			APIURL:          "http://127.0.0.1:9997",
			WHEPProxyTarget: "http://127.0.0.1:8889",
			HLSProxyTarget:  "http://127.0.0.1:8888",
			RTMPPublicBase:  "rtmp://localhost:1935/live",
			AuthAllowed:     mustCIDRs(t, "127.0.0.0/8", "::1/128"),
		},
	}
	if tweak != nil {
		tweak(cfg)
	}
	h := hub.New(cfg.RoomGrace)
	return New(h, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))), h
}

// do 发一个请求并把响应解析成 map，方便断言 JSON 字段。
func do(t *testing.T, srv *Server, method, target string, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.RemoteAddr = "127.0.0.1:50000"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	out := map[string]any{}
	if rec.Body.Len() > 0 && strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("响应不是合法 JSON: %s", rec.Body.String())
		}
	}
	return rec, out
}

func authBody(action, path, query, id string) string {
	payload := map[string]any{
		"ip":       "127.0.0.1",
		"action":   action,
		"path":     path,
		"protocol": "rtmp",
		"id":       id,
		"query":    query,
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

func publish(t *testing.T, srv *Server, username, room, token, session string) *httptest.ResponseRecorder {
	t.Helper()
	query := "room=" + room + "&token=" + token
	if room == "" {
		query = "token=" + token
	}
	rec, _ := do(t, srv, http.MethodPost, "/internal/mtx/auth", authBody("publish", "live/"+username, query, session))
	return rec
}

func TestConfigEndpoint(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	rec, body := do(t, srv, http.MethodGet, "/api/config", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码应为 200，实际 %d", rec.Code)
	}
	if body["app"] != "live" || body["rtmpServer"] != "rtmp://localhost:1935/live" {
		t.Fatalf("配置内容异常：%v", body)
	}
	if body["graceSeconds"].(float64) != 300 {
		t.Fatalf("保留时长应为 300 秒，实际 %v", body["graceSeconds"])
	}
	if body["hls"] != true || body["webrtc"] != true {
		t.Fatalf("两种播放方式都应可用：%v", body)
	}
}

func TestRoomsEmptyIsArray(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	rec, _ := do(t, srv, http.MethodGet, "/api/rooms", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码应为 200，实际 %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"rooms":[]`) {
		t.Fatalf("空列表应序列化为 []，实际 %s", rec.Body.String())
	}
}

func TestRoomNotFound(t *testing.T) {
	srv, _ := newTestServer(t, nil)

	rec, body := do(t, srv, http.MethodGet, "/api/rooms/nobody", "")
	if rec.Code != http.StatusNotFound || body["code"] != "room_not_found" {
		t.Fatalf("应返回 404 room_not_found，实际 %d %v", rec.Code, body)
	}

	rec, body = do(t, srv, http.MethodGet, "/api/does-not-exist", "")
	if rec.Code != http.StatusNotFound || body["code"] != "not_found" {
		t.Fatalf("未知接口应返回 JSON 404，实际 %d %v", rec.Code, body)
	}
}

// 完整的一条链路：鉴权通过 → 监控确认开播 → 房间可查 → 播放信息可用。
func TestPublishFlowCreatesRoom(t *testing.T) {
	srv, h := newTestServer(t, nil)

	rec := publish(t, srv, "alex", "%E6%B5%8B%E8%AF%95%E7%9B%B4%E6%92%AD", testToken, "s1")
	if rec.Code != http.StatusOK {
		t.Fatalf("鉴权应通过，实际 %d %s", rec.Code, rec.Body.String())
	}

	// 鉴权通过但还没开播：房间不存在。
	if rec, _ := do(t, srv, http.MethodGet, "/api/rooms/alex", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("未开播时不应有房间，实际 %d", rec.Code)
	}

	h.ConfirmLive("alex", &hub.MediaInfo{VideoCodec: "H264", Width: 1920, Height: 1080})

	rec, body := do(t, srv, http.MethodGet, "/api/rooms/alex", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("房间应可查，实际 %d", rec.Code)
	}
	room := body["room"].(map[string]any)
	if room["room"] != "测试直播" || room["state"] != "LIVE" {
		t.Fatalf("房间信息异常：%v", room)
	}
	if room["media"].(map[string]any)["width"].(float64) != 1920 {
		t.Fatalf("应带编码参数：%v", room["media"])
	}

	_, body = do(t, srv, http.MethodGet, "/api/rooms/alex/play", "")
	play := body["playback"].(map[string]any)
	if play["protocol"] != "hls" {
		t.Fatalf("默认应推荐 HLS，实际 %v", play["protocol"])
	}
	if play["hlsUrl"] != "/hls/live/alex/index.m3u8" {
		t.Fatalf("HLS 地址异常：%v", play["hlsUrl"])
	}
	if play["whepUrl"] != "/rtc/live/alex/whep" {
		t.Fatalf("WHEP 地址异常：%v", play["whepUrl"])
	}
}

// 只有 WebRTC 代理时推荐 webRTC，并通过 /api/config 反映出来。
func TestPlayWithoutHLS(t *testing.T) {
	srv, h := newTestServer(t, func(c *config.Config) { c.MediaMTX.HLSProxyTarget = "" })
	h.ConfirmLive("alex", nil)

	_, body := do(t, srv, http.MethodGet, "/api/config", "")
	if body["hls"] != false || body["webrtc"] != true {
		t.Fatalf("配置应与代理开关一致：%v", body)
	}

	_, body = do(t, srv, http.MethodGet, "/api/rooms/alex/play", "")
	play := body["playback"].(map[string]any)
	if play["protocol"] != "webrtc" {
		t.Fatalf("应推荐 webrtc，实际 %v", play["protocol"])
	}
	if _, ok := play["hlsUrl"]; ok {
		t.Fatalf("未配置 HLS 时不应给出 hlsUrl：%v", play)
	}
}

// 断流保留期不能给播放地址，否则前端会播一个不存在的流。
func TestPlayInGraceHasNoPlayback(t *testing.T) {
	srv, h := newTestServer(t, nil)
	h.ConfirmLive("alex", nil)
	h.MarkGrace("alex")

	_, body := do(t, srv, http.MethodGet, "/api/rooms/alex/play", "")
	if body["state"] != "GRACE" {
		t.Fatalf("状态应为 GRACE，实际 %v", body["state"])
	}
	if _, ok := body["playback"]; ok {
		t.Fatalf("保留期不应给播放地址：%v", body)
	}
	if body["graceUntil"].(float64) <= float64(time.Now().UnixMilli()) {
		t.Fatalf("应给出释放时间：%v", body["graceUntil"])
	}
}

func TestPublishRejectsInvalidToken(t *testing.T) {
	srv, _ := newTestServer(t, nil)

	rec := publish(t, srv, "alex", "测试", "short", "s1")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("非法令牌应返回 401，实际 %d", rec.Code)
	}
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Code != string(hub.ReasonInvalidToken) || body.Message == "" {
		t.Fatalf("拒绝原因异常：%+v", body)
	}
}

func TestPublishRejectsUnboundAndConflicts(t *testing.T) {
	srv, _ := newTestServer(t, nil)

	if rec := publish(t, srv, "alex", "测试", testToken, "s1"); rec.Code != http.StatusOK {
		t.Fatalf("首次鉴权应通过，实际 %d", rec.Code)
	}
	// 规则 1：同一用户名换令牌。
	if rec := publish(t, srv, "alex", "测试", "anotherToken0123456789abcdef", "s2"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("换令牌应被拒绝，实际 %d", rec.Code)
	}
	// 规则 2：同一令牌换用户名。
	if rec := publish(t, srv, "bob", "测试", testToken, "s3"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("换用户名应被拒绝，实际 %d", rec.Code)
	}
}

// 推流路径必须在配置的 app 下，避免有人推到其它路径绕过统计。
func TestPublishRejectsOtherApp(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	rec, body := do(t, srv, http.MethodPost, "/internal/mtx/auth", authBody("publish", "other/alex", "token="+testToken, "s1"))

	if rec.Code != http.StatusForbidden || body["code"] != "invalid_path" {
		t.Fatalf("应返回 403 invalid_path，实际 %d %v", rec.Code, body)
	}
}

func TestPublishAcceptsTokenFromPasswordAndQuery(t *testing.T) {
	srv, _ := newTestServer(t, nil)

	// 令牌填在密码位（rtmp://user:token@host）。
	body := `{"action":"publish","path":"live/alex","password":"` + testToken + `","query":"","id":"s1"}`
	if rec, _ := do(t, srv, http.MethodPost, "/internal/mtx/auth", body); rec.Code != http.StatusOK {
		t.Fatalf("密码位令牌应被接受，实际 %d %s", rec.Code, rec.Body.String())
	}

	// 换一个用户名，只用 query 里的令牌。
	body = `{"action":"publish","path":"live/bob","query":"room=%E6%B5%8B%E8%AF%95&token=` + bobToken + `","id":"s2"}`
	if rec, _ := do(t, srv, http.MethodPost, "/internal/mtx/auth", body); rec.Code != http.StatusOK {
		t.Fatalf("query 令牌应被接受，实际 %d %s", rec.Code, rec.Body.String())
	}
}

func TestAuthAllowsReadAndAPI(t *testing.T) {
	srv, _ := newTestServer(t, nil)

	for _, action := range []string{"read", "api", "metrics", ""} {
		rec, _ := do(t, srv, http.MethodPost, "/internal/mtx/auth", authBody(action, "live/alex", "", "s1"))
		if rec.Code != http.StatusOK {
			t.Fatalf("action=%q 应放行，实际 %d", action, rec.Code)
		}
	}
}

func TestAuthSecret(t *testing.T) {
	srv, _ := newTestServer(t, func(c *config.Config) { c.MediaMTX.AuthSecret = "s3cret" })

	rec, body := do(t, srv, http.MethodPost, "/internal/mtx/auth", authBody("read", "live/alex", "", "s1"))
	if rec.Code != http.StatusUnauthorized || body["code"] != "invalid_secret" {
		t.Fatalf("缺密钥应拒绝，实际 %d %v", rec.Code, body)
	}

	// 查询参数里带密钥。
	req := httptest.NewRequest(http.MethodPost, "/internal/mtx/auth?secret=s3cret", strings.NewReader(authBody("read", "live/alex", "", "s1")))
	req.RemoteAddr = "127.0.0.1:50000"
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("密钥正确时应放行，实际 %d %s", rr.Code, rr.Body.String())
	}

	// 也可以放在请求头里。
	req = httptest.NewRequest(http.MethodPost, "/internal/mtx/auth", strings.NewReader(authBody("read", "live/alex", "", "s1")))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("X-Tinycast-Secret", "s3cret")
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("请求头密钥应放行，实际 %d", rr.Code)
	}
}

func TestAuthRejectsPublicSource(t *testing.T) {
	srv, _ := newTestServer(t, func(c *config.Config) { c.MediaMTX.AuthAllowed = mustCIDRs(t, "10.0.0.0/8") })

	rec, body := do(t, srv, http.MethodPost, "/internal/mtx/auth", authBody("publish", "live/alex", "token="+testToken, "s1"))
	if rec.Code != http.StatusForbidden || body["code"] != "ip_forbidden" {
		t.Fatalf("公网来源应拒绝，实际 %d %v", rec.Code, body)
	}
}

func TestWatchHeartbeat(t *testing.T) {
	srv, h := newTestServer(t, nil)
	h.ConfirmLive("alex", nil)

	rec, body := do(t, srv, http.MethodPost, "/api/rooms/alex/watch", `{"viewerId":"viewer-1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("心跳应成功，实际 %d %s", rec.Code, rec.Body.String())
	}
	if body["room"].(map[string]any)["viewerCount"].(float64) != 1 {
		t.Fatalf("应统计到 1 人：%v", body["room"])
	}

	// 重复心跳不能重复计数。
	do(t, srv, http.MethodPost, "/api/rooms/alex/watch", `{"viewerId":"viewer-1"}`)
	if _, body = do(t, srv, http.MethodGet, "/api/rooms/alex", ""); body["room"].(map[string]any)["viewerCount"].(float64) != 1 {
		t.Fatalf("重复心跳不应重复计数：%v", body["room"])
	}

	// 查询参数形式同样可用。
	if rec, _ := do(t, srv, http.MethodPost, "/api/rooms/alex/watch?viewerId=viewer-2", ""); rec.Code != http.StatusOK {
		t.Fatalf("查询参数形式心跳应成功，实际 %d", rec.Code)
	}

	// 离开后立即减一。
	if rec, _ := do(t, srv, http.MethodDelete, "/api/rooms/alex/watch?viewerId=viewer-2", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("离开应返回 204，实际 %d", rec.Code)
	}
	if _, body = do(t, srv, http.MethodGet, "/api/rooms/alex", ""); body["room"].(map[string]any)["viewerCount"].(float64) != 1 {
		t.Fatalf("离开后应剩 1 人：%v", body["room"])
	}
}

func TestWatchValidatesViewerID(t *testing.T) {
	srv, h := newTestServer(t, nil)
	h.ConfirmLive("alex", nil)

	for _, body := range []string{"", `{}`, `{"viewerId":"bad id!"}`, `{"viewerId":"` + strings.Repeat("a", 65) + `"}`} {
		rec, out := do(t, srv, http.MethodPost, "/api/rooms/alex/watch", body)
		if rec.Code != http.StatusBadRequest || out["code"] != "invalid_viewer" {
			t.Fatalf("请求体 %q 应返回 400 invalid_viewer，实际 %d %v", body, rec.Code, out)
		}
	}

	// 房间不存在时心跳返回 404，前端据此停止重试。
	if rec, _ := do(t, srv, http.MethodPost, "/api/rooms/nobody/watch", `{"viewerId":"v1"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("房间不存在应返回 404，实际 %d", rec.Code)
	}
}

func TestSSESendsInitialSnapshot(t *testing.T) {
	srv, h := newTestServer(t, nil)
	h.ConfirmLive("alex", nil)

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		srv.Handler().ServeHTTP(rec, req)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.Body.String(), "event: rooms") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	out := rec.Body.String()
	if !strings.Contains(out, "retry: 3000") {
		t.Fatalf("应设置重连间隔：%s", out)
	}
	if !strings.Contains(out, "event: rooms") || !strings.Contains(out, `"username":"alex"`) {
		t.Fatalf("应推送房间快照：%s", out)
	}
	if rec.Header().Get("Content-Type") != "text/event-stream; charset=utf-8" {
		t.Fatalf("Content-Type 异常：%s", rec.Header().Get("Content-Type"))
	}
}
