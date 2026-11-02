package httpapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
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

// mustLive 让测试快速构造一间已鉴权并开播的房间。
func mustLive(t *testing.T, h *hub.Hub, username string) {
	t.Helper()
	res := h.Bind(hub.BindInput{
		Username:  username,
		RoomName:  username,
		Token:     testToken,
		SessionID: "live-" + username,
	})
	if !res.Allowed {
		t.Fatalf("鉴权应通过，实际被拒: %s %s", res.Reason, res.Message)
	}
	h.ConfirmLive(username, nil)
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

// SSE 连接必须同时受全局与单 IP 上限约束，否则一个脚本就能吃光描述符。
func TestEventStreamLimits(t *testing.T) {
	s := &Server{}
	for i := 0; i < maxEventStreamsPerIP; i++ {
		if !s.acquireEvent("192.0.2.1") {
			t.Fatalf("第 %d 条连接不应被拒绝", i+1)
		}
	}
	if s.acquireEvent("192.0.2.1") {
		t.Fatal("同一 IP 超过上限后应拒绝")
	}
	if !s.acquireEvent("192.0.2.2") {
		t.Fatal("其它 IP 不应受影响")
	}
	for i := 0; i < maxEventStreamsPerIP; i++ {
		s.releaseEvent("192.0.2.1")
	}
	s.releaseEvent("192.0.2.2")
	if !s.acquireEvent("192.0.2.1") {
		t.Fatal("释放后应重新可用")
	}
}

func TestSecurityHeaders(t *testing.T) {
	srv, _ := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "object-src 'none'") {
		t.Fatalf("CSP 缺失或过于宽松：%q", csp)
	}
	if got := rec.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("明文 HTTP 不应发送 HSTS，实际 %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.TLS = &tls.ConnectionState{}
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if got := rec.Header().Get("Strict-Transport-Security"); !strings.Contains(got, "max-age=31536000") {
		t.Fatalf("HTTPS 应发送 HSTS，实际 %q", got)
	}
}

// 客户端中断 HLS 分片下载时 ReverseProxy 会 panic(http.ErrAbortHandler)，
// recoverer 必须静默吞掉，不能变成 500 或 ERROR 日志。
func TestRecovererRepanicsAbortHandler(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	h := srv.recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	req := httptest.NewRequest(http.MethodGet, "/hls/live/alex/seg.mp4", nil)
	rec := httptest.NewRecorder()
	defer func() {
		if got := recover(); got != http.ErrAbortHandler {
			t.Fatalf("应原样抛回哨兵值，实际 %v", got)
		}
		if rec.Code == http.StatusInternalServerError {
			t.Fatalf("ErrAbortHandler 不应变成 500：%d", rec.Code)
		}
	}()
	h.ServeHTTP(rec, req)
	t.Fatal("ErrAbortHandler 应继续向上抛出")
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
	mustLive(t, h, "alex")

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

// 配了 PUBLIC_BASE_URL 时播放地址要用绝对地址，方便反代到其它域名。
func TestPlayUsesPublicBaseURL(t *testing.T) {
	srv, h := newTestServer(t, func(c *config.Config) {
		c.PublicBaseURL = "https://live.example.com/"
	})
	mustLive(t, h, "alex")

	_, body := do(t, srv, http.MethodGet, "/api/rooms/alex/play", "")
	play := body["playback"].(map[string]any)
	if play["hlsUrl"] != "https://live.example.com/hls/live/alex/index.m3u8" {
		t.Fatalf("HLS 绝对地址异常：%v", play["hlsUrl"])
	}
	if play["whepUrl"] != "https://live.example.com/rtc/live/alex/whep" {
		t.Fatalf("WHEP 绝对地址异常：%v", play["whepUrl"])
	}
}

// 断流保留期不能给播放地址，否则前端会播一个不存在的流。
func TestPlayInGraceHasNoPlayback(t *testing.T) {
	srv, h := newTestServer(t, nil)
	mustLive(t, h, "alex")
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

	for _, action := range []string{"read", "api", "metrics"} {
		rec, _ := do(t, srv, http.MethodPost, "/internal/mtx/auth", authBody(action, "live/alex", "", "s1"))
		if rec.Code != http.StatusOK {
			t.Fatalf("action=%q 应放行，实际 %d", action, rec.Code)
		}
	}

	// 未知 action 必须拒绝，否则 MediaMTX 新增操作时会被静默放行。
	rec, body := do(t, srv, http.MethodPost, "/internal/mtx/auth", authBody("totally_unknown", "live/alex", "", "s1"))
	if rec.Code != http.StatusForbidden || body["code"] != "invalid_action" {
		t.Fatalf("未知 action 应拒绝，实际 %d %v", rec.Code, body)
	}
}

// 反代部署时所有请求的 RemoteAddr 都是 127.0.0.1，不能因此把公网请求当成内网回调。
func TestAuthRejectsForwardedRequestWithoutSecret(t *testing.T) {
	srv, _ := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodPost, "/internal/mtx/auth", strings.NewReader(authBody("read", "live/alex", "", "s1")))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("带转发头且未配置密钥时应拒绝，实际 %d %s", rec.Code, rec.Body.String())
	}
}

// 只有直连来源属于可信代理时才采信 X-Forwarded-For。
func TestAuthUsesForwardedIPOnlyBehindTrustedProxy(t *testing.T) {
	srv, _ := newTestServer(t, func(c *config.Config) {
		c.MediaMTX.AuthSecret = "s3cret"
		c.TrustedProxies = mustCIDRs(t, "127.0.0.0/8")
	})

	send := func(remote, forwarded string) int {
		req := httptest.NewRequest(http.MethodPost, "/internal/mtx/auth?secret=s3cret", strings.NewReader(authBody("read", "live/alex", "", "s1")))
		req.RemoteAddr = remote
		req.Header.Set("Content-Type", "application/json")
		if forwarded != "" {
			req.Header.Set("X-Forwarded-For", forwarded)
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec.Code
	}

	if code := send("127.0.0.1:50000", "203.0.113.9"); code != http.StatusForbidden {
		t.Fatalf("公网来源经可信代理转发后应拒绝，实际 %d", code)
	}
	if code := send("203.0.113.9:50000", ""); code != http.StatusForbidden {
		t.Fatalf("非白名单直连来源应拒绝，实际 %d", code)
	}
	if code := send("127.0.0.1:50000", ""); code != http.StatusOK {
		t.Fatalf("本机直连来源应放行，实际 %d", code)
	}
}

func TestEventStreamKeyAggregatesIPv6By64(t *testing.T) {
	a := eventStreamKey("2001:db8:1:2::1")
	b := eventStreamKey("2001:db8:1:2:ffff::9")
	c := eventStreamKey("2001:db8:1:3::1")
	if a != b {
		t.Fatalf("同一 /64 应聚合到同一个键：%q vs %q", a, b)
	}
	if a == c {
		t.Fatalf("不同 /64 不应共用键：%q", a)
	}
	if got := eventStreamKey("192.0.2.10"); got != "192.0.2.10" {
		t.Fatalf("IPv4 应原样使用，实际 %q", got)
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
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("密钥正确时应放行，实际 %d %s", rr.Code, rr.Body.String())
	}

	// 也可以放在请求头里。
	req = httptest.NewRequest(http.MethodPost, "/internal/mtx/auth", strings.NewReader(authBody("read", "live/alex", "", "s1")))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Content-Type", "application/json")
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
	mustLive(t, h, "alex")

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

// 非 JSON 的请求体不应被接受，避免跨站表单直接伪造人数。
func TestWatchRequiresJSONContentType(t *testing.T) {
	srv, h := newTestServer(t, nil)
	mustLive(t, h, "alex")

	req := httptest.NewRequest(http.MethodPost, "/api/rooms/alex/watch", strings.NewReader(`{"viewerId":"v1"}`))
	req.Header.Set("Content-Type", "text/plain")
	req.RemoteAddr = "127.0.0.1:50000"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("应拒绝非 JSON 请求体，实际 %d %s", rec.Code, rec.Body.String())
	}
	if view, _ := h.Room("alex"); view.ViewerCount != 0 {
		t.Fatalf("不应计入人数：%d", view.ViewerCount)
	}
}

// 反代可能改写 Host：只要浏览器声明 Sec-Fetch-Site: same-origin 就放行；
// 没有该头时才退回比对 Origin，跨站来源仍然拒绝。
func TestWatchOriginChecks(t *testing.T) {
	srv, h := newTestServer(t, nil)
	mustLive(t, h, "alex")

	post := func(site, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/rooms/alex/watch", strings.NewReader("{\"viewerId\":\"v1\"}"))
		req.Header.Set("Content-Type", "application/json")
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		req.RemoteAddr = "127.0.0.1:50000"
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	if rec := post("cross-site", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("跨站请求应被拒绝，实际 %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("same-origin", "https://cdn.example.com"); rec.Code != http.StatusOK {
		t.Fatalf("反代改写 Host 时同源请求应放行，实际 %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("", "https://evil.example"); rec.Code != http.StatusForbidden {
		t.Fatalf("没有 Sec-Fetch-Site 时跨源 Origin 应被拒绝，实际 %d", rec.Code)
	}
	if rec := post("", "http://example.com"); rec.Code != http.StatusOK {
		t.Fatalf("同源 Origin 应放行，实际 %d %s", rec.Code, rec.Body.String())
	}
}

func TestWatchValidatesViewerID(t *testing.T) {
	srv, h := newTestServer(t, nil)
	mustLive(t, h, "alex")

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

// syncRecorder 让测试在 handler 仍在写响应时也能安全读取已写内容。
// httptest.ResponseRecorder 不是并发安全的，SSE 的轮询读取必须加锁。
type syncRecorder struct {
	mu  sync.Mutex
	rec *httptest.ResponseRecorder
}

func newSyncRecorder() *syncRecorder {
	return &syncRecorder{rec: httptest.NewRecorder()}
}

func (r *syncRecorder) Header() http.Header { return r.rec.Header() }

func (r *syncRecorder) WriteHeader(code int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rec.WriteHeader(code)
}

func (r *syncRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rec.Write(p)
}

func (r *syncRecorder) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rec.Flush()
}

func (r *syncRecorder) BodyString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rec.Body.String()
}

func TestSSESendsInitialSnapshot(t *testing.T) {
	srv, h := newTestServer(t, nil)
	mustLive(t, h, "alex")

	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	rec := newSyncRecorder()
	done := make(chan struct{})
	go func() {
		srv.Handler().ServeHTTP(rec, req)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.BodyString(), "event: rooms") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	out := rec.BodyString()
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

// XFF 最左段由客户端决定，不能当作真实 IP：从右往左剥离可信代理；
// 整条链都可信时宁可回退到直连地址，也不采信最左段。
func TestClientIPSkipsTrustedProxiesFromTheRight(t *testing.T) {
	srv, _ := newTestServer(t, func(c *config.Config) {
		c.TrustedProxies = mustCIDRs(t, "127.0.0.0/8", "10.0.0.0/8")
	})

	req := httptest.NewRequest(http.MethodGet, "/api/rooms", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.1.2.3")
	if got := srv.clientIP(req); got != "203.0.113.9" {
		t.Fatalf("应取最右侧不可信地址，实际 %q", got)
	}

	req.Header.Set("X-Forwarded-For", "10.0.0.2, 10.0.0.3")
	if got := srv.clientIP(req); got != "127.0.0.1" {
		t.Fatalf("全部可信时应回退直连地址，实际 %q", got)
	}
}

// 配置 INTERNAL_ADDR 后，公网端口不应再注册回调路由。
func TestInternalAddrRemovesPublicAuthRoute(t *testing.T) {
	srv, _ := newTestServer(t, func(c *config.Config) {
		c.InternalAddr = "127.0.0.1:18856"
	})

	rec, _ := do(t, srv, http.MethodPost, "/internal/mtx/auth", authBody("publish", "live/alex", "token="+testToken, "s1"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("公网端口不应暴露回调，实际 %d %s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/internal/mtx/auth", strings.NewReader(authBody("publish", "live/alex", "token="+testToken, "s1")))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	srv.InternalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("内部端口应处理回调，实际 %d %s", rec.Code, rec.Body.String())
	}
}

// 鉴权回调是服务端到服务端的请求：MediaMTX 既不发 Sec-Fetch-Site 也不发 Origin。
// 浏览器不一样，跨站写入一定带其中之一，所以同源检查能把网页挡在解析之前。
func TestMTXAuthRejectsCrossSiteRequests(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	h := srv.InternalHandler()

	post := func(headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/internal/mtx/auth", strings.NewReader(authBody("publish", "live/alex", "token="+testToken, "s1")))
		req.RemoteAddr = "127.0.0.1:50000"
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	for _, headers := range []map[string]string{
		{"Content-Type": "application/json", "Sec-Fetch-Site": "cross-site"},
		{"Content-Type": "application/json; charset=utf-8", "Sec-Fetch-Site": "same-site"},
		{"Content-Type": "application/json", "Origin": "https://evil.example"},
	} {
		rec := post(headers)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("跨站回调应被拒绝（%v），实际 %d %s", headers, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "cross_origin") {
			t.Fatalf("应返回 cross_origin，实际 %s", rec.Body.String())
		}
	}

	// 同源页面（没有 Sec-Fetch-Site 的老浏览器）与 MediaMTX 服务端请求都应放行。
	if rec := post(map[string]string{"Content-Type": "application/json", "Origin": "http://example.com"}); rec.Code != http.StatusOK {
		t.Fatalf("同源请求应放行，实际 %d %s", rec.Code, rec.Body.String())
	}
	if rec := post(map[string]string{"Content-Type": "application/json"}); rec.Code != http.StatusOK {
		t.Fatalf("服务端回调应放行，实际 %d %s", rec.Code, rec.Body.String())
	}
}

// 跨站表单只能发 text/plain 之类的简单类型，所以非 JSON 的请求体必须在解析前丢掉。
func TestMTXAuthRequiresJSONContentType(t *testing.T) {
	srv, h := newTestServer(t, nil)

	for _, contentType := range []string{"", "text/plain", "application/x-www-form-urlencoded", "application/jsonx"} {
		req := httptest.NewRequest(http.MethodPost, "/internal/mtx/auth", strings.NewReader(authBody("publish", "live/alex", "token="+testToken, "s1")))
		req.RemoteAddr = "127.0.0.1:50000"
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		srv.InternalHandler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("Content-Type=%q 应返回 415，实际 %d %s", contentType, rec.Code, rec.Body.String())
		}
	}
	if _, ok := h.Room("alex"); ok {
		t.Fatal("被拒绝的请求不应真的建立推流会话")
	}

	// 带 charset 的 JSON 是 MediaMTX/curl 的常见写法，不能误伤。
	req := httptest.NewRequest(http.MethodPost, "/internal/mtx/auth", strings.NewReader(authBody("publish", "live/alex", "token="+testToken, "s1")))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	rec := httptest.NewRecorder()
	srv.InternalHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("带 charset 的 JSON 应放行，实际 %d %s", rec.Code, rec.Body.String())
	}
}

// 人数按 viewerId 去重，而 viewerId 由浏览器生成：同一房间、同一客户端
// 换着 ID 刷心跳必须封顶，否则一个人就能把人数顶到房间上限。
func TestWatchCapsViewersPerClient(t *testing.T) {
	srv, h := newTestServer(t, nil)
	mustLive(t, h, "alex")

	post := func(remote, viewer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/rooms/alex/watch", strings.NewReader(`{"viewerId":"`+viewer+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		return rec
	}

	for i := 0; i < maxViewersPerRoomIP; i++ {
		if rec := post("127.0.0.1:50000", fmt.Sprintf("viewer-%d", i)); rec.Code != http.StatusOK {
			t.Fatalf("第 %d 个观众应放行，实际 %d %s", i+1, rec.Code, rec.Body.String())
		}
	}
	// 满额后重复心跳仍然放行，且不增加人数。
	if rec := post("127.0.0.1:50000", "viewer-0"); rec.Code != http.StatusOK {
		t.Fatalf("重复心跳应放行，实际 %d %s", rec.Code, rec.Body.String())
	}
	if view, ok := h.Room("alex"); !ok || view.ViewerCount != maxViewersPerRoomIP {
		t.Fatalf("重复心跳不应增加人数，实际 %+v", view)
	}

	rec := post("127.0.0.1:50000", "viewer-over")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("超限的新 viewerId 应返回 429，实际 %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "room_full") {
		t.Fatalf("应返回 room_full 错误码，实际 %s", rec.Body.String())
	}
	if view, ok := h.Room("alex"); !ok || view.ViewerCount != maxViewersPerRoomIP {
		t.Fatalf("超限请求不应计入人数，实际 %+v", view)
	}

	// 主动离开必须立刻释放 viewerCap 配额，不能等 TTL：否则同一网络的新观众
	// 在旧 viewerId 过期前都会看到 room_full。
	req := httptest.NewRequest(http.MethodDelete, "/api/rooms/alex/watch?viewerId=viewer-0", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("主动离开应返回 204，实际 %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("127.0.0.1:50000", "viewer-over"); rec.Code != http.StatusOK {
		t.Fatalf("释放配额后新观众应放行，实际 %d %s", rec.Code, rec.Body.String())
	}

	// 其它网络不受影响。
	if rec := post("198.51.100.7:50000", "viewer-other"); rec.Code != http.StatusOK {
		t.Fatalf("其它客户端应放行，实际 %d %s", rec.Code, rec.Body.String())
	}
}
