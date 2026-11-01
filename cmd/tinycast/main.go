// Command tinycast 是轻量级直播推流平台的业务后端。
//
// 它只负责鉴权、房间生命周期与观看人数统计；音视频转发由 MediaMTX 完成。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"tinycast/internal/config"
	"tinycast/internal/httpapi"
	"tinycast/internal/hub"
	"tinycast/internal/logfile"
	"tinycast/internal/monitor"
	"tinycast/internal/mtx"
)

func main() {
	if err := run(); err != nil {
		slog.Error("服务异常退出", "err", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "", "HTTP 监听地址，覆盖环境变量 HTTP_ADDR")
	envFile := flag.String("config", os.Getenv("TINYCAST_ENV"), "环境变量文件（KEY=VALUE），默认读取 TINYCAST_ENV")
	open := flag.Bool("open", false, "启动后用默认浏览器打开首页")
	version := flag.Bool("version", false, "打印版本信息后退出")
	flag.Parse()

	if *version {
		fmt.Println(buildVersion())
		return nil
	}

	// 配置文件必须先加载：LOG_FILE、LOG_LEVEL 等日志开关也在其中。
	envVars := 0
	if *envFile != "" {
		n, err := config.LoadEnvFile(*envFile)
		if err != nil {
			return err
		}
		envVars = n
	}

	logger, logCloser, err := newLogger()
	if err != nil {
		return err
	}
	if logCloser != nil {
		defer logCloser.Close()
	}
	slog.SetDefault(logger)

	if *envFile != "" {
		logger.Info("已加载配置文件", "path", *envFile, "vars", envVars)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *addr != "" {
		cfg.HTTPAddr = *addr
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	h := hub.New(cfg.RoomGrace)

	mtxClient := mtx.New(cfg.MediaMTX.APIURL, cfg.MediaMTX.APIUser, cfg.MediaMTX.APIPassword)
	mon := monitor.New(h, mtxClient, monitor.Options{
		App:            cfg.MediaMTX.App,
		Interval:       cfg.MonitorInterval,
		ViewerTimeout:  cfg.ViewerTimeout,
		BindingTimeout: cfg.PendingTimeout,
		Logger:         logger,
	})
	go mon.Run(ctx)
	logger.Info("已启动 MediaMTX 状态巡检", "api", cfg.MediaMTX.APIURL, "app", cfg.MediaMTX.App, "interval", cfg.MonitorInterval.String())

	if cfg.MediaMTX.AuthSecret == "" {
		logger.Warn("未配置 MEDIAMTX_AUTH_SECRET，鉴权回调仅依赖来源 IP 白名单保护")
	}
	if cfg.MediaMTX.WHEPProxyTarget == "" && cfg.MediaMTX.HLSProxyTarget == "" {
		logger.Warn("未配置 MediaMTX 播放代理地址，播放需要由外部反向代理转发 /rtc/ 与 /hls/")
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
			"app", cfg.MediaMTX.App,
			"grace", cfg.RoomGrace.String(),
			"monitor", cfg.MonitorInterval.String(),
			"viewerTimeout", cfg.ViewerTimeout.String(),
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

// buildVersion 返回用于运维排查的版本串，例如
// v0.1.0 (rev a1b2c3d, dirty) 或 go1.27.3 (从源码构建，无版本信息)。
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "tinycast (unknown build)"
	}
	var rev, dirty, when string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = ", dirty"
			}
		case "vcs.time":
			when = s.Value
		}
	}
	name := info.Main.Version
	if name == "" || name == "(devel)" {
		name = "tinycast"
	}
	if rev == "" {
		return fmt.Sprintf("%s (%s)", name, info.GoVersion)
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	return fmt.Sprintf("%s (rev %s%s, %s, %s)", name, rev, dirty, when, info.GoVersion)
}

// newLogger 构造日志器：默认写标准输出，配置了 LOG_FILE 时同时按大小滚动落盘。
func newLogger() (*slog.Logger, io.Closer, error) {
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	var out io.Writer = os.Stdout
	var closer io.Closer
	if path := strings.TrimSpace(os.Getenv("LOG_FILE")); path != "" {
		w, err := logfile.New(logfile.Options{
			Path:     path,
			MaxBytes: envInt64("LOG_MAX_MB", 16) << 20,
			Keep:     int(envInt64("LOG_KEEP", 3)),
		})
		if err != nil {
			return nil, nil, err
		}
		out = io.MultiWriter(os.Stdout, w)
		closer = w
	}
	return slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: level})), closer, nil
}

func envInt64(key string, def int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		return def
	}
	return v
}
