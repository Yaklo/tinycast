package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"tinycast/internal/config"
	"tinycast/internal/hub"
)

const testSecret = "unit-test-secret"

// newFixture 构造一个使用内存状态中心的测试服务。
//
// 白名单里包含 192.0.2.0/24 —— httptest 的默认来源地址就落在这个网段。
func newFixture(t *testing.T, mutate func(*config.Config)) (*Server, *hub.Hub) {
	t.Helper()

	cfg := &config.Config{
		HTTPAddr:        ":8080",
		RoomGrace:       5 * time.Minute,
		JanitorInterval: 15 * time.Second,
		SRS: config.SRSConfig{
			App:              "live",
			CallbackSecret:   testSecret,
			RTMPPublicBase:   "rtmp://live.example.com/live",
			WebRTCPublicBase: "webrtc://live.example.com/live",
		},
	}
	for _, cidr := range []string{"127.0.0.0/8", "192.0.2.0/24"} {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatalf("解析测试网段失败: %v", err)
		}
		cfg.SRS.CallbackAllowed = append(cfg.SRS.CallbackAllowed, network)
	}
	if mutate != nil {
		mutate(cfg)
	}

	h := hub.New(hub.Options{Grace: cfg.RoomGrace})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(h, cfg, logger), h
}

const (
	aliceToken = "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_abcd"
	bobToken   = "ZzYyXxWwVvUuTtSsRrQqPpOoNnMmLlKkJJii9876-"
)

// callbackBody 对应 SRS 回调请求体中的字段（测试里自己序列化成 snake_case）。
type callbackBody struct {
	Action   string
	ClientID string
	App      string
	Stream   string
	Param    string
}

func (b callbackBody) marshal() ([]byte, error) {
	return json.Marshal(map[string]string{
		"action":    b.Action,
		"client_id": b.ClientID,
		"app":       b.App,
		"stream":    b.Stream,
		"param":     b.Param,
	})
}

// postCallback 发起一次 SRS 回调，返回响应。
func postCallback(t *testing.T, srv *Server, secret string, body callbackBody) *httptest.ResponseRecorder {
	t.Helper()

	raw, err := body.marshal()
	if err != nil {
		t.Fatalf("序列化回调体失败: %v", err)
	}
	target := "/internal/srs/" + body.Action
	if secret != "" {
		target += "?secret=" + url.QueryEscape(secret)
	}
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func publishBody(username, room, token, clientID string) callbackBody {
	param := url.Values{}
	param.Set("room", room)
	param.Set("token", token)
	return callbackBody{
		Action:   actionPublish,
		ClientID: clientID,
		App:      "live",
		Stream:   username,
		Param:    "?" + param.Encode(),
	}
}

// 一次合法推流回调应放行并创建房间。
func TestSRSPublishAllowed(t *testing.T) {
	srv, h := newFixture(t, nil)

	rec := postCallback(t, srv, testSecret, publishBody("alice", "爱丽丝的直播间", aliceToken, "c1"))
	if rec.Code != http.StatusOK || rec.Body.String() != "0" {
		t.Fatalf("应放行（200/0），实际 %d/%q", rec.Code, rec.Body.String())
	}
	view, ok := h.Room("alice")
	if !ok || view.State != hub.StateLive {
		t.Fatalf("应创建 LIVE 房间，实际 %+v ok=%v", view, ok)
	}
	if view.Room != "爱丽丝的直播间" {
		t.Fatalf("房间名解析错误: %q", view.Room)
	}
}

// 令牌非法时应拒绝，且 SRS 能看到的响应体不能是 0。
func TestSRSPublishRejectedOnInvalidToken(t *testing.T) {
	srv, h := newFixture(t, nil)

	rec := postCallback(t, srv, testSecret, publishBody("alice", "房间", "tooshort", "c1"))
	if rec.Code == http.StatusOK || rec.Body.String() == "0" {
		t.Fatalf("应拒绝，实际 %d/%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Tinycast-Reason"); got != string(hub.ReasonInvalidToken) {
		t.Fatalf("拒绝原因头应为 %s，实际 %q", hub.ReasonInvalidToken, got)
	}
	if len(h.Snapshot().Rooms) != 0 {
		t.Fatal("被拒绝的推流不应创建房间")
	}
}

// 同一身份的第二条推流连接必须被拒绝。
func TestSRSPublishRejectsSecondConnection(t *testing.T) {
	srv, _ := newFixture(t, nil)

	if rec := postCallback(t, srv, testSecret, publishBody("alice", "房间", aliceToken, "c1")); rec.Body.String() != "0" {
		t.Fatalf("首次推流应放行，实际 %d/%q", rec.Code, rec.Body.String())
	}
	rec := postCallback(t, srv, testSecret, publishBody("alice", "房间", aliceToken, "c2"))
	if rec.Body.String() == "0" {
		t.Fatal("第二条连接应被拒绝")
	}
	if got := rec.Header().Get("X-Tinycast-Reason"); got != string(hub.ReasonAlreadyPublishing) {
		t.Fatalf("拒绝原因应为 %s，实际 %q", hub.ReasonAlreadyPublishing, got)
	}
}

// 回调密钥缺失或错误时一律拒绝。
func TestSRSCallbackRequiresSecret(t *testing.T) {
	srv, _ := newFixture(t, nil)

	for _, secret := range []string{"", "wrong-secret"} {
		rec := postCallback(t, srv, secret, publishBody("alice", "房间", aliceToken, "c1"))
		if rec.Code != http.StatusForbidden || rec.Body.String() == "0" {
			t.Fatalf("secret=%q 应被拒绝，实际 %d/%q", secret, rec.Code, rec.Body.String())
		}
	}
}

// 来自非白名单地址的回调一律拒绝，即使密钥正确。
func TestSRSCallbackRejectsForeignIP(t *testing.T) {
	srv, _ := newFixture(t, func(cfg *config.Config) {
		_, network, _ := net.ParseCIDR("10.0.0.0/8")
		cfg.SRS.CallbackAllowed = []*net.IPNet{network}
	})

	rec := postCallback(t, srv, testSecret, publishBody("alice", "房间", aliceToken, "c1"))
	if rec.Code != http.StatusForbidden || rec.Body.String() == "0" {
		t.Fatalf("外部地址的回调应被拒绝，实际 %d/%q", rec.Code, rec.Body.String())
	}
}

// 未配置密钥但显式允许任意来源时（本地调试）回调应放行。
func TestSRSCallbackAllowAll(t *testing.T) {
	srv, h := newFixture(t, func(cfg *config.Config) {
		cfg.SRS.CallbackSecret = ""
		cfg.SRS.CallbackAllowAll = true
	})

	rec := postCallback(t, srv, "", publishBody("alice", "房间", aliceToken, "c1"))
	if rec.Code != http.StatusOK || rec.Body.String() != "0" {
		t.Fatalf("应放行，实际 %d/%q", rec.Code, rec.Body.String())
	}
	if _, ok := h.Room("alice"); !ok {
		t.Fatal("应创建房间")
	}
}

// on_unpublish 让房间进入 GRACE，重推后恢复 LIVE。
func TestSRSUnpublishAndRepublish(t *testing.T) {
	srv, h := newFixture(t, nil)

	postCallback(t, srv, testSecret, publishBody("alice", "房间", aliceToken, "c1"))

	rec := postCallback(t, srv, testSecret, callbackBody{
		Action: actionUnpublish, ClientID: "c1", App: "live", Stream: "alice",
	})
	if rec.Code != http.StatusOK || rec.Body.String() != "0" {
		t.Fatalf("on_unpublish 应放行，实际 %d/%q", rec.Code, rec.Body.String())
	}
	view, _ := h.Room("alice")
	if view.State != hub.StateGrace {
		t.Fatalf("应进入 GRACE，实际 %s", view.State)
	}
	if view.GraceUntil == 0 {
		t.Fatal("GRACE 应带释放时间")
	}

	// 恢复推流。
	postCallback(t, srv, testSecret, publishBody("alice", "房间", aliceToken, "c2"))
	view, _ = h.Room("alice")
	if view.State != hub.StateLive {
		t.Fatalf("重推后应为 LIVE，实际 %s", view.State)
	}
}

// 观看回调驱动在线人数，重复回调不重复计数。
func TestSRSViewerCounting(t *testing.T) {
	srv, h := newFixture(t, nil)
	postCallback(t, srv, testSecret, publishBody("alice", "房间", aliceToken, "c1"))

	for _, id := range []string{"v1", "v1", "v2"} {
		postCallback(t, srv, testSecret, callbackBody{
			Action: actionPlay, ClientID: id, App: "live", Stream: "alice",
		})
	}
	view, _ := h.Room("alice")
	if view.ViewerCount != 2 {
		t.Fatalf("观看人数应为 2，实际 %d", view.ViewerCount)
	}

	postCallback(t, srv, testSecret, callbackBody{Action: actionStop, ClientID: "v1", App: "live", Stream: "alice"})
	postCallback(t, srv, testSecret, callbackBody{Action: actionStop, ClientID: "v1", App: "live", Stream: "alice"})
	view, _ = h.Room("alice")
	if view.ViewerCount != 1 {
		t.Fatalf("退出后观看人数应为 1，实际 %d", view.ViewerCount)
	}
}

// 其他 app 的回调应被放行但不影响本服务的状态。
func TestSRSCallbackIgnoresOtherApp(t *testing.T) {
	srv, h := newFixture(t, nil)

	body := publishBody("alice", "房间", aliceToken, "c1")
	body.App = "other"
	rec := postCallback(t, srv, testSecret, body)
	if rec.Code != http.StatusOK || rec.Body.String() != "0" {
		t.Fatalf("外部 app 的回调应直接放行，实际 %d/%q", rec.Code, rec.Body.String())
	}
	if len(h.Snapshot().Rooms) != 0 {
		t.Fatal("外部 app 不应创建房间")
	}
}

// on_publish 请求体无法解析时必须拒绝（不能"解析失败就放行"）。
func TestSRSPublishBadPayloadRejected(t *testing.T) {
	srv, _ := newFixture(t, nil)

	req := httptest.NewRequest(http.MethodPost, "/internal/srs/on_publish?secret="+url.QueryEscape(testSecret), strings.NewReader("{not json"))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code == http.StatusOK || rec.Body.String() == "0" {
		t.Fatalf("非法请求体应被拒绝，实际 %d/%q", rec.Code, rec.Body.String())
	}
}

// 未知回调类型返回 404，避免被当成有效回调。
func TestSRSUnknownAction(t *testing.T) {
	srv, _ := newFixture(t, nil)
	rec := postCallback(t, srv, testSecret, callbackBody{Action: "on_something", ClientID: "c1"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知回调应返回 404，实际 %d", rec.Code)
	}
}

// 房间列表接口只暴露展示字段，绝不包含推流令牌或其摘要。
func TestRoomsEndpointHidesToken(t *testing.T) {
	srv, _ := newFixture(t, nil)
	postCallback(t, srv, testSecret, publishBody("alice", "房间", aliceToken, "c1"))

	req := httptest.NewRequest(http.MethodGet, "/api/rooms", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("房间列表应返回 200，实际 %d", rec.Code)
	}
	raw := rec.Body.String()
	if strings.Contains(raw, aliceToken) {
		t.Fatal("房间列表不得包含推流令牌明文")
	}
	if strings.Contains(raw, hub.HashToken(aliceToken)) {
		t.Fatal("房间列表不得包含推流令牌摘要")
	}

	var payload struct {
		Rooms []hub.RoomView
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析房间列表失败: %v", err)
	}
	if len(payload.Rooms) != 1 || payload.Rooms[0].Username != "alice" {
		t.Fatalf("房间列表内容不正确: %+v", payload.Rooms)
	}
}

// 房间详情：不存在时返回 404。
func TestRoomDetailNotFound(t *testing.T) {
	srv, _ := newFixture(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/rooms/ghost", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在的房间应返回 404，实际 %d", rec.Code)
	}
}

// 播放信息：LIVE 时给出 WHEP 地址，GRACE 时不给播放地址。
func TestPlayInfo(t *testing.T) {
	srv, _ := newFixture(t, nil)
	postCallback(t, srv, testSecret, publishBody("alice", "房间", aliceToken, "c1"))

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/rooms/alice/play", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("播放信息应返回 200，实际 %d", rec.Code)
	}
	var live playResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &live); err != nil {
		t.Fatalf("解析播放信息失败: %v", err)
	}
	if live.Playback == nil {
		t.Fatal("LIVE 状态应返回播放信息")
	}
	if !strings.Contains(live.Playback.WhepURL, "/rtc/v1/whep/") {
		t.Fatalf("WHEP 地址不正确: %q", live.Playback.WhepURL)
	}
	if !strings.Contains(live.Playback.WhepURL, "stream=alice") || !strings.Contains(live.Playback.WhepURL, "app=live") {
		t.Fatalf("WHEP 地址应带上 app 与 stream 参数: %q", live.Playback.WhepURL)
	}

	postCallback(t, srv, testSecret, callbackBody{Action: actionUnpublish, ClientID: "c1", App: "live", Stream: "alice"})

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/rooms/alice/play", nil))
	var grace playResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &grace); err != nil {
		t.Fatalf("解析播放信息失败: %v", err)
	}
	if grace.Playback != nil {
		t.Fatal("GRACE 状态不应返回播放地址")
	}
	if grace.GraceUntil == 0 {
		t.Fatal("GRACE 状态应返回剩余保留时间点")
	}
}

// 未被 SRS 使用的 /internal 路径返回 JSON 404，不会被 SPA 回退吃掉。
func TestInternalNotFoundIsJSON(t *testing.T) {
	srv, _ := newFixture(t, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/internal/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("应返回 404，实际 %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("应返回 JSON，实际 %q", ct)
	}
}

// SSE 连接应先收到一帧完整快照。
func TestSSEInitialSnapshot(t *testing.T) {
	srv, _ := newFixture(t, nil)
	postCallback(t, srv, testSecret, publishBody("alice", "房间", aliceToken, "c1"))

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("建立 SSE 连接失败: %v", err)
	}
	defer resp.Body.Close()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type 应为 text/event-stream，实际 %q", ct)
	}

	reader := bufio.NewReader(resp.Body)
	var dataLine string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && dataLine == "" {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("读取 SSE 失败: %v", err)
		}
		if strings.HasPrefix(line, "data: ") {
			dataLine = strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		}
	}
	if dataLine == "" {
		t.Fatal("未收到快照数据")
	}

	var snap hub.Snapshot
	if err := json.Unmarshal([]byte(dataLine), &snap); err != nil {
		t.Fatalf("解析快照失败: %v", err)
	}
	if len(snap.Rooms) != 1 || snap.Rooms[0].Username != "alice" {
		t.Fatalf("首帧快照内容不正确: %+v", snap.Rooms)
	}
}

// 前端未构建时，根路径应返回可读提示而不是 404。
func TestIndexPageServed(t *testing.T) {
	srv, _ := newFixture(t, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("首页应返回 200，实际 %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("首页应返回 HTML，实际 %q", ct)
	}
}

// 公开配置接口不得泄露回调密钥等敏感信息。
func TestConfigEndpointSanitized(t *testing.T) {
	srv, _ := newFixture(t, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("配置接口应返回 200，实际 %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), testSecret) {
		t.Fatal("公开配置不得包含回调密钥")
	}
}

// 健康检查返回基本统计。
func TestHealthz(t *testing.T) {
	srv, _ := newFixture(t, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("健康检查应返回 200，实际 %d", rec.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析健康检查响应失败: %v", err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("健康检查状态不正确: %v", payload["status"])
	}
}
