// Package config 负责从环境变量加载 tinycast 的全部运行时配置。
//
// 项目刻意不引入配置文件与第三方配置库：所有开关都通过环境变量注入，
// 这样同一个二进制既可以直接在 Windows/Linux 上运行，也可以塞进容器。
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// 默认值集中在这里，方便与 README 的配置表保持一致。
const (
	DefaultHTTPAddr        = ":8080"
	DefaultRoomGrace       = 5 * time.Minute
	DefaultJanitorInterval = 15 * time.Second
	DefaultSRSApp          = "live"
)

// Config 是后端全部运行时配置。
type Config struct {
	// HTTPAddr 是 HTTP 服务监听地址，例如 ":8080"。
	HTTPAddr string
	// RoomGrace 是推流断开后房间保留时长（需求规定 5 分钟）。
	RoomGrace time.Duration
	// JanitorInterval 是后台清理任务的执行间隔。
	JanitorInterval time.Duration
	// CORSOrigins 是允许跨域访问 /api 的来源；为空表示不发送 CORS 头
	// （生产环境前端与后端同源，无需跨域）。
	CORSOrigins []string
	// PublicBaseURL 用于拼装返回给浏览器的绝对地址，例如 https://live.example.com。
	// 为空时返回相对地址，由浏览器按当前 origin 解析。
	PublicBaseURL string
	// SRS 保存与流媒体服务器相关的配置。
	SRS SRSConfig
}

// SRSConfig 描述 SRS 一侧的对接参数。
type SRSConfig struct {
	// App 是 SRS 的应用名（RTMP 地址中的 /live 段），同时也是回调过滤条件。
	App string
	// CallbackSecret 是 SRS 回调地址上的共享密钥，SRS 配置里以 ?secret=xxx 传递。
	// 为空表示不校验（仅建议本地调试使用）。
	CallbackSecret string
	// CallbackAllowed 是允许发起回调的来源网段白名单。
	CallbackAllowed []*net.IPNet
	// CallbackAllowAll 为真时跳过来源 IP 校验（例如上层已经有网关防护）。
	CallbackAllowAll bool
	// RTMPPublicBase 是展示给用户的 OBS 服务器地址，例如 rtmp://live.example.com/live。
	RTMPPublicBase string
	// WebRTCPublicBase 是展示用的 WebRTC 播放地址前缀，例如 webrtc://live.example.com/live。
	WebRTCPublicBase string
	// RTCProxyTarget 非空时，后端会把 /rtc/* 反向代理到 SRS 的 HTTP API，
	// 这样浏览器只需要和业务后端同源交互，不必处理 SRS 的跨域问题。
	RTCProxyTarget string
	// RTCPublicBase 是返回给浏览器的 WHEP 信令地址前缀。为空表示使用相对地址
	// （即浏览器的当前站点 + /rtc/... ），适用于经后端或 Nginx 转发的部署方式。
	RTCPublicBase string
	// ClientsAPIURL 非空时，清理任务会定期向 SRS 查询在线客户端，
	// 用于修复丢失的回调（例如 SRS 重启导致的僵尸观看会话）。
	ClientsAPIURL string
	// ClientsAPIUser / ClientsAPIPassword 是 SRS HTTP API 的 BasicAuth（SRS 开启 http_server 鉴权时需要）。
	ClientsAPIUser     string
	ClientsAPIPassword string
}

// Load 读取环境变量并做基本校验。
func Load() (*Config, error) {
	cfg := &Config{
		HTTPAddr:        envString("HTTP_ADDR", DefaultHTTPAddr),
		RoomGrace:       envDuration("ROOM_GRACE", DefaultRoomGrace),
		JanitorInterval: envDuration("JANITOR_INTERVAL", DefaultJanitorInterval),
		CORSOrigins:     envList("CORS_ALLOWED_ORIGINS"),
		PublicBaseURL:   strings.TrimRight(envString("PUBLIC_BASE_URL", ""), "/"),
		SRS: SRSConfig{
			App:                envString("SRS_APP", DefaultSRSApp),
			CallbackSecret:     envString("SRS_CALLBACK_SECRET", ""),
			CallbackAllowAll:   envBool("SRS_CALLBACK_ALLOW_ALL", false),
			RTMPPublicBase:     strings.TrimRight(envString("SRS_RTMP_PUBLIC_BASE", "rtmp://localhost:1935/"+DefaultSRSApp), "/"),
			WebRTCPublicBase:   strings.TrimRight(envString("SRS_WEBRTC_PUBLIC_BASE", "webrtc://localhost/"+DefaultSRSApp), "/"),
			RTCProxyTarget:     strings.TrimRight(envString("SRS_RTC_PROXY_TARGET", ""), "/"),
			RTCPublicBase:      strings.TrimRight(envString("SRS_RTC_PUBLIC_BASE", ""), "/"),
			ClientsAPIURL:      strings.TrimRight(envString("SRS_CLIENTS_API_URL", ""), "/"),
			ClientsAPIUser:     envString("SRS_CLIENTS_API_USER", ""),
			ClientsAPIPassword: envString("SRS_CLIENTS_API_PASSWORD", ""),
		},
	}

	if strings.TrimSpace(cfg.HTTPAddr) == "" {
		return nil, fmt.Errorf("config: HTTP_ADDR 不能为空")
	}
	if cfg.RoomGrace <= 0 {
		return nil, fmt.Errorf("config: ROOM_GRACE 必须为正数，当前为 %s", cfg.RoomGrace)
	}
	if cfg.JanitorInterval <= 0 {
		return nil, fmt.Errorf("config: JANITOR_INTERVAL 必须为正数，当前为 %s", cfg.JanitorInterval)
	}
	if cfg.SRS.App == "" {
		return nil, fmt.Errorf("config: SRS_APP 不能为空")
	}
	for _, item := range []struct{ key, value string }{
		{"SRS_RTC_PROXY_TARGET", cfg.SRS.RTCProxyTarget},
		{"SRS_CLIENTS_API_URL", cfg.SRS.ClientsAPIURL},
		{"PUBLIC_BASE_URL", cfg.PublicBaseURL},
		{"SRS_RTC_PUBLIC_BASE", cfg.SRS.RTCPublicBase},
	} {
		if item.value == "" {
			continue
		}
		u, err := url.Parse(item.value)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("config: %s 必须是带协议与主机的绝对地址，当前为 %q", item.key, item.value)
		}
	}

	// 默认只信任本机与内网网段：SRS 回调接口绝不应该被公网直接访问。
	cidrs := envList("SRS_CALLBACK_ALLOWED_CIDRS")
	if len(cidrs) == 0 {
		cidrs = []string{"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"}
	}
	for _, raw := range cidrs {
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, fmt.Errorf("config: SRS_CALLBACK_ALLOWED_CIDRS 含非法网段 %q: %w", raw, err)
		}
		cfg.SRS.CallbackAllowed = append(cfg.SRS.CallbackAllowed, network)
	}

	return cfg, nil
}

func envString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return strings.TrimSpace(v)
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil {
		return d
	}
	// 允许直接写秒数，例如 ROOM_GRACE=300。
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		return time.Duration(secs) * time.Second
	}
	return def
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func envList(key string) []string {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
