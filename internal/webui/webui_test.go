package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// 首页必须直接返回 HTML，不能被重定向（http.FileServer 会把 /index.html 301 到 ./）。
func TestServeIndex(t *testing.T) {
	rec := get(t, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("首页应返回 200，实际 %d（重定向到 %q）", rec.Code, rec.Header().Get("Location"))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("首页 Content-Type 错误: %q", ct)
	}
	if body := rec.Body.String(); !strings.Contains(body, "<div id=\"app\">") {
		t.Fatalf("首页内容不像是前端产物: %q", truncate(body))
	}
}

// 前端路由（/r/<username>）也要回退到 index.html。
func TestSPAFallback(t *testing.T) {
	rec := get(t, "/r/alex")
	if rec.Code != http.StatusOK {
		t.Fatalf("前端路由应返回 200，实际 %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "<div id=\"app\">") {
		t.Fatalf("前端路由应回退到 index.html: %q", truncate(body))
	}
}

// 缺失的静态资源应是 404，而不是悄悄返回首页。
func TestMissingAssetIs404(t *testing.T) {
	rec := get(t, "/assets/not-exists.js")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("缺失静态资源应返回 404，实际 %d", rec.Code)
	}
}

// 根目录下的图标必须按静态资源返回，不能回退成 index.html。
func TestServeRootStaticFiles(t *testing.T) {
	dist, err := fs.Sub(assets, "dist")
	if err != nil {
		t.Fatalf("读取内嵌产物失败: %v", err)
	}

	cases := []struct {
		name   string
		prefix string
	}{
		{"favicon.ico", "image/"},
		{"favicon.svg", "image/svg+xml"},
		{"apple-touch-icon.png", "image/png"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := fs.Stat(dist, tc.name); err != nil {
				t.Skipf("dist 里没有 %s（前端未构建）", tc.name)
			}
			rec := get(t, "/"+tc.name)
			if rec.Code != http.StatusOK {
				t.Fatalf("应返回 200，实际 %d", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, tc.prefix) {
				t.Fatalf("Content-Type 应为 %s，实际 %q", tc.prefix, ct)
			}
			if body := rec.Body.String(); strings.Contains(body, "<div id=\"app\">") {
				t.Fatal("不应回退到 index.html")
			}
			if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
				t.Fatalf("图标应使用协商缓存，Cache-Control=%q", cc)
			}
		})
	}
}

// HEAD 请求不应带响应体，但要有正确的 Content-Length。
func TestHeadIndex(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD 应返回 200，实际 %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD 不应返回响应体，实际 %d 字节", rec.Body.Len())
	}
	if rec.Header().Get("Content-Length") == "" {
		t.Fatal("HEAD 应带 Content-Length")
	}
}

func truncate(s string) string {
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}
