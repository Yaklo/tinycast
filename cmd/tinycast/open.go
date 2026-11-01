package main

import (
	"net"
	"os/exec"
	"runtime"
)

// localURL 把监听地址翻译成浏览器能直接打开的地址。
//
// 监听 0.0.0.0 或 :8080 时浏览器无法直接访问，这里统一换成 127.0.0.1。
func localURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://127.0.0.1:8080/"
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/"
}

// openBrowser 用系统默认浏览器打开地址。
func openBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// start 的第一个引号参数是窗口标题，必须留空，否则 URL 会被当成标题。
		cmd = exec.Command("cmd", "/c", "start", "", rawURL)
	case "darwin":
		cmd = exec.Command("open", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Start()
}
