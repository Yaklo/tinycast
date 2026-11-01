// Package httpapi 提供 tinycast 的 HTTP 接口：
// 房间查询、播放信息、观看心跳、SSE 实时事件，以及只允许 MediaMTX 调用的鉴权回调。
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
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
}

// New 创建 HTTP 服务。
func New(h *hub.Hub, cfg *config.Config, log *slog.Logger) *Server {
	s := &Server{hub: h, cfg: cfg, log: log}

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

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/healthz", s.handleHealth)
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/rooms", s.handleRooms)
	mux.HandleFunc("GET /api/rooms/{username}", s.handleRoom)
	mux.HandleFunc("GET /api/rooms/{username}/play", s.handlePlay)
	mux.HandleFunc("POST /api/rooms/{username}/watch", s.handleWatch)
	mux.HandleFunc("DELETE /api/rooms/{username}/watch", s.handleUnwatch)
	mux.HandleFunc("GET /api/events", s.handleEvents)

	// MediaMTX 的鉴权回调。默认只接受内网来源 + 共享密钥，见 mtxauth.go。
	mux.HandleFunc("POST /internal/mtx/auth", s.handleMTXAuth)

	// 未匹配的 /api 与 /internal 一律返回 JSON 404，避免被 SPA 回退误当成页面。
	mux.HandleFunc("/api/", s.handleNotFound)
	mux.HandleFunc("/internal/", s.handleNotFound)

	// 浏览器只与业务后端同源交互，媒体信令与播放列表都由后端转发。
	if s.whep != nil {
		mux.Handle("/rtc/", s.whep)
	}
	if s.hls != nil {
		mux.Handle("/hls/", s.hls)
	}

	mux.Handle("/", webui.Handler())

	return s.recoverer(s.logger(s.cors(s.securityHeaders(mux))))
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"serverTime":  time.Now().UnixMilli(),
		"rooms":       len(s.hub.Snapshot().Rooms),
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

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "接口不存在: "+r.URL.Path)
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
		req.URL.Path = singleJoiningSlash(u.Path, strings.TrimPrefix(req.URL.Path, prefix))
		req.Host = u.Host
	}
	return proxy, nil
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
