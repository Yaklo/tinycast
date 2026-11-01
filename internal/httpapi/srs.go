package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"tinycast/internal/hub"
)

// SRS 支持的四个业务回调。
const (
	actionPublish   = "on_publish"
	actionUnpublish = "on_unpublish"
	actionPlay      = "on_play"
	actionStop      = "on_stop"

	// maxCallbackBody 限制回调请求体大小，防止被塞入超大 JSON。
	maxCallbackBody = 1 << 20
)

// srsPayload 是 SRS HTTP 回调的请求体。
type srsPayload struct {
	Action    string `json:"action"`
	ClientID  string `json:"client_id"`
	IP        string `json:"ip"`
	Vhost     string `json:"vhost"`
	App       string `json:"app"`
	Stream    string `json:"stream"`
	Param     string `json:"param"`
	ServerID  string `json:"server_id"`
	StreamURL string `json:"stream_url"`
	StreamID  string `json:"stream_id"`
	TCURL     string `json:"tcUrl"`
	PageURL   string `json:"pageUrl"`
}

// handleSRSCallback 处理 /internal/srs/{action}。
//
// 该接口只允许 SRS 调用：来源 IP 必须在白名单内，且必须带上共享密钥。
func (s *Server) handleSRSCallback(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	switch action {
	case actionPublish, actionUnpublish, actionPlay, actionStop:
	default:
		writeError(w, http.StatusNotFound, "unknown_action", "未知的 SRS 回调类型: "+action)
		return
	}

	if err := s.authorizeCallback(r); err != nil {
		s.log.Warn("SRS 回调被拒绝", "action", action, "remote", clientIP(r), "err", err)
		callbackReject(w, "unauthorized")
		return
	}

	var payload srsPayload
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCallbackBody))
	if err := dec.Decode(&payload); err != nil {
		// 推流鉴权不能"解析失败就放行"，其余回调则不影响播放。
		s.log.Warn("SRS 回调请求体解析失败", "action", action, "remote", clientIP(r), "err", err)
		if action == actionPublish {
			callbackReject(w, "bad_payload")
			return
		}
		callbackOK(w)
		return
	}

	username, params := parseStreamParam(payload.Stream, payload.Param)
	if payload.App != "" && s.cfg.SRS.App != "" && payload.App != s.cfg.SRS.App {
		// 不是本应用的事件（例如 SRS 同时承载了别的 app），直接放行不处理。
		callbackOK(w)
		return
	}

	sourceIP := payload.IP
	if sourceIP == "" {
		sourceIP = clientIP(r)
	}

	switch action {
	case actionPublish:
		res := s.hub.Publish(hub.PublishInput{
			Username: username,
			RoomName: params.Get("room"),
			Token:    params.Get("token"),
			ClientID: payload.ClientID,
			IP:       sourceIP,
		})
		if !res.Allowed {
			s.log.Warn("推流鉴权失败",
				"username", username,
				"client", payload.ClientID,
				"ip", sourceIP,
				"reason", string(res.Reason),
				"detail", res.Message,
			)
			callbackReject(w, string(res.Reason))
			return
		}
		s.log.Info("推流开始", "username", username, "room", res.Room.Room, "client", payload.ClientID, "ip", sourceIP)
		callbackOK(w)

	case actionUnpublish:
		if s.hub.Unpublish(username, payload.ClientID) {
			s.log.Info("推流断开，进入保留期", "username", username, "client", payload.ClientID)
		}
		callbackOK(w)

	case actionPlay:
		if s.hub.Play(username, payload.ClientID) {
			s.log.Debug("观众进入", "username", username, "client", payload.ClientID)
		}
		callbackOK(w)

	case actionStop:
		if s.hub.Stop(payload.ClientID) {
			s.log.Debug("观众离开", "username", username, "client", payload.ClientID)
		}
		callbackOK(w)
	}
}

// authorizeCallback 校验回调来源与共享密钥。
func (s *Server) authorizeCallback(r *http.Request) error {
	if !s.cfg.SRS.CallbackAllowAll {
		ip := net.ParseIP(clientIP(r))
		if ip == nil {
			return fmt.Errorf("无法解析来源地址 %q", clientIP(r))
		}
		if !s.ipAllowed(ip) {
			return fmt.Errorf("来源地址 %s 不在白名单内", ip)
		}
	}

	secret := s.cfg.SRS.CallbackSecret
	if secret == "" {
		return nil
	}
	got := r.URL.Query().Get("secret")
	if got == "" {
		got = r.Header.Get("X-Tinycast-Secret")
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
		return errors.New("回调密钥不匹配")
	}
	return nil
}

func (s *Server) ipAllowed(ip net.IP) bool {
	for _, network := range s.cfg.SRS.CallbackAllowed {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// parseStreamParam 从 SRS 回调里解出流名与推流参数。
//
// SRS 正常会把流名与参数分开（stream=alice, param=?room=x&token=y），
// 但不同版本/编排方式下也可能把查询串留在 stream 里，这里两种都兼容。
func parseStreamParam(stream, param string) (string, url.Values) {
	name := stream
	if i := strings.IndexByte(name, '?'); i >= 0 {
		if param == "" {
			param = name[i:]
		}
		name = name[:i]
	}
	name = strings.TrimSpace(name)

	values := url.Values{}
	raw := strings.TrimPrefix(strings.TrimSpace(param), "?")
	if raw != "" {
		if parsed, err := url.ParseQuery(raw); err == nil {
			values = parsed
		}
	}
	return name, values
}

// callbackOK 告诉 SRS 放行；SRS 要求 HTTP 200 且响应体为 0。
func callbackOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("0"))
}

// callbackReject 拒绝本次连接；非 0 的响应体（或非 200 状态码）都会被 SRS 视为拒绝。
func callbackReject(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Tinycast-Reason", reason)
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte("1"))
}
