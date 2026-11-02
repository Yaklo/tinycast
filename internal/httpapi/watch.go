package httpapi

import (
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tinycast/internal/hub"
)

// watchRequest 是观看心跳的请求体。
//
// viewerID 由浏览器生成并保存在 sessionStorage 里：同一标签页刷新不会重复计数，
// 而关闭标签页后心跳自然停止，超时后会被后端清理。
type watchRequest struct {
	ViewerID string `json:"viewerId"`
}

// maxViewerIDLen 限制观看会话 ID 长度，避免被塞入超长字符串。
const maxViewerIDLen = 64

// handleWatch 处理一次观看心跳（同时承担"进入房间"的作用）。
//
// 为什么不用 MediaMTX 的 readers 统计人数：HLS 的播放会话是按请求创建的短连接，
// 同一个人反复拉去播放列表会被算成很多次；而 WebRTC 会话又看不到 HLS 观众。
// 统一由前端心跳上报，两种播放模式的人数口径才一致。
func (s *Server) handleWatch(w http.ResponseWriter, r *http.Request) {
	if !sameOriginRequest(r) {
		writeError(w, http.StatusForbidden, "cross_origin", "观看心跳只接受同源请求")
		return
	}
	// 走请求体上报时必须声明 application/json：跨站表单做不到这一点，
	// 与 sameOriginRequest 一起把伪造人数的成本抬高。
	if r.Method == http.MethodPost && r.URL.Query().Get("viewerId") == "" && r.ContentLength != 0 {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "请求体需要使用 application/json")
			return
		}
	}
	username := strings.TrimSpace(r.PathValue("username"))
	viewerID, err := decodeViewerID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_viewer", err.Error())
		return
	}
	// 先校验参数再扣额度：否则 24 rps 的无效请求就能把同一个 NAT 出口
	// 后面所有观众的心跳额度耗尽 10 秒。
	if !s.watchLimiter.allow(watchLimitKey(username, s.clientIP(r))) {
		w.Header().Set("Retry-After", "10")
		writeError(w, http.StatusTooManyRequests, "too_many_requests", "观看心跳过于频繁，请稍后再试")
		return
	}

	view, ok := s.hub.Room(username)
	if !ok {
		writeError(w, http.StatusNotFound, "room_not_found", "房间不存在或已释放")
		return
	}
	added, ok := s.hub.WatchChanged(view.Username, viewerID)
	if !ok {
		if view.ViewerCount >= hub.MaxViewersPerRoom {
			writeError(w, http.StatusTooManyRequests, "room_full", "该房间观看人数已达上限")
			return
		}
		writeError(w, http.StatusNotFound, "room_not_found", "房间不存在或已释放")
		return
	}
	// 只有新增观众才需要广播；每 10 秒一次的心跳刷新不产生全量快照。
	if added {
		s.scheduleBroadcast()
	}

	updated, ok := s.hub.Room(view.Username)
	if !ok {
		writeError(w, http.StatusNotFound, "room_not_found", "房间不存在或已释放")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"room":       updated,
		"serverTime": time.Now().UnixMilli(),
	})
}

// handleUnwatch 在播放器主动离开时立即减一，不必等心跳超时。
func (s *Server) handleUnwatch(w http.ResponseWriter, r *http.Request) {
	if !sameOriginRequest(r) {
		writeError(w, http.StatusForbidden, "cross_origin", "观看心跳只接受同源请求")
		return
	}
	username := strings.TrimSpace(r.PathValue("username"))
	viewerID, err := decodeViewerID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_viewer", err.Error())
		return
	}
	if !s.unwatchLimiter.allow(watchLimitKey(username, s.clientIP(r))) {
		w.Header().Set("Retry-After", "10")
		writeError(w, http.StatusTooManyRequests, "too_many_requests", "请求过于频繁，请稍后再试")
		return
	}

	if s.hub.Unwatch(username, viewerID) {
		s.scheduleBroadcast()
	}
	w.WriteHeader(http.StatusNoContent)
}

// sameOriginRequest 阻止其它站点用表单或 no-cors fetch 伪造观看人数。
//
// 浏览器的跨站请求一定带 Sec-Fetch-Site: cross-site；老浏览器没有这个头时，
// 退而检查 Origin 是否与本站一致。curl 之类不带任何头的客户端仍然可用。
func sameOriginRequest(r *http.Request) bool {
	// Sec-Fetch-Site 由浏览器写入，页面脚本改不了：同源请求直接放行，
	// 不必再比对 Origin——反向代理可能改写 Host，严格比对会误伤正常观众。
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
		// 老浏览器没有 Sec-Fetch-Site，退回检查 Origin。
	default:
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
	}
	return true
}

// watchLimitKey 按「房间 + 客户端」计数：一个人刷 A 房间不应影响别人看 B 房间。
// IPv6 地址收敛到 /64，否则同一台机器整段地址轮流用等于没有限额。
func watchLimitKey(username, ip string) string {
	return username + "|" + eventStreamKey(ip)
}

func decodeViewerID(r *http.Request) (string, error) {
	viewerID := strings.TrimSpace(r.URL.Query().Get("viewerId"))
	if viewerID == "" && r.Body != nil && r.Method == http.MethodPost {
		var body watchRequest
		// 空 body 是允许的（前端可以只靠查询参数上报），此时按格式错误处理。
		if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4096)).Decode(&body); err != nil {
			return "", errors.New("请求体需要是 {\"viewerId\":\"...\"}")
		}
		viewerID = strings.TrimSpace(body.ViewerID)
	}
	if viewerID == "" {
		return "", errors.New("缺少 viewerId")
	}
	if len(viewerID) > maxViewerIDLen {
		return "", errors.New("viewerId 过长")
	}
	for _, ch := range viewerID {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		case ch == '-', ch == '_', ch == '.':
		default:
			return "", errors.New("viewerId 只能包含字母、数字、- _ .")
		}
	}
	return viewerID, nil
}
