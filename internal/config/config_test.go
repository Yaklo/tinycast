package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// clearEnv 清空所有与 tinycast 相关的环境变量，让每个用例从默认值开始。
func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"HTTP_ADDR", "INTERNAL_ADDR", "ROOM_GRACE", "MONITOR_INTERVAL", "VIEWER_TIMEOUT", "PENDING_TIMEOUT",
		"CORS_ALLOWED_ORIGINS", "PUBLIC_BASE_URL", "TRUSTED_PROXIES",
		"MEDIAMTX_APP", "MEDIAMTX_API_URL", "MEDIAMTX_API_USER", "MEDIAMTX_API_PASSWORD",
		"MEDIAMTX_AUTH_SECRET", "MEDIAMTX_AUTH_ALLOWED_CIDRS", "MEDIAMTX_WHEP_PROXY_TARGET",
		"MEDIAMTX_HLS_PROXY_TARGET", "MEDIAMTX_RTMP_PUBLIC_BASE",
	} {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("默认配置应可用，实际报错: %v", err)
	}
	if cfg.HTTPAddr != DefaultHTTPAddr {
		t.Fatalf("默认监听地址应为 %s，实际 %s", DefaultHTTPAddr, cfg.HTTPAddr)
	}
	if cfg.InternalAddr != "" {
		t.Fatalf("默认不应启用独立回调端口，实际 %q", cfg.InternalAddr)
	}
	if cfg.RoomGrace != 5*time.Minute {
		t.Fatalf("默认保留期应为 5 分钟，实际 %s", cfg.RoomGrace)
	}
	if cfg.MediaMTX.App != "live" {
		t.Fatalf("默认应用名应为 live，实际 %s", cfg.MediaMTX.App)
	}
	if cfg.MediaMTX.APIURL != DefaultMediaMTXAPIURL {
		t.Fatalf("默认控制接口地址不符：%s", cfg.MediaMTX.APIURL)
	}
	if len(cfg.MediaMTX.AuthAllowed) == 0 {
		t.Fatal("默认应带内网回调白名单")
	}
	if len(cfg.TrustedProxies) == 0 {
		t.Fatal("默认应只信任本机反代")
	}
	if cfg.MonitorInterval != time.Second {
		t.Fatalf("默认巡检间隔应为 1s，实际 %s", cfg.MonitorInterval)
	}
}

func TestLoadInternalAddr(t *testing.T) {
	clearEnv(t)
	t.Setenv("HTTP_ADDR", "127.0.0.1:18855")
	t.Setenv("INTERNAL_ADDR", "127.0.0.1:18856")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.InternalAddr != "127.0.0.1:18856" {
		t.Fatalf("应启用独立回调端口，实际 %q", cfg.InternalAddr)
	}
}

func TestLoadInternalAddrEqualToHTTPIsRejected(t *testing.T) {
	clearEnv(t)
	t.Setenv("HTTP_ADDR", "127.0.0.1:18855")
	t.Setenv("INTERNAL_ADDR", "127.0.0.1:18855")

	if _, err := Load(); err == nil {
		t.Fatal("显式把 INTERNAL_ADDR 配成 HTTP_ADDR 应拒绝启动")
	} else if !strings.Contains(err.Error(), "同一个监听地址") {
		t.Fatalf("错误信息应说明地址冲突，实际 %v", err)
	}
}

func TestSameListenAddr(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"127.0.0.1:18855", "127.0.0.1:18855", true},
		{"127.0.0.1:18855", "127.0.0.1:18856", false},
		{":18855", "0.0.0.0:18855", true},
		{"0.0.0.0:18855", "[::]:18855", true},
		{"[::1]:18855", "[::1]:18855", true},
		{"localhost:18855", "127.0.0.1:18855", false},
		{"127.0.0.1", "127.0.0.1:18855", false},
	}
	for _, tc := range cases {
		if got := SameListenAddr(tc.a, tc.b); got != tc.want {
			t.Fatalf("SameListenAddr(%q, %q) = %v，期望 %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestLoadRoomGraceFormats(t *testing.T) {
	for raw, want := range map[string]time.Duration{
		"90s":   90 * time.Second,
		"300":   300 * time.Second,
		"2m30s": 150 * time.Second,
	} {
		clearEnv(t)
		t.Setenv("ROOM_GRACE", raw)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("ROOM_GRACE=%s 应可用: %v", raw, err)
		}
		if cfg.RoomGrace != want {
			t.Fatalf("ROOM_GRACE=%s 解析为 %s，期望 %s", raw, cfg.RoomGrace, want)
		}
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	cases := map[string]map[string]string{
		"保留期为负":    {"ROOM_GRACE": "-1m"},
		"巡检间隔为零":   {"MONITOR_INTERVAL": "0s"},
		"心跳超时为零":   {"VIEWER_TIMEOUT": "0s"},
		"绑定期限为零":   {"PENDING_TIMEOUT": "0s"},
		"应用名带斜杠":   {"MEDIAMTX_APP": "live/x"},
		"控制接口地址非法": {"MEDIAMTX_API_URL": "127.0.0.1:9997"},
		"网段非法":     {"MEDIAMTX_AUTH_ALLOWED_CIDRS": "127.0.0.1"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range env {
				t.Setenv(k, v)
			}
			if _, err := Load(); err == nil {
				t.Fatalf("%s 应报错", name)
			}
		})
	}
}

func TestLoadAuthAllowedCIDRs(t *testing.T) {
	clearEnv(t)
	t.Setenv("MEDIAMTX_AUTH_ALLOWED_CIDRS", "10.1.0.0/16, 192.168.5.0/24")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.MediaMTX.AuthAllowed) != 2 {
		t.Fatalf("应解析出 2 个网段，实际 %d", len(cfg.MediaMTX.AuthAllowed))
	}
	if cfg.MediaMTX.AuthAllowed[0].String() != "10.1.0.0/16" {
		t.Fatalf("网段解析错误：%s", cfg.MediaMTX.AuthAllowed[0])
	}
}

func TestLoadTrustedProxies(t *testing.T) {
	clearEnv(t)
	t.Setenv("TRUSTED_PROXIES", "10.9.0.0/16, 2001:db8::/32")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.TrustedProxies) != 2 {
		t.Fatalf("应解析出 2 个可信代理网段，实际 %d", len(cfg.TrustedProxies))
	}
	if cfg.TrustedProxies[0].String() != "10.9.0.0/16" {
		t.Fatalf("网段解析错误：%s", cfg.TrustedProxies[0])
	}
}

func TestLoadTrimsTrailingSlashes(t *testing.T) {
	clearEnv(t)
	t.Setenv("MEDIAMTX_WHEP_PROXY_TARGET", "http://127.0.0.1:8889/")
	t.Setenv("MEDIAMTX_RTMP_PUBLIC_BASE", "rtmp://live.example.com/live/")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if strings.HasSuffix(cfg.MediaMTX.WHEPProxyTarget, "/") {
		t.Fatalf("代理地址不应带结尾斜杠：%s", cfg.MediaMTX.WHEPProxyTarget)
	}
	if strings.HasSuffix(cfg.MediaMTX.RTMPPublicBase, "/") {
		t.Fatalf("RTMP 地址不应带结尾斜杠：%s", cfg.MediaMTX.RTMPPublicBase)
	}
}
