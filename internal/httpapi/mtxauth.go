package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"tinycast/internal/hub"
	"tinycast/internal/mtx"
)

// mtxAuthPayload 是 MediaMTX authMethod=http 时 POST 过来的鉴权请求体。
//
// 字段含义见 MediaMTX 文档 authHTTPAddress：action 是 publish/read/api/playback，
// path 是完整路径名（例如 live/alex），query 是推流 URL 里 ? 之后的原始串。
type mtxAuthPayload struct {
	IP       string `json:"ip"`
	User     string `json:"user"`
	Password string `json:"password"`
	Token    string `json:"token"`
	Action   string `json:"action"`
	Path     string `json:"path"`
	Protocol string `json:"protocol"`
	ID       string `json:"id"`
	Query    string `json:"query"`
}

// 推流鉴权请求体很小，超过这个长度直接拒绝。
const maxMTXAuthBody = 64 << 10

// handleMTXAuth 处理 MediaMTX 的鉴权回调。
//
// 这是整个系统唯一的安全边界：
//   - 只有内网来源（默认 127/8、10/8、172.16/12、192.168/16 等）能调用；
//   - 配置了 MEDIAMTX_AUTH_SECRET 时还要求共享密钥一致；
//   - 推流的身份校验与占用（规则 1/2/3）全部在这里完成，前端无法绕过。
//
// 观看（read）一律放行：直播本身就是公开的，房间是否可看由前端按状态决定。
// MediaMTX 自己读 /v3 接口时也会发 action=api，同样必须放行，否则后端监控会失明。
func (s *Server) handleMTXAuth(w http.ResponseWriter, r *http.Request) {
	if !s.authSecretOK(r) {
		s.log.Warn("拒绝来源不明的鉴权回调：共享密钥不匹配", "remote", clientIP(r), "path", r.URL.Path)
		writeError(w, http.StatusUnauthorized, "invalid_secret", "鉴权回调密钥不正确")
		return
	}
	if !s.authIPAllowed(clientIP(r)) {
		s.log.Warn("拒绝公网来源的鉴权回调", "remote", clientIP(r))
		writeError(w, http.StatusForbidden, "ip_forbidden", "鉴权回调只允许内网来源")
		return
	}

	var payload mtxAuthPayload
	if r.Body != nil {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxMTXAuthBody)).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_body", "无法解析鉴权请求体")
			return
		}
	}

	switch payload.Action {
	case "publish":
		s.authorizePublish(w, payload)
	case "read", "api", "playback", "metrics", "":
		// 观看与内部接口不做身份校验。
		allowAuth(w)
	default:
		allowAuth(w)
	}
}

// authorizePublish 校验推流身份并把用户名映射到房间。
func (s *Server) authorizePublish(w http.ResponseWriter, payload mtxAuthPayload) {
	app, username := mtx.SplitStreamName(payload.Path)
	if app != s.cfg.MediaMTX.App {
		s.log.Warn("推流路径不在配置的 app 下，已拒绝", "path", payload.Path, "app", s.cfg.MediaMTX.App)
		writeError(w, http.StatusForbidden, "invalid_path", "推流地址必须是 "+s.cfg.MediaMTX.App+"/<用户名>")
		return
	}

	token := payload.Token
	if token == "" {
		// 兼容把令牌填在 RTMP 认证信息里的客户端（rtmp://user:token@host/...）。
		token = payload.Password
	}
	if token == "" {
		token = mtx.QueryValue(payload.Query, "token")
	}

	result := s.hub.Bind(hub.BindInput{
		Username:  username,
		RoomName:  mtx.QueryValue(payload.Query, "room"),
		Token:     token,
		SessionID: payload.ID,
		IP:        payload.IP,
	})
	if !result.Allowed {
		s.log.Warn("推流鉴权失败", "username", username, "reason", string(result.Reason), "ip", payload.IP)
		writeError(w, http.StatusUnauthorized, string(result.Reason), result.Message)
		return
	}

	s.log.Info("推流鉴权通过", "username", username, "room", result.Room.Room, "session", payload.ID, "ip", payload.IP)
	allowAuth(w)
}

// authSecretOK 校验回调地址上的共享密钥（?secret=xxx 或 X-Tinycast-Secret 头）。
//
// 未配置 MEDIAMTX_AUTH_SECRET 时跳过该校验，此时安全性完全依赖来源网段白名单。
func (s *Server) authSecretOK(r *http.Request) bool {
	want := s.cfg.MediaMTX.AuthSecret
	if want == "" {
		return true
	}
	got := r.URL.Query().Get("secret")
	if got == "" {
		got = r.Header.Get("X-Tinycast-Secret")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// authIPAllowed 判断来源 IP 是否在白名单网段内。
func (s *Server) authIPAllowed(remote string) bool {
	ip := net.ParseIP(strings.TrimSpace(remote))
	if ip == nil {
		return false
	}
	for _, network := range s.cfg.MediaMTX.AuthAllowed {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func allowAuth(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
