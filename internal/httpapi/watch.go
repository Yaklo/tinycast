package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
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
	username := strings.TrimSpace(r.PathValue("username"))
	viewerID, err := decodeViewerID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_viewer", err.Error())
		return
	}

	view, ok := s.hub.Room(username)
	if !ok {
		writeError(w, http.StatusNotFound, "room_not_found", "房间不存在或已释放")
		return
	}
	s.hub.Watch(view.Username, viewerID)
	// 人数变化需要立刻反映到其它观众与首页，因此刷新一次快照。
	s.hub.Broadcast()

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
	username := strings.TrimSpace(r.PathValue("username"))
	viewerID, err := decodeViewerID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_viewer", err.Error())
		return
	}

	s.hub.Unwatch(username, viewerID)
	s.hub.Broadcast()
	w.WriteHeader(http.StatusNoContent)
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
