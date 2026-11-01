package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"tinycast/internal/hub"
)

// handleEvents 是一条长期保持的 SSE 连接。
//
// 每次变化都推送一份完整快照：前端不需要维护增量补丁，任何一帧丢失都能靠下一帧自愈。
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
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

	snapshots, release := s.hub.Subscribe()
	defer release()

	// 断线后 3 秒重连，同时立即补一帧当前状态。
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
			if err := writeSnapshot(w, snap); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			// 注释行不会被前端当作事件，只用来保活并探测断开。
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
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
