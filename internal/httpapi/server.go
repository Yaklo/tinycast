// Package httpapi 提供 tinycast 的 HTTP 接口：
// 房间查询、播放信息、观看心跳、SSE 实时事件，以及只允许 MediaMTX 调用的鉴权回调。
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"tinycast/internal/config"
	"tinycast/internal/hub"
	"tinycast/internal/webui"
)

// Server 组装全部路由。
type Server struct {
	hub *hub.Hub
	cfg *config.Config
	log *slog.Logger
	// whep 把浏览器 /rtc/* 的 WHEP 信令转发给 MediaMTX。
	whep *httputil.ReverseProxy
	// hls 把浏览器 /hls/* 的播放列表与分片转发给 MediaMTX。
	hls *httputil.ReverseProxy
	mux http.Handler

	// SSE 连接上限：单机轻量部署也要防止一个客户端把文件描述符与广播开销吃光。
	eventsMu    sync.Mutex
	eventsByIP  map[string]int
	eventsTotal int

	// mediaSlots 限制同时进行中的 /hls/、/rtc/ 反代请求数。
	// 慢客户端可以把一个流式响应挂很久，不设上限会吃光文件描述符。
	mediaSlots chan struct{}

	// 观看人数变化可能每秒来很多次；把广播合并成最多每 100ms 一次，
	// 避免一个廉价请求放大成对每个 SSE 订阅者的全量快照。
	broadcastMu    sync.Mutex
	broadcastTimer *time.Timer
}

// New 创建 HTTP 服务。
func New(h *hub.Hub, cfg *config.Config, log *slog.Logger) *Server {
	s := &Server{
		hub:        h,
		cfg:        cfg,
		log:        log,
		eventsByIP: make(map[string]int),
		mediaSlots: make(chan struct{}, 512),
	}

	if cfg.MediaMTX.WHEPProxyTarget != "" {
		if proxy, err := newMediaProxy(cfg.MediaMTX.WHEPProxyTarget, "/rtc", true); err != nil {
			log.Error("MEDIAMTX_WHEP_PROXY_TARGET 解析失败，已关闭 WebRTC 播放", "target", cfg.MediaMTX.WHEPProxyTarget, "err", err)
		} else {
			proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
				log.Warn("WebRTC 信令代理失败", "path", r.URL.Path, "err", err)
				writeError(w, http.StatusBadGateway, "mediamtx_unreachable", "无法连接流媒体服务器")
			}
			s.whep = proxy
		}
	}

	if cfg.MediaMTX.HLSProxyTarget != "" {
		// HLS 入口会 302 到 ?cookieCheck=1 的同名地址，必须把 Location 补上 /hls 前缀，
		// 否则浏览器会跳到后端并不存在的路径（落到 SPA 上，播放列表就拿不到了）。
		if proxy, err := newMediaProxy(cfg.MediaMTX.HLSProxyTarget, "/hls", true); err != nil {
			log.Error("MEDIAMTX_HLS_PROXY_TARGET 解析失败，已关闭 HLS 播放", "target", cfg.MediaMTX.HLSProxyTarget, "err", err)
		} else {
			proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
				log.Warn("HLS 代理失败", "path", r.URL.Path, "err", err)
				writeError(w, http.StatusBadGateway, "mediamtx_unreachable", "无法连接流媒体服务器")
			}
			s.hls = proxy
		}
	}

	s.mux = s.routes()
	return s
}

// Handler 返回带中间件的根处理器。
func (s *Server) Handler() http.Handler { return s.mux }

// scheduleBroadcast 合并短时间内的多次状态变化。
func (s *Server) scheduleBroadcast() {
	s.broadcastMu.Lock()
	defer s.broadcastMu.Unlock()
	if s.broadcastTimer != nil {
		return
	}
	s.broadcastTimer = time.AfterFunc(100*time.Millisecond, func() {
		s.broadcastMu.Lock()
		s.broadcastTimer = nil
		s.broadcastMu.Unlock()
		s.hub.Broadcast()
	})
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/healthz", s.handleHealth)
	mux.HandleFunc("GET /api/version", s.handleVersion)
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/rooms", s.handleRooms)
	mux.HandleFunc("GET /api/rooms/{username}", s.handleRoom)
	mux.HandleFunc("GET /api/rooms/{username}/play", s.handlePlay)
	mux.HandleFunc("POST /api/rooms/{username}/watch", s.handleWatch)
	mux.HandleFunc("DELETE /api/rooms/{username}/watch", s.handleUnwatch)
	mux.HandleFunc("GET /api/events", s.handleEvents)

	// MediaMTX 的鉴权回调。默认只接受本机来源，可再加共享密钥，见 mtxauth.go。
	mux.HandleFunc("POST /internal/mtx/auth", s.handleMTXAuth)

	// 未匹配的 /api 与 /internal 一律返回 JSON 404，避免被 SPA 回退误当成页面。
	mux.HandleFunc("/api/", s.handleNotFound)
	mux.HandleFunc("/internal/", s.handleNotFound)

	// 浏览器只与业务后端同源交互，媒体信令与播放列表都由后端转发。
	if s.whep != nil {
		mux.Handle("/rtc/", s.mediaProxyHandler(s.whep, "/rtc"))
	}
	if s.hls != nil {
		mux.Handle("/hls/", s.mediaProxyHandler(s.hls, "/hls"))
	}

	mux.Handle("/", webui.Handler())

	return s.recoverer(s.logger(s.cors(s.securityHeaders(mux))))
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"serverTime":  time.Now().UnixMilli(),
		"rooms":       s.hub.RoomCount(),
		"subscribers": s.hub.SubscriberCount(),
	})
}

// handleConfig 暴露前端需要的公开配置（不包含任何密钥）。
//
// 推流配置的生成完全在浏览器端完成，这个接口只用于取得展示用的地址与保留时长。
func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"app":          s.cfg.MediaMTX.App,
		"rtmpServer":   s.cfg.MediaMTX.RTMPPublicBase,
		"graceSeconds": int(s.cfg.RoomGrace / time.Second),
		"hls":          s.hls != nil,
		"webrtc":       s.whep != nil,
	})
}

func (s *Server) handleNotFound(w http.ResponseWriter, _ *http.Request) {
	// 不要回显请求路径：它可能包含攻击者构造的脚本片段，也会把内部路径带进日志与页面。
	writeError(w, http.StatusNotFound, "not_found", "接口不存在")
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	if err := enc.Encode(body); err != nil {
		// 响应已经开始写出，只能记录下来。
		return
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, apiError{Code: code, Message: message})
}

// newMediaProxy 构造一个到 MediaMTX 的反向代理。
//
// prefix 是浏览器侧的路径前缀（/rtc、/hls），会被剥掉后转发；
// rewriteLocation 用于把上游 302 的 Location 重新加上前缀，否则浏览器会跳出代理路径。
func newMediaProxy(target, prefix string, rewriteLocation bool) (*httputil.ReverseProxy, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.FlushInterval = -1
	// 默认 Transport 没有响应头超时，MediaMTX 卡住时反代会永久占住 goroutine。
	proxy.Transport = &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		if !rewriteLocation {
			return nil
		}
		loc := resp.Header.Get("Location")
		if loc == "" || !strings.HasPrefix(loc, "/") || strings.HasPrefix(loc, prefix+"/") {
			return nil
		}
		resp.Header.Set("Location", prefix+loc)
		return nil
	}
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = u.Scheme
		req.URL.Host = u.Host
		// URL.Path 已经被 net/http 解码（%2e%2e 会变成 ..），先 Clean 再拼到上游，
		// 避免 /hls/%2e%2e/%2e%2e/... 把请求带出 /hls 前缀。
		trimmed := strings.TrimPrefix(req.URL.Path, prefix)
		req.URL.Path = singleJoiningSlash(u.Path, path.Clean(trimmed))
		req.URL.RawPath = ""
		req.Host = u.Host
	}
	return proxy, nil
}

// mediaProxyHandler 在进入反代前再做一次路径校验。
//
// 只 Clean 不够：/hls/../../internal/mtx/auth 会被 Clean 成 /internal/mtx/auth，
// 虽然已经离开 /hls 前缀，但仍然会被转发到 MediaMTX。这里直接拒绝含 ".." 段的请求。
func (s *Server) mediaProxyHandler(proxy *httputil.ReverseProxy, prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validMediaPath(r.URL.Path, prefix, s.cfg.MediaMTX.App) {
			writeError(w, http.StatusBadRequest, "invalid_path", "非法的媒体路径")
			return
		}
		select {
		case s.mediaSlots <- struct{}{}:
			defer func() { <-s.mediaSlots }()
		default:
			w.Header().Set("Retry-After", "2")
			writeError(w, http.StatusServiceUnavailable, "too_many_streams", "媒体请求过多，请稍后重试")
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

// validMediaPath 只放行 /<prefix>/<app>/<username>/... 这一种形状。
//
// 不限 app 段时，HLS 代理一旦误配到 MediaMTX 的 API 端口，
// /hls/v3/paths/list 就能直接读到控制接口。
func validMediaPath(p, prefix, app string) bool {
	base := prefix + "/" + strings.Trim(app, "/") + "/"
	if !strings.HasPrefix(p, base) {
		return false
	}
	rest := strings.TrimPrefix(p, prefix)
	for _, segment := range strings.Split(rest, "/") {
		if segment == ".." {
			return false
		}
	}
	return true
}

func singleJoiningSlash(a, b string) string {
	switch {
	case strings.HasSuffix(a, "/") && strings.HasPrefix(b, "/"):
		return a + b[1:]
	case !strings.HasSuffix(a, "/") && !strings.HasPrefix(b, "/"):
		return a + "/" + b
	}
	return a + b
}
