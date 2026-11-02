package httpapi

import (
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				// net/http 的 ReverseProxy 在客户端中断下载时会 panic(http.ErrAbortHandler)，
				// 这是"对端已经走了"的正常信号，不是服务端故障：不再打 ERROR 与完整栈。
				// 原样抛回给 net/http，由它静默关闭连接；吞掉会让访问日志把中断记成 200。
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				s.log.Error("请求处理发生 panic", "path", r.URL.Path, "panic", rec, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal_error", "服务内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		// 流式接口（SSE）的耗时没有参考价值，单独降级为 Debug。
		level := slog.LevelInfo
		if r.URL.Path == "/api/events" {
			level = slog.LevelDebug
		}
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		s.log.Log(r.Context(), level, "http 请求",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"bytes", rec.bytes,
			"duration", time.Since(start).Round(time.Millisecond).String(),
			"remote", s.clientIP(r),
		)
	})
}

// statusRecorder 记录响应状态码与字节数，并向下层透传 Flush 能力（SSE 必需）。
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status = code
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.status = http.StatusOK
		r.wrote = true
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (s *Server) cors(next http.Handler) http.Handler {
	if len(s.cfg.CORSOrigins) == 0 {
		return next
	}
	allowAll := false
	allowed := make(map[string]struct{}, len(s.cfg.CORSOrigins))
	for _, o := range s.cfg.CORSOrigins {
		if o == "*" {
			allowAll = true
		}
		allowed[o] = struct{}{}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			_, ok := allowed[origin]
			if ok || allowAll {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Tinycast-Secret")
				w.Header().Set("Access-Control-Max-Age", "600")
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		// 前端是同源 SPA：脚本只来自本站；HLS.js 的 worker 与 MSE 使用 blob:；
		// 样式表由 Vite 打包，但 Vue 过渡仍可能写内联 style，所以 style-src 保留 unsafe-inline。
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self'; worker-src 'self' blob:; font-src 'self' data:; object-src 'none'; base-uri 'self'; frame-ancestors 'self'; form-action 'self'")
		// 只有确实走 HTTPS 时才发 HSTS；本机 HTTP 调试不应被浏览器记住。
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP 返回请求的真实来源地址。
//
// 只有当直连来源是可信反向代理时才采信 X-Forwarded-For：
// 否则任何客户端都能自己写一个 XFF 头来冒充内网地址，绕过鉴权回调白名单。
func (s *Server) clientIP(r *http.Request) string {
	remote := hostOnly(r.RemoteAddr)
	if !s.trustedProxy(remote) {
		return remote
	}
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		// XFF 是“客户端, 代理1, 代理2”的顺序，从右往左剥离可信代理，
		// 取第一个不可信地址。取最左段等于让客户端自己决定 IP。
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			candidate := hostOnly(strings.TrimSpace(parts[i]))
			if candidate == "" {
				continue
			}
			if !s.trustedProxy(candidate) {
				return candidate
			}
		}
		// 整条链都是可信代理时，最左段完全可能由客户端自己写入。
		// 宁可回退到直连地址（可信代理本身），也不要采信这个可伪造的值。
	}
	return remote
}

func hostOnly(addr string) string {
	addr = strings.TrimSpace(addr)
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return strings.Trim(addr, "[]")
}

func (s *Server) trustedProxy(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, network := range s.cfg.TrustedProxies {
		if network.Contains(parsed) {
			return true
		}
	}
	return false
}

// hasForwardedHeader 判断请求是否经过反向代理。
func hasForwardedHeader(r *http.Request) bool {
	return r.Header.Get("X-Forwarded-For") != "" ||
		r.Header.Get("X-Real-IP") != "" ||
		r.Header.Get("Forwarded") != "" ||
		r.Header.Get("X-Forwarded-Host") != "" ||
		r.Header.Get("X-Forwarded-Proto") != ""
}
