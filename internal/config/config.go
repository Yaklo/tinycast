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
	DefaultHTTPAddr        = ":18855"
	DefaultRoomGrace       = 5 * time.Minute
	DefaultMonitorInterval = time.Second
	DefaultViewerTimeout   = 30 * time.Second
	DefaultPendingTimeout  = 60 * time.Second
	DefaultMediaMTXApp     = "live"

	DefaultMediaMTXAPIURL     = "http://127.0.0.1:9997"
	DefaultMediaMTXWHEPTarget = "http://127.0.0.1:8889"
	DefaultMediaMTXHLSTarget  = "http://127.0.0.1:8888"
	DefaultMediaMTXRTMPPublic = "rtmp://localhost:1935/" + DefaultMediaMTXApp
)

// Config 是后端全部运行时配置。
type Config struct {
	// HTTPAddr 是 HTTP 服务监听地址，例如 ":18855"。
	HTTPAddr string
	// InternalAddr 是仅供 MediaMTX 回调的内部监听地址，例如 "127.0.0.1:18856"。
	// 为空表示回调与 HTTPAddr 共用端口（兼容旧配置）；设置后公网端口不再注册
	// /internal/ 路由，反向代理即使没有注入 X-Forwarded-For 也无法伪造回调。
	InternalAddr string
	// RoomGrace 是推流断开后房间保留时长（需求规定 5 分钟）。
	RoomGrace time.Duration
	// MonitorInterval 是房间状态巡检间隔（向 MediaMTX 拉取一次推流清单）。
	MonitorInterval time.Duration
	// ViewerTimeout 是观看会话的存活时间：播放器每隔一段心跳续期，
	// 超过该时长没有心跳就被视为异常断开并从人数中移除。
	ViewerTimeout time.Duration
	// PendingTimeout 是"鉴权通过但推流始终没成功"的绑定最长保留时间。
	PendingTimeout time.Duration
	// CORSOrigins 是允许跨域访问 /api 的来源；为空表示不发送 CORS 头
	// （生产环境前端与后端同源，无需跨域）。
	CORSOrigins []string
	// PublicBaseURL 用于拼装返回给浏览器的绝对地址，例如 https://live.example.com。
	// 为空时返回相对地址，由浏览器按当前 origin 解析。
	PublicBaseURL string
	// TrustedProxies 是允许提供 X-Forwarded-For 的反向代理网段。
	// 只有直连来源落在这些网段里时，才采信转发头里的客户端地址；
	// 否则任何人都能伪造 XFF 绕过鉴权回调与连接数限制。
	TrustedProxies []*net.IPNet
	// MediaMTX 保存与流媒体服务器相关的配置。
	MediaMTX MediaMTXConfig
}

// MediaMTXConfig 描述 MediaMTX 一侧的对接参数。
type MediaMTXConfig struct {
	// App 是 RTMP/WebRTC 的路径前缀（推流地址中的 /live 段），也是鉴权时的路径前缀。
	App string
	// APIURL 是 MediaMTX 控制接口地址，后端通过它读取推流与观看会话清单。
	APIURL string
	// APIUser / APIPassword 是控制接口的 BasicAuth（MediaMTX 开了 apiAuthentication 时需要）。
	APIUser     string
	APIPassword string
	// AuthSecret 是鉴权回调地址上的共享密钥，MediaMTX 配置里以 ?secret=xxx 传递。
	// 为空表示不校验；此时会拒绝任何带转发头的请求，避免反向代理把公网请求
	// 伪装成本机回调。生产环境仍建议配置密钥。
	AuthSecret string
	// AuthAllowed 是允许发起鉴权回调的来源网段白名单。
	AuthAllowed []*net.IPNet
	// WHEPProxyTarget 是 WebRTC 播放信令（WHEP）的上游地址，后端把 /rtc/* 转发给它。
	WHEPProxyTarget string
	// HLSProxyTarget 是 HLS 播放的上游地址，后端把 /hls/* 转发给它。
	HLSProxyTarget string
	// RTMPPublicBase 是展示给用户的 OBS 服务器地址，例如 rtmp://live.example.com/live。
	RTMPPublicBase string
}

// Load 读取环境变量并做基本校验。
func Load() (*Config, error) {
	cfg := &Config{
		HTTPAddr:        envString("HTTP_ADDR", DefaultHTTPAddr),
		InternalAddr:    envString("INTERNAL_ADDR", ""),
		RoomGrace:       envDuration("ROOM_GRACE", DefaultRoomGrace),
		MonitorInterval: envDuration("MONITOR_INTERVAL", DefaultMonitorInterval),
		ViewerTimeout:   envDuration("VIEWER_TIMEOUT", DefaultViewerTimeout),
		PendingTimeout:  envDuration("PENDING_TIMEOUT", DefaultPendingTimeout),
		CORSOrigins:     envList("CORS_ALLOWED_ORIGINS"),
		PublicBaseURL:   strings.TrimRight(envString("PUBLIC_BASE_URL", ""), "/"),
		MediaMTX: MediaMTXConfig{
			App:             envString("MEDIAMTX_APP", DefaultMediaMTXApp),
			APIURL:          strings.TrimRight(envString("MEDIAMTX_API_URL", DefaultMediaMTXAPIURL), "/"),
			APIUser:         envString("MEDIAMTX_API_USER", ""),
			APIPassword:     envString("MEDIAMTX_API_PASSWORD", ""),
			AuthSecret:      envString("MEDIAMTX_AUTH_SECRET", ""),
			WHEPProxyTarget: strings.TrimRight(envString("MEDIAMTX_WHEP_PROXY_TARGET", DefaultMediaMTXWHEPTarget), "/"),
			HLSProxyTarget:  strings.TrimRight(envString("MEDIAMTX_HLS_PROXY_TARGET", DefaultMediaMTXHLSTarget), "/"),
			RTMPPublicBase:  strings.TrimRight(envString("MEDIAMTX_RTMP_PUBLIC_BASE", DefaultMediaMTXRTMPPublic), "/"),
		},
	}

	if strings.TrimSpace(cfg.HTTPAddr) == "" {
		return nil, fmt.Errorf("config: HTTP_ADDR 不能为空")
	}
	// 配置成同一个地址时没有独立监听的意义，按“未启用”处理，避免回调路由被误删。
	if cfg.InternalAddr == cfg.HTTPAddr {
		cfg.InternalAddr = ""
	}
	if cfg.RoomGrace <= 0 {
		return nil, fmt.Errorf("config: ROOM_GRACE 必须为正数，当前为 %s", cfg.RoomGrace)
	}
	if cfg.MonitorInterval <= 0 {
		return nil, fmt.Errorf("config: MONITOR_INTERVAL 必须为正数，当前为 %s", cfg.MonitorInterval)
	}
	if cfg.ViewerTimeout <= 0 {
		return nil, fmt.Errorf("config: VIEWER_TIMEOUT 必须为正数，当前为 %s", cfg.ViewerTimeout)
	}
	if cfg.PendingTimeout <= 0 {
		return nil, fmt.Errorf("config: PENDING_TIMEOUT 必须为正数，当前为 %s", cfg.PendingTimeout)
	}
	if cfg.MediaMTX.App == "" {
		return nil, fmt.Errorf("config: MEDIAMTX_APP 不能为空")
	}
	if strings.ContainsAny(cfg.MediaMTX.App, "/?#& ") {
		return nil, fmt.Errorf("config: MEDIAMTX_APP 不能包含空格或 /?#&，当前为 %q", cfg.MediaMTX.App)
	}

	for _, item := range []struct{ key, value string }{
		{"MEDIAMTX_API_URL", cfg.MediaMTX.APIURL},
		{"MEDIAMTX_WHEP_PROXY_TARGET", cfg.MediaMTX.WHEPProxyTarget},
		{"MEDIAMTX_HLS_PROXY_TARGET", cfg.MediaMTX.HLSProxyTarget},
		{"PUBLIC_BASE_URL", cfg.PublicBaseURL},
	} {
		if item.value == "" {
			continue
		}
		u, err := url.Parse(item.value)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("config: %s 必须是带协议与主机的绝对地址，当前为 %q", item.key, item.value)
		}
	}

	// 默认只信任本机：MediaMTX 与 tinycast 通常在同一台机器上。
	// Docker 等跨容器部署必须显式配置 MEDIAMTX_AUTH_ALLOWED_CIDRS（compose 已配）。
	allowed, err := parseCIDRs("MEDIAMTX_AUTH_ALLOWED_CIDRS", []string{"127.0.0.0/8", "::1/128"})
	if err != nil {
		return nil, err
	}
	cfg.MediaMTX.AuthAllowed = allowed

	// 默认只信任本机反代。部署在 nginx/caddy 后面时，这些代理会把公网请求
	// 的 RemoteAddr 变成本机地址，必须靠 X-Forwarded-For 还原真实来源。
	trusted, err := parseCIDRs("TRUSTED_PROXIES", []string{"127.0.0.0/8", "::1/128"})
	if err != nil {
		return nil, err
	}
	cfg.TrustedProxies = trusted

	return cfg, nil
}

func parseCIDRs(key string, def []string) ([]*net.IPNet, error) {
	raw := envList(key)
	if len(raw) == 0 {
		raw = def
	}
	out := make([]*net.IPNet, 0, len(raw))
	for _, item := range raw {
		_, network, err := net.ParseCIDR(item)
		if err != nil {
			return nil, fmt.Errorf("config: %s 含非法网段 %q: %w", key, item, err)
		}
		out = append(out, network)
	}
	return out, nil
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
