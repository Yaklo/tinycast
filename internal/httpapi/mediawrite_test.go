package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tinycast/internal/config"
)

// deadlineRecorder 记录 SetWriteDeadline 的调用，并统计 Flush 次数。
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
	flushes   int
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.deadlines = append(d.deadlines, t)
	return nil
}

func (d *deadlineRecorder) Flush() {
	d.flushes++
	d.ResponseRecorder.Flush()
}

// 每次 Write 之前都要刷新期限，否则慢读客户端会把反代的拷贝 goroutine
// 永久卡在 Write 上，媒体槽与单 IP 配额跟着一起泄漏。
// Flush 也要刷新：反代用 FlushInterval=-1，响应头与缓冲可能在 Write 之外先 flush。
func TestMediaDeadlineWriterRefreshesBeforeWriteAndFlush(t *testing.T) {
	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	start := time.Unix(1_700_000_000, 0)
	now := start
	w := newMediaDeadlineWriter(rec, mediaWriteTimeout)
	w.now = func() time.Time { return now }

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("chunk-1")); err != nil {
		t.Fatalf("写入失败：%v", err)
	}
	now = now.Add(5 * time.Second)
	if _, err := w.Write([]byte("chunk-2")); err != nil {
		t.Fatalf("写入失败：%v", err)
	}
	now = now.Add(5 * time.Second)
	w.Flush()

	if len(rec.deadlines) != 4 {
		t.Fatalf("写响应头、两次写与一次 Flush 各应刷新一次期限，实际 %d 次", len(rec.deadlines))
	}
	if want := start.Add(mediaWriteTimeout); !rec.deadlines[0].Equal(want) {
		t.Fatalf("首个期限应为 now+30s，实际 %s", rec.deadlines[0])
	}
	if want := now.Add(mediaWriteTimeout); !rec.deadlines[3].Equal(want) {
		t.Fatalf("Flush 之前应刷新期限，实际 %s", rec.deadlines[3])
	}
	if rec.flushes != 1 {
		t.Fatalf("Flush 应透传到下层，实际 %d 次", rec.flushes)
	}
	if got := rec.Body.String(); got != "chunk-1chunk-2" {
		t.Fatalf("响应体应透传，实际 %q", got)
	}
	if w.Unwrap() != http.ResponseWriter(rec) {
		t.Fatal("Unwrap 应返回下层 ResponseWriter")
	}
}

// 测试里的 ResponseRecorder 不支持写期限：此时应安静降级，不能让播放失败。
func TestMediaDeadlineWriterWithoutDeadlineSupport(t *testing.T) {
	rec := httptest.NewRecorder()
	w := newMediaDeadlineWriter(rec, mediaWriteTimeout)

	if _, err := w.Write([]byte("ok")); err != nil {
		t.Fatalf("不支持写期限时仍应写入：%v", err)
	}
	w.Flush() // 不应 panic
	if rec.Body.String() != "ok" {
		t.Fatalf("响应体应透传，实际 %q", rec.Body.String())
	}
}

// 下层没有实现 Flusher 时 Flush 不能 panic（例如极简包装的 ResponseWriter）。
func TestMediaDeadlineWriterFlushWithoutFlusher(t *testing.T) {
	w := newMediaDeadlineWriter(struct{ http.ResponseWriter }{httptest.NewRecorder()}, mediaWriteTimeout)
	w.Flush()
}

// 反代路径上真的会设置期限，非媒体接口不受影响（SSE 有自己的一套）。
func TestMediaProxyAppliesWriteDeadline(t *testing.T) {
	up := &fakeUpstream{}
	srvUp := httptest.NewServer(up)
	defer srvUp.Close()

	srv, _ := newTestServer(t, func(c *config.Config) {
		c.MediaMTX.WHEPProxyTarget = srvUp.URL
		c.MediaMTX.HLSProxyTarget = srvUp.URL
	})

	for _, target := range []string{"/hls/live/alex/index.m3u8", "/rtc/live/alex/whep"} {
		rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.RemoteAddr = "127.0.0.1:50000"
		srv.Handler().ServeHTTP(rec, req)

		if len(rec.deadlines) == 0 {
			t.Fatalf("%s 没有设置写期限，慢读客户端会永久占住媒体槽", target)
		}
		got := rec.deadlines[len(rec.deadlines)-1]
		if got.Before(time.Now().Add(mediaWriteTimeout-time.Minute)) || got.After(time.Now().Add(mediaWriteTimeout+time.Minute)) {
			t.Fatalf("%s 的写期限偏离预期：%s", target, got)
		}
	}

	// /api/rooms 这类普通接口不应该被套上媒体写期限。
	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodGet, "/api/rooms", nil)
	req.RemoteAddr = "127.0.0.1:50000"
	srv.Handler().ServeHTTP(rec, req)
	if len(rec.deadlines) != 0 {
		t.Fatalf("非媒体接口不应设置媒体写期限，实际 %d 次", len(rec.deadlines))
	}
}
