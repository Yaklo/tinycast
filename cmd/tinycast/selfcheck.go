package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	"tinycast/internal/mtx"
)

// checkMTXCallback 在启动时核对 MediaMTX 的鉴权回调地址是否指向本进程。
//
// 端口被写死是这类部署最常见的坑：只改了 HTTP_ADDR，MediaMTX 仍然回调旧端口，
// 推流会被 MediaMTX 拒绝，而 tinycast 自己看起来一切正常，很难定位。
func checkMTXCallback(ctx context.Context, client *mtx.Client, logger *slog.Logger, httpAddr string) {
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	cfg, err := client.GlobalConfig(checkCtx)
	if err != nil {
		logger.Warn("无法读取 MediaMTX 配置，跳过回调地址自检", "err", err)
		return
	}
	addr := strings.TrimSpace(cfg.AuthHTTPAddress)
	if addr == "" {
		logger.Error("MediaMTX 没有配置 authHTTPAddress，推流鉴权不会生效")
		return
	}
	if problem := callbackPortProblem(addr, httpAddr); problem != "" {
		logger.Error("MediaMTX 鉴权回调地址与 TinyCast 监听地址不一致",
			"authHTTPAddress", addr, "listenAddr", httpAddr, "hint", problem)
	}
}

// isLoopbackListenAddr 判断监听地址是否只绑定回环。
// 空主机名（例如 ":18856"）表示所有网卡，不算回环。
func isLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil || host == "" {
		return false
	}
	if strings.EqualFold(strings.Trim(host, "[]"), "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// callbackPortProblem 只比较端口：容器部署时主机名是服务名（tinycast），
// 与本机监听地址的字面值不同是正常的。
func callbackPortProblem(authAddr, httpAddr string) string {
	u, err := url.Parse(strings.TrimSpace(authAddr))
	if err != nil || u.Host == "" {
		return "authHTTPAddress 不是合法的绝对地址"
	}
	_, wantPort, err := net.SplitHostPort(strings.TrimSpace(httpAddr))
	if err != nil || wantPort == "" {
		return ""
	}
	gotPort := u.Port()
	if gotPort == "" {
		if strings.EqualFold(u.Scheme, "https") {
			gotPort = "443"
		} else {
			gotPort = "80"
		}
	}
	if gotPort != wantPort {
		return fmt.Sprintf("MediaMTX 会回调 %s，但 TinyCast 监听在 %s；请把 mediamtx.yml 的 authHTTPAddress 改成与 HTTP_ADDR（单独配置时是 INTERNAL_ADDR）相同的端口", gotPort, wantPort)
	}
	return ""
}
