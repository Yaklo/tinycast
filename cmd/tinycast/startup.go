package main

import (
	"fmt"
	"net"

	"tinycast/internal/config"
)

// validateInternalAddr 在启动前拒绝「内部回调端口对外监听 + 没有共享密钥」的组合。
//
// 这种配置下，同网段的任何人都能直接 POST /internal/mtx/auth 伪造推流身份、
// 抢占别人的用户名，而 tinycast 自己看起来一切正常。只打一条 ERROR 日志继续
// 启动等于把安全边界交给运维读日志的习惯，所以这里直接让启动失败。
//
// 回环地址（127.0.0.1、::1、localhost）允许：那是 MediaMTX 与本进程同机的默认部署。
// INTERNAL_ADDR 为空时回调沿用 HTTP_ADDR，行为保持不变。
func validateInternalAddr(cfg *config.Config) error {
	if cfg == nil || cfg.InternalAddr == "" {
		return nil
	}
	// 地址写错（例如漏了端口）不该被当成「对外监听」：错误信息要指向真正的毛病，
	// 否则运维会去查密钥而不是改地址。
	if _, _, err := net.SplitHostPort(cfg.InternalAddr); err != nil {
		return fmt.Errorf("config: INTERNAL_ADDR=%q 不是合法的 host:port 形式：%w", cfg.InternalAddr, err)
	}
	if config.SameListenAddr(cfg.InternalAddr, cfg.HTTPAddr) {
		return fmt.Errorf("config: INTERNAL_ADDR=%s 与 HTTP_ADDR=%s 指向同一个监听地址；这会把内部回调暴露在公网端口。请改用独立端口（例如 127.0.0.1:18856），或留空 INTERNAL_ADDR 表示共用端口", cfg.InternalAddr, cfg.HTTPAddr)
	}
	if isLoopbackListenAddr(cfg.InternalAddr) {
		return nil
	}
	if cfg.MediaMTX.AuthSecret != "" {
		return nil
	}
	return fmt.Errorf("config: INTERNAL_ADDR=%s 监听在非回环地址，且未配置 MEDIAMTX_AUTH_SECRET；同网段任何人都能伪造推流鉴权，请改用回环地址（例如 127.0.0.1:18856）或设置 MEDIAMTX_AUTH_SECRET", cfg.InternalAddr)
}
