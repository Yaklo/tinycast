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
	// Protocol 是推荐协议：配置了 HLS 代理时为 hls（有声音、兼容 H.265），否则为 webrtc。
	Protocol string `json:"protocol"`
	// HLSURL 是 HLS 播放列表地址（m3u8），前端用 hls.js 或原生播放器播放。
	HLSURL string `json:"hlsUrl,omitempty"`
	// WhepURL 是 WHEP 信令地址，前端 POST 一段 SDP offer 即可换回 answer。
	WhepURL string `json:"whepUrl,omitempty"`
}

type playResponse struct {
	Username     string         `json:"username"`
	State        hub.State      `json:"state"`
	GraceUntil   int64          `json:"graceUntil,omitempty"`
	Playback     *playback      `json:"playback,omitempty"`
	Media        *hub.MediaInfo `json:"media,omitempty"`
	ServerTime   int64          `json:"serverTime"`
	GraceSeconds int            `json:"graceSeconds"`
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
		Media:        view.Media,
		ServerTime:   time.Now().UnixMilli(),
		GraceSeconds: s.graceSeconds(),
	}
	if view.State == hub.StateLive {
		resp.Playback = s.playbackFor(view.Username)
	} else {
		resp.GraceUntil = view.GraceUntil
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) graceSeconds() int { return int(s.cfg.RoomGrace / time.Second) }

// playbackFor 拼装播放地址。
//
// 默认返回相对地址（浏览器按当前 origin 解析）：部署时前端与后端同源，
// 后端再把 /rtc/ 与 /hls/ 转发给 MediaMTX，因此不需要额外的跨域配置。
func (s *Server) playbackFor(username string) *playback {
	stream := url.PathEscape(username)
	out := &playback{}

	if s.hls != nil {
		out.Protocol = "hls"
		out.HLSURL = "/hls/" + s.cfg.MediaMTX.App + "/" + stream + "/index.m3u8"
	}
	if s.whep != nil {
		if out.Protocol == "" {
			out.Protocol = "webrtc"
		}
		out.WhepURL = "/rtc/" + s.cfg.MediaMTX.App + "/" + stream + "/whep"
	}
	if out.Protocol == "" {
		// 两个上游都没配置时依然返回结构体，前端据此显示"未启用播放"的提示。
		out.Protocol = "none"
	}
	return out
}
