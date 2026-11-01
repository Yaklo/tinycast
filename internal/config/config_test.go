package config

import (
	"testing"
	"time"
)

// setenv 用一个 map 覆盖环境变量，测试结束后自动还原。
func setenv(t *testing.T, env map[string]string) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
}

// 默认配置应满足需求：5 分钟保留期、live 应用、内网白名单。
func TestLoadDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("默认配置应可用，实际报错: %v", err)
	}
	if cfg.HTTPAddr != DefaultHTTPAddr {
		t.Fatalf("默认监听地址应为 %s，实际 %s", DefaultHTTPAddr, cfg.HTTPAddr)
	}
	if cfg.RoomGrace != 5*time.Minute {
		t.Fatalf("默认保留期应为 5 分钟，实际 %s", cfg.RoomGrace)
	}
	if cfg.SRS.App != "live" {
		t.Fatalf("默认应用名应为 live，实际 %s", cfg.SRS.App)
	}
	if len(cfg.SRS.CallbackAllowed) == 0 {
		t.Fatal("默认应带内网回调白名单")
	}
}

// 保留期既支持 Go 时长写法，也支持纯秒数。
func TestLoadRoomGraceFormats(t *testing.T) {
	cases := map[string]time.Duration{
		"90s":   90 * time.Second,
		"300":   300 * time.Second,
		"2m30s": 150 * time.Second,
	}
	for raw, want := range cases {
		t.Run(raw, func(t *testing.T) {
			setenv(t, map[string]string{"ROOM_GRACE": raw})
			cfg, err := Load()
			if err != nil {
				t.Fatalf("加载配置失败: %v", err)
			}
			if cfg.RoomGrace != want {
				t.Fatalf("ROOM_GRACE=%s 应解析为 %s，实际 %s", raw, want, cfg.RoomGrace)
			}
		})
	}
}

// 非正的保留期必须被拒绝，否则房间永远不会被释放。
func TestLoadRejectsInvalidGrace(t *testing.T) {
	setenv(t, map[string]string{"ROOM_GRACE": "-1s"})
	if _, err := Load(); err == nil {
		t.Fatal("负的保留期应导致配置错误")
	}
}

// 非法 CIDR 必须被拒绝。
func TestLoadRejectsInvalidCIDR(t *testing.T) {
	setenv(t, map[string]string{"SRS_CALLBACK_ALLOWED_CIDRS": "10.0.0.0/8,not-a-cidr"})
	if _, err := Load(); err == nil {
		t.Fatal("非法网段应导致配置错误")
	}
}

// 自定义网段会替换默认白名单。
func TestLoadCustomCIDRs(t *testing.T) {
	setenv(t, map[string]string{"SRS_CALLBACK_ALLOWED_CIDRS": "172.20.0.0/16,::1/128"})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if len(cfg.SRS.CallbackAllowed) != 2 {
		t.Fatalf("白名单应包含 2 个网段，实际 %d", len(cfg.SRS.CallbackAllowed))
	}
}

// 绝对地址类配置必须同时带协议与主机。
func TestLoadValidatesURLs(t *testing.T) {
	setenv(t, map[string]string{"SRS_RTC_PROXY_TARGET": "srs:1985"})
	if _, err := Load(); err == nil {
		t.Fatal("缺少协议的代理地址应导致配置错误")
	}

	t.Setenv("SRS_RTC_PROXY_TARGET", "http://srs:1985")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("合法代理地址应被接受: %v", err)
	}
	if cfg.SRS.RTCProxyTarget != "http://srs:1985" {
		t.Fatalf("代理地址解析错误: %s", cfg.SRS.RTCProxyTarget)
	}
}

// 地址尾部的斜杠会被去掉，避免拼出双斜杠。
func TestLoadTrimsTrailingSlash(t *testing.T) {
	setenv(t, map[string]string{
		"PUBLIC_BASE_URL":        "https://live.example.com/",
		"SRS_RTMP_PUBLIC_BASE":   "rtmp://live.example.com/live/",
		"SRS_WEBRTC_PUBLIC_BASE": "webrtc://live.example.com/live/",
		"SRS_CLIENTS_API_URL":    "http://127.0.0.1:1985/api/v1/clients/",
		"SRS_RTC_PROXY_TARGET":   "http://127.0.0.1:1985/",
		"SRS_RTC_PUBLIC_BASE":    "https://live.example.com/",
		"SRS_CALLBACK_SECRET":    " secret-value ",
		"CORS_ALLOWED_ORIGINS":   " https://a.example.com , https://b.example.com ",
		"SRS_CALLBACK_ALLOW_ALL": "false",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	for name, got := range map[string]string{
		"PUBLIC_BASE_URL":        cfg.PublicBaseURL,
		"SRS_RTMP_PUBLIC_BASE":   cfg.SRS.RTMPPublicBase,
		"SRS_WEBRTC_PUBLIC_BASE": cfg.SRS.WebRTCPublicBase,
		"SRS_CLIENTS_API_URL":    cfg.SRS.ClientsAPIURL,
		"SRS_RTC_PROXY_TARGET":   cfg.SRS.RTCProxyTarget,
		"SRS_RTC_PUBLIC_BASE":    cfg.SRS.RTCPublicBase,
	} {
		if got == "" || got[len(got)-1] == '/' {
			t.Fatalf("%s 不应以斜杠结尾，实际 %q", name, got)
		}
	}
	if cfg.SRS.CallbackSecret != "secret-value" {
		t.Fatalf("密钥应被去除首尾空白，实际 %q", cfg.SRS.CallbackSecret)
	}
	if len(cfg.CORSOrigins) != 2 || cfg.CORSOrigins[0] != "https://a.example.com" {
		t.Fatalf("CORS 列表解析错误: %v", cfg.CORSOrigins)
	}
}
