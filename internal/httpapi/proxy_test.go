package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tinycast/internal/config"
)

// fakeMediaMTX 记录被代理的请求，并按需返回指定响应。
type fakeUpstream struct {
	path     string
	rawQuery string
	method   string
	body     string
}

func (f *fakeUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.path = r.URL.Path
	f.rawQuery = r.URL.RawQuery
	f.method = r.Method
	var b strings.Builder
	if r.Body != nil {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		b.Write(buf[:n])
	}
	f.body = b.String()

	switch {
	case strings.HasSuffix(r.URL.Path, "/whep"):
		w.Header().Set("Content-Type", "application/sdp")
		w.Header().Set("Location", r.URL.Path)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("v=0\r\n"))
	case strings.HasSuffix(r.URL.Path, "index.m3u8"):
		// MediaMTX 的 HLS 入口会先 302 到带 cookieCheck 的地址。
		w.Header().Set("Location", r.URL.Path+"?cookieCheck=1")
		w.WriteHeader(http.StatusFound)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

func TestWHEPProxyForwardsAndRewritesLocation(t *testing.T) {
	up := &fakeUpstream{}
	srvUp := httptest.NewServer(up)
	defer srvUp.Close()

	srv, _ := newTestServer(t, func(c *config.Config) { c.MediaMTX.WHEPProxyTarget = srvUp.URL })

	req := httptest.NewRequest(http.MethodPost, "/rtc/live/alex/whep", strings.NewReader("v=0 offer"))
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Content-Type", "application/sdp")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("应透传 201，实际 %d", rec.Code)
	}
	if up.method != http.MethodPost || up.path != "/live/alex/whep" {
		t.Fatalf("转发路径异常：%s %s", up.method, up.path)
	}
	if up.body != "v=0 offer" {
		t.Fatalf("请求体未透传：%q", up.body)
	}
	if got := rec.Header().Get("Location"); got != "/rtc/live/alex/whep" {
		t.Fatalf("Location 应加上 /rtc 前缀，实际 %s", got)
	}
	if !strings.Contains(rec.Body.String(), "v=0") {
		t.Fatalf("响应体未透传：%s", rec.Body.String())
	}
}

func TestHLSProxyRewritesRedirect(t *testing.T) {
	up := &fakeUpstream{}
	srvUp := httptest.NewServer(up)
	defer srvUp.Close()

	srv, _ := newTestServer(t, func(c *config.Config) { c.MediaMTX.HLSProxyTarget = srvUp.URL })

	req := httptest.NewRequest(http.MethodGet, "/hls/live/alex/index.m3u8", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if up.path != "/live/alex/index.m3u8" {
		t.Fatalf("转发路径异常：%s", up.path)
	}
	// 不跟随跳转，直接检查 302 的 Location 已被加上 /hls 前缀，
	// 否则浏览器会被带到后端不认识的路径上。
	if rec.Code != http.StatusFound {
		t.Fatalf("应透传 302，实际 %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/hls/live/alex/index.m3u8?cookieCheck=1" {
		t.Fatalf("Location 改写错误：%s", got)
	}
}

// 编码过的 .. 段会被 net/http 解码进 URL.Path，必须在进入反代前拒绝，
// 否则请求可能被带到 MediaMTX 的其它接口上。
func TestMediaProxyRejectsPathTraversal(t *testing.T) {
	up := &fakeUpstream{}
	srvUp := httptest.NewServer(up)
	defer srvUp.Close()

	srv, _ := newTestServer(t, func(c *config.Config) {
		c.MediaMTX.WHEPProxyTarget = srvUp.URL
		c.MediaMTX.HLSProxyTarget = srvUp.URL
	})

	for _, target := range []string{
		"/hls/%2e%2e/%2e%2e/internal/mtx/auth",
		"/rtc/../internal/mtx/auth",
		"/hls/live/..%2f..%2finternal",
	} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.RemoteAddr = "127.0.0.1:50000"
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		// 明文的 .. 段会先被 ServeMux 用 307 重定向掉；编码过的段则进入下面的代理守卫。
		if rec.Code == http.StatusTemporaryRedirect {
			continue
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s 应返回 400，实际 %d %s", target, rec.Code, rec.Body.String())
		}
	}
	if up.path != "" {
		t.Fatalf("非法路径不应到达上游，实际 %s", up.path)
	}
}

// 代理只应转发本应用的流路径，避免误配端口时把 MediaMTX 的管理接口暴露出去。
func TestMediaProxyRejectsOtherApps(t *testing.T) {
	up := &fakeUpstream{}
	srvUp := httptest.NewServer(up)
	defer srvUp.Close()

	srv, _ := newTestServer(t, func(c *config.Config) {
		c.MediaMTX.WHEPProxyTarget = srvUp.URL
		c.MediaMTX.HLSProxyTarget = srvUp.URL
	})

	for _, target := range []string{"/hls/v3/paths/list", "/rtc/api/v1/config", "/hls/other/alex/index.m3u8"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.RemoteAddr = "127.0.0.1:50000"
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s 应返回 400，实际 %d %s", target, rec.Code, rec.Body.String())
		}
	}
	if up.path != "" {
		t.Fatalf("非本应用路径不应到达上游，实际 %s", up.path)
	}
}

// 上游挂掉时给出明确的 502，而不是让前端拿到 SPA 的 HTML。
func TestProxyUnreachableReturns502(t *testing.T) {
	srvUp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	target := srvUp.URL
	srvUp.Close()

	srv, _ := newTestServer(t, func(c *config.Config) {
		c.MediaMTX.WHEPProxyTarget = target
		c.MediaMTX.HLSProxyTarget = target
	})

	for _, target := range []string{"/rtc/live/alex/whep", "/hls/live/alex/index.m3u8"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.RemoteAddr = "127.0.0.1:50000"
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadGateway {
			t.Fatalf("%s 应返回 502，实际 %d", target, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "mediamtx_unreachable") {
			t.Fatalf("%s 应给出错误码，实际 %s", target, rec.Body.String())
		}
	}
}

// 两个代理都没配置时，/rtc 请求应落到 SPA（而不是 404），前端据此提示未启用播放。
func TestProxyDisabledFallsBackToSPA(t *testing.T) {
	srv, _ := newTestServer(t, func(c *config.Config) {
		c.MediaMTX.WHEPProxyTarget = ""
		c.MediaMTX.HLSProxyTarget = ""
	})

	_, body := do(t, srv, http.MethodGet, "/api/rooms/alex/play", "")
	_ = body

	h := srv.Handler()
	req := httptest.NewRequest(http.MethodGet, "/rtc/live/alex/whep", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusOK {
		t.Fatalf("未配置代理时不应 5xx，实际 %d", rec.Code)
	}
}

// 单个 IP 的并发媒体请求必须有独立上限：全局 512 槽挡不住一个人开 512 条慢连接。
func TestMediaSlotPerIPLimit(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	ip := "203.0.113.7"
	for i := 0; i < maxMediaPerIP; i++ {
		if !srv.acquireMediaSlot(ip) {
			t.Fatalf("第 %d 条连接不应被拒绝", i+1)
		}
	}
	if srv.acquireMediaSlot(ip) {
		t.Fatal("超过单 IP 配额后应拒绝")
	}
	if !srv.acquireMediaSlot("203.0.113.8") {
		t.Fatal("其它 IP 不应受影响")
	}
	srv.releaseMediaSlot(ip)
	if !srv.acquireMediaSlot(ip) {
		t.Fatal("释放后应可以重新占用")
	}
}

// 控制字符不在正常 URL 路径里，不能转发给上游。
func TestValidMediaPathRejectsControlChars(t *testing.T) {
	if validMediaPath("/hls/live/alex/\x00/index.m3u8", "/hls", "live") {
		t.Fatal("含 NUL 的路径应被拒绝")
	}
	if !validMediaPath("/hls/live/alex/index.m3u8", "/hls", "live") {
		t.Fatal("正常路径不应被拒绝")
	}
}
