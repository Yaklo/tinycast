package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"tinycast/internal/hub"
)

type roomsResponse struct {
	Rooms        []hub.RoomView `json:"rooms"`
	ServerTime   int64          `json:"serverTime"`
	GraceSeconds int            `json:"graceSeconds"`
}

func (s *Server) handleRooms(w http.ResponseWriter, _ *http.Request) {
	snap := s.hub.Snapshot()
	writeJSON(w, http.StatusOK, roomsResponse{
		Rooms:        snap.Rooms,
		ServerTime:   snap.ServerTime,
		GraceSeconds: s.graceSeconds(),
	})
}

type roomResponse struct {
	Room         hub.RoomView `json:"room"`
	ServerTime   int64        `json:"serverTime"`
	GraceSeconds int          `json:"graceSeconds"`
}

func (s *Server) handleRoom(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.PathValue("username"))
	view, ok := s.hub.Room(username)
	if !ok {
		writeError(w, http.StatusNotFound, "room_not_found", "房间不存在或已释放")
		return
	}
	writeJSON(w, http.StatusOK, roomResponse{
		Room:         view,
		ServerTime:   time.Now().UnixMilli(),
		GraceSeconds: s.graceSeconds(),
	})
}

// playback 描述浏览器播放一路直播所需的全部信息。
type playback struct {
	Protocol string `json:"protocol"`
	// WhepURL 是 SRS 的 WHEP 信令地址，前端 POST 一段 SDP offer 即可换回 answer。
	WhepURL string `json:"whepUrl"`
	// StreamURL 是 SRS 惯用的 webrtc:// 地址，用于展示与排障。
	StreamURL string `json:"streamUrl"`
}

type playResponse struct {
	Username     string    `json:"username"`
	State        hub.State `json:"state"`
	GraceUntil   int64     `json:"graceUntil,omitempty"`
	Playback     *playback `json:"playback"`
	ServerTime   int64     `json:"serverTime"`
	GraceSeconds int       `json:"graceSeconds"`
}

// handlePlay 返回播放信息；GRACE 状态下不会给出播放地址，避免前端播放"假直播"。
func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.PathValue("username"))
	view, ok := s.hub.Room(username)
	if !ok {
		writeError(w, http.StatusNotFound, "room_not_found", "房间不存在或已释放")
		return
	}

	resp := playResponse{
		Username:     view.Username,
		State:        view.State,
		ServerTime:   time.Now().UnixMilli(),
		GraceSeconds: s.graceSeconds(),
	}
	if view.State == hub.StateLive {
		resp.Playback = &playback{
			Protocol:  "webrtc",
			WhepURL:   s.whepURL(view.Username),
			StreamURL: s.streamURL(view.Username),
		}
	} else {
		resp.GraceUntil = view.GraceUntil
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) graceSeconds() int { return int(s.cfg.RoomGrace / time.Second) }

// whepURL 拼装浏览器使用的 WHEP 信令地址。
//
// 默认返回相对地址（由浏览器按当前 origin 解析），这样部署时只要把 /rtc/ 转发给 SRS
// 就能保持同源；SRS_RTC_PUBLIC_BASE 非空时则指向独立的 SRS 地址。
func (s *Server) whepURL(username string) string {
	q := url.Values{}
	q.Set("app", s.cfg.SRS.App)
	q.Set("stream", username)
	return strings.TrimRight(s.cfg.SRS.RTCPublicBase, "/") + "/rtc/v1/whep/?" + q.Encode()
}

func (s *Server) streamURL(username string) string {
	return strings.TrimRight(s.cfg.SRS.WebRTCPublicBase, "/") + "/" + url.PathEscape(username)
}
