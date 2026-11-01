// Package webui 把前端构建产物打包进二进制，实现"单文件部署"。
//
// 前端构建时会把产物输出到本包的 dist 目录（见 web/vite.config.ts），
// 因此 go build 得到的可执行文件即可同时提供 API 与页面。
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"
)

//go:embed all:dist
var assets embed.FS

// Handler 返回托管前端静态资源的 http.Handler（带 SPA 回退）。
func Handler() http.Handler {
	dist, err := fs.Sub(assets, "dist")
	if err != nil {
		return http.HandlerFunc(serveNotBuilt)
	}
	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return http.HandlerFunc(serveNotBuilt)
	}

	files := http.FileServer(http.FS(dist))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}

		// assets/ 下的文件名带内容哈希，缺失就是真缺失，不该回退到首页。
		if strings.HasPrefix(name, "assets/") {
			if info, err := fs.Stat(dist, name); err != nil || info.IsDir() {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			cloned := r.Clone(r.Context())
			cloned.URL.Path = "/" + name
			files.ServeHTTP(w, cloned)
			return
		}

		// 其余路径（含 /r/<username> 这类前端路由）一律回退到 index.html。
		w.Header().Set("Cache-Control", "no-cache")
		serveIndex(w, r, index)
	})
}

// serveIndex 直接写出 index.html 的内容。
//
// 这里不用 http.FileServer：它会把 /index.html 重定向到 ./，导致首页与所有前端路由都变成 301。
func serveIndex(w http.ResponseWriter, r *http.Request, index []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(index)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(index)
}

// notBuiltPage 在未构建前端时给出明确的提示，而不是一个白屏。
const notBuiltPage = `<!doctype html>
<html lang="zh-CN">
<head><meta charset="utf-8"><title>tinycast</title>
<style>body{font-family:system-ui,sans-serif;max-width:44rem;margin:4rem auto;padding:0 1.5rem;line-height:1.7;color:#1f2933}
code{background:#f1f5f9;padding:.15rem .4rem;border-radius:.25rem}</style></head>
<body>
<h1>前端尚未构建</h1>
<p>后端已经启动，但可执行文件里没有打包前端产物。</p>
<ul>
<li>开发模式：在 <code>web/</code> 目录执行 <code>pnpm dev</code>，然后访问 Vite 打印的地址。</li>
<li>生产模式：在仓库根目录执行 <code>./scripts/build.sh</code>，再重新构建后端。</li>
</ul>
<p>API 仍然可用：<code>/api/rooms</code>、<code>/api/events</code>。</p>
</body>
</html>`

func serveNotBuilt(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(notBuiltPage))
}
