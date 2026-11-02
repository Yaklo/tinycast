package httpapi

import (
	"net/http"
	"time"
)

// mediaWriteTimeout 是 /hls/、/rtc/ 反代响应的单次写期限。
//
// 反代用 io.Copy 把上游数据流式拷给浏览器：客户端完全不读时，Write 会一直阻塞在
// 内核发送缓冲区上，goroutine、全局媒体槽与单 IP 配额就被一条连接占住。
//
// 期限每次 Write 前都会刷新，所以它切断的是「完全停滞超过 30 秒」的连接；
// 一直慢读但不停（每秒几十字节）的客户端仍能占住槽位较久，要根治得按带宽限流，
// 那属于另一层的事。MediaMTX 的 HLS 分片只有几百 KB，30 秒还没写完说明对端已不读。
const mediaWriteTimeout = 30 * time.Second

// mediaDeadlineWriter 在每次写之前刷新响应写期限，并把 Flush/Unwrap 透传给下层。
//
// SSE 不走这里：events.go 自己按 sseWriteTimeout 刷新期限，两条路径互不影响。
// 响应结束后 net/http 会把连接上的写期限清空（finishRequest 之后统一重置），
// 所以这里不必手动清理，keep-alive 复用连接不会带着过期期限。
type mediaDeadlineWriter struct {
	http.ResponseWriter
	controller *http.ResponseController
	timeout    time.Duration
	now        func() time.Time
}

func newMediaDeadlineWriter(w http.ResponseWriter, timeout time.Duration) *mediaDeadlineWriter {
	return &mediaDeadlineWriter{
		ResponseWriter: w,
		controller:     http.NewResponseController(w),
		timeout:        timeout,
		now:            time.Now,
	}
}

// Write 先刷新期限再写出：期限覆盖这次 Write 的整个阻塞过程（含首个字节的 flush）。
func (w *mediaDeadlineWriter) Write(p []byte) (int, error) {
	w.refreshDeadline()
	return w.ResponseWriter.Write(p)
}

// WriteHeader 也要刷新期限：302 这类没有响应体的转发只写响应头，
// 慢读客户端同样会卡在头部写出上。
func (w *mediaDeadlineWriter) WriteHeader(status int) {
	w.refreshDeadline()
	w.ResponseWriter.WriteHeader(status)
}

func (w *mediaDeadlineWriter) refreshDeadline() {
	if w.timeout <= 0 {
		return
	}
	// 不支持写期限的连接（例如测试里的 ResponseRecorder）会返回 http.ErrNotSupported：
	// 忽略即可，没有期限也好过让播放直接失败。
	_ = w.controller.SetWriteDeadline(w.now().Add(w.timeout))
}

// Flush 必须保留：反代配置了 FlushInterval=-1，HLS 分片与 WHEP 信令靠它立即送达。
//
// 这里也要先刷新期限：ReverseProxy 通过 http.NewResponseController(dst).Flush
// 调用它，响应头与缓冲区可能在 Write 之外就被 flush，慢读客户端同样会卡在这一步。
func (w *mediaDeadlineWriter) Flush() {
	w.refreshDeadline()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap 让 http.ResponseController 能继续向下找到真正的连接。
func (w *mediaDeadlineWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
