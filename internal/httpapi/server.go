// Package httpapi 提供 tinycast 的 HTTP 接口：
// 房间查询、播放信息、SSE 实时事件，以及只允许 SRS 调用的内部回调接口。
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
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
	rtc *httputil.ReverseProxy
	mux http.Handler
}

// New 创建 HTTP 服务。
func New(h *hub.Hub, cfg *config.Config, log *slog.Logger) *Server {
	s := &Server{hub: h, cfg: cfg, log: log}

	if cfg.SRS.RTCProxyTarget != "" {
		target, err := url.Parse(cfg.SRS.RTCProxyTarget)
		if err != nil {
			log.Error("SRS_RTC_PROXY_TARGET 解析失败，已忽略 WebRTC 信令代理", "target", cfg.SRS.RTCProxyTarget, "err", err)
		} else {
			proxy := httputil.NewSingleHostReverseProxy(target)
			proxy.FlushInterval = -1
			proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
				log.Warn("WebRTC 信令代理失败", "path", r.URL.Path, "err", err)
				writeError(w, http.StatusBadGateway, "srs_unreachable", "无法连接流媒体服务器")
			}
			s.rtc = proxy
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
	mux.HandleFunc("GET /api/events", s.handleEvents)

	mux.HandleFunc("POST /internal/srs/{action}", s.handleSRSCallback)

	// 未匹配的 /api 与 /internal 一律返回 JSON 404，避免被 SPA 回退误当成页面。
	mux.HandleFunc("/api/", s.handleNotFound)
	mux.HandleFunc("/internal/", s.handleNotFound)

	if s.rtc != nil {
		// 让浏览器只与业务后端同源交互，WebRTC 信令由后端转发给 SRS。
		mux.Handle("/rtc/", s.rtc)
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
		"app":          s.cfg.SRS.App,
		"rtmpServer":   s.cfg.SRS.RTMPPublicBase,
		"webrtcBase":   s.cfg.SRS.WebRTCPublicBase,
		"graceSeconds": int(s.cfg.RoomGrace / time.Second),
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
