package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"tinycast/internal/hub"
)

// SSE 连接上限。单机部署下，一个脚本可以轻易打开成千上万条连接，
// 每条连接都占一个文件描述符并参与每次全量广播；不设上限会把轻量服务拖死。
const (
	maxEventStreams      = 1024
	maxEventStreamsPerIP = 16
	// 单次写的上限：慢客户端不能永久占住 goroutine 与配额。
	sseWriteTimeout = 30 * time.Second
)

// handleEvents 是一条长期保持的 SSE 连接。
//
// 每次变化都推送一份完整快照：前端不需要维护增量补丁，任何一帧丢失都能靠下一帧自愈。
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	ip := eventStreamKey(s.clientIP(r))
	if !s.acquireEvent(ip) {
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, "too_many_streams", "实时连接数已达上限，请稍后重试")
		return
	}
	defer s.releaseEvent(ip)

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "stream_unsupported", "当前连接不支持流式输出")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// SSE 没有 WriteTimeout 可依赖（连接本来就是长响应），只能每次写前刷新期限。
	// 不支持 SetWriteDeadline 的连接（例如测试里的 ResponseRecorder）忽略即可。
	controller := http.NewResponseController(w)
	setDeadline := func() { _ = controller.SetWriteDeadline(time.Now().Add(sseWriteTimeout)) }

	snapshots, release := s.hub.Subscribe()
	defer release()

	// 断线后 3 秒重连，同时立即补一帧当前状态。
	setDeadline()
	if _, err := io.WriteString(w, "retry: 3000\n\n"); err != nil {
		return
	}
	if err := writeSnapshot(w, s.hub.Snapshot()); err != nil {
		return
	}
	flusher.Flush()

	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case snap, open := <-snapshots:
			if !open {
				return
			}
			setDeadline()
			if err := writeSnapshot(w, snap); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			// 注释行不会被前端当作事件，只用来保活并探测断开。
			setDeadline()
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// eventStreamKey 把 IPv6 地址收敛到 /64：同一台机器通常整段 /64 可用，
// 逐地址计数等于没有上限。
func eventStreamKey(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() != nil {
		return ip
	}
	return parsed.Mask(net.CIDRMask(64, 128)).String()
}

func (s *Server) acquireEvent(ip string) bool {
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()
	if s.eventsByIP == nil {
		s.eventsByIP = make(map[string]int)
	}
	if s.eventsTotal >= maxEventStreams || s.eventsByIP[ip] >= maxEventStreamsPerIP {
		return false
	}
	s.eventsTotal++
	s.eventsByIP[ip]++
	return true
}

func (s *Server) releaseEvent(ip string) {
	s.eventsMu.Lock()
	defer s.eventsMu.Unlock()
	if s.eventsTotal > 0 {
		s.eventsTotal--
	}
	if n := s.eventsByIP[ip]; n > 1 {
		s.eventsByIP[ip] = n - 1
	} else {
		delete(s.eventsByIP, ip)
	}
}

func writeSnapshot(w io.Writer, snap hub.Snapshot) error {
	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "id: %d\n", snap.Seq)
	b.WriteString("event: rooms\n")
	b.WriteString("data: ")
	b.Write(data)
	b.WriteString("\n\n")
	_, err = io.WriteString(w, b.String())
	return err
}
