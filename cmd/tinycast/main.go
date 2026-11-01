// Command tinycast 是轻量级直播推流平台的业务后端。
//
// 它只负责鉴权、房间生命周期与观看人数统计；音视频转发由 SRS 完成。
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"tinycast/internal/config"
	"tinycast/internal/httpapi"
	"tinycast/internal/hub"
	"tinycast/internal/janitor"
	"tinycast/internal/srsapi"
)

func main() {
	if err := run(); err != nil {
		slog.Error("服务异常退出", "err", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "", "HTTP 监听地址，覆盖环境变量 HTTP_ADDR")
	open := flag.Bool("open", false, "启动后用默认浏览器打开首页")
	flag.Parse()

	logger := newLogger()
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *addr != "" {
		cfg.HTTPAddr = *addr
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	h := hub.New(hub.Options{Grace: cfg.RoomGrace})
	go h.Run(ctx)

	var inspector *srsapi.Inspector
	if cfg.SRS.ClientsAPIURL != "" {
		inspector = srsapi.New(cfg.SRS.ClientsAPIURL, cfg.SRS.ClientsAPIUser, cfg.SRS.ClientsAPIPassword)
		logger.Info("已启用 SRS 在线状态核对", "endpoint", cfg.SRS.ClientsAPIURL)
	}

	runner := janitor.New(janitor.Options{
		Hub:       h,
		Log:       logger,
		Interval:  cfg.JanitorInterval,
		App:       cfg.SRS.App,
		Inspector: inspector,
	})
	go runner.Run(ctx)

	if cfg.SRS.CallbackSecret == "" {
		logger.Warn("未配置 SRS_CALLBACK_SECRET，回调接口仅依赖来源 IP 白名单保护")
	}
	if cfg.SRS.RTCProxyTarget == "" && cfg.SRS.RTCPublicBase == "" {
		logger.Warn("未配置 SRS_RTC_PROXY_TARGET 或 SRS_RTC_PUBLIC_BASE，WebRTC 信令需要由反向代理转发 /rtc/")
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.New(h, cfg, logger).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// 不设置 WriteTimeout：SSE 需要长期保持连接。
	}

	if *open {
		// 稍等片刻再开浏览器，避免页面比监听端口先就绪。
		go func() {
			time.Sleep(300 * time.Millisecond)
			if err := openBrowser(localURL(cfg.HTTPAddr)); err != nil {
				logger.Warn("无法自动打开浏览器，请手动访问", "url", localURL(cfg.HTTPAddr), "err", err)
			}
		}()
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("tinycast 已启动",
			"addr", cfg.HTTPAddr,
			"app", cfg.SRS.App,
			"grace", cfg.RoomGrace.String(),
			"janitor", cfg.JanitorInterval.String(),
		)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("收到退出信号，正在关闭服务")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}
	logger.Info("服务已停止")
	return nil
}

func newLogger() *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}
