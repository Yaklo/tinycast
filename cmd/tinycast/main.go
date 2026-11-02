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
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"tinycast/internal/config"
	"tinycast/internal/httpapi"
	"tinycast/internal/hub"
	"tinycast/internal/monitor"
	"tinycast/internal/mtx"
	"tinycast/internal/version"
)

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() (err error) {
	// 日志器要在退出路径上也可用：这里统一负责“先写致命错误，再关日志文件”，
	// 否则 run 返回后文件已经关闭，运维只能看到一条丢失的 ERROR。
	var (
		logger    *slog.Logger
		logCloser io.Closer
	)
	defer func() {
		if err != nil {
			if logger != nil {
				logger.Error("服务异常退出", "err", err)
			} else {
				slog.Error("服务异常退出", "err", err)
			}
		}
		if logCloser != nil {
			_ = logCloser.Close()
		}
	}()

	addr := flag.String("addr", "", "HTTP 监听地址，覆盖环境变量 HTTP_ADDR")
	envFile := flag.String("config", os.Getenv("TINYCAST_ENV"), "环境变量文件（KEY=VALUE），默认读取 TINYCAST_ENV")
	open := flag.Bool("open", false, "启动后用默认浏览器打开首页")
	showVersion := flag.Bool("version", false, "打印版本信息后退出")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
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

	logger, logCloser = newLogger()
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
		if cfg.InternalAddr == cfg.HTTPAddr {
			cfg.InternalAddr = ""
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	h := hub.New(cfg.RoomGrace)

	mtxClient := mtx.New(cfg.MediaMTX.APIURL, cfg.MediaMTX.APIUser, cfg.MediaMTX.APIPassword)
	callbackAddr := cfg.HTTPAddr
	if cfg.InternalAddr != "" {
		callbackAddr = cfg.InternalAddr
	}
	mon := monitor.New(h, mtxClient, monitor.Options{
		App:            cfg.MediaMTX.App,
		Interval:       cfg.MonitorInterval,
		ViewerTimeout:  cfg.ViewerTimeout,
		BindingTimeout: cfg.PendingTimeout,
		Logger:         logger,
	})

	if cfg.MediaMTX.AuthSecret == "" && cfg.InternalAddr == "" {
		logger.Error("未配置 MEDIAMTX_AUTH_SECRET 且回调与公网端口共用，鉴权回调仅依赖来源 IP 白名单保护")
	}
	if cfg.InternalAddr != "" && !isLoopbackListenAddr(cfg.InternalAddr) {
		if cfg.MediaMTX.AuthSecret == "" {
			logger.Error("内部回调端口监听在非回环地址，且未配置 MEDIAMTX_AUTH_SECRET；同网段任何人都能伪造推流鉴权",
				"internalAddr", cfg.InternalAddr, "hint", "把 INTERNAL_ADDR 改回 127.0.0.1:18856，或设置 MEDIAMTX_AUTH_SECRET")
		} else {
			logger.Warn("内部回调端口监听在非回环地址，请确认只有 MediaMTX 能访问该端口", "internalAddr", cfg.InternalAddr)
		}
	}
	if cfg.MediaMTX.WHEPProxyTarget == "" && cfg.MediaMTX.HLSProxyTarget == "" {
		logger.Error("未配置 MediaMTX 播放代理地址，播放需要由外部反向代理转发 /rtc/ 与 /hls/")
	}

	api := httpapi.New(h, cfg, logger)
	server := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: api.Handler(),
		// 所有请求的 Context 都挂在进程级 ctx 上：收到退出信号时 SSE 与反代
		// 会立刻结束，Shutdown 不必再等满 10 秒的超时。
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 10 * time.Second,
		// 只限制请求读取阶段：SSE 与 HLS 的响应可能持续很久，不能设 WriteTimeout。
		ReadTimeout:    30 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	servers := []*http.Server{server}

	// 独立内部端口只注册鉴权回调：MediaMTX 与本进程同机（或同一容器网络）时使用。
	var internalServer *http.Server
	if cfg.InternalAddr != "" {
		internalServer = &http.Server{
			Addr:              cfg.InternalAddr,
			Handler:           api.InternalHandler(),
			BaseContext:       func(net.Listener) context.Context { return ctx },
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    1 << 20,
		}
		servers = append(servers, internalServer)
	}

	// 先把端口占住再启动巡检：MediaMTX 的 /v3 接口要回调本进程鉴权，
	// 若监听器还没 bind，启动阶段会冒出一条假的「控制接口不可用」。
	mainLn, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("监听公网端口 %s: %w", cfg.HTTPAddr, err)
	}
	var internalLn net.Listener
	if cfg.InternalAddr != "" {
		internalLn, err = net.Listen("tcp", cfg.InternalAddr)
		if err != nil {
			_ = mainLn.Close()
			return fmt.Errorf("监听内部回调端口 %s: %w", cfg.InternalAddr, err)
		}
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

	errCh := make(chan error, len(servers))
	go func() {
		logger.Info("tinycast 已启动",
			"version", version.String(),
			"addr", cfg.HTTPAddr,
			"internalAddr", cfg.InternalAddr,
			"app", cfg.MediaMTX.App,
			"grace", cfg.RoomGrace.String(),
			"monitor", cfg.MonitorInterval.String(),
			"viewerTimeout", cfg.ViewerTimeout.String(),
		)
		if err := server.Serve(mainLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	if internalServer != nil {
		go func() {
			logger.Info("内部回调端口已启动", "addr", cfg.InternalAddr)
			if err := internalServer.Serve(internalLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
	}

	// 监听器已经 ready，巡检可以立即开始；MediaMTX 短暂不可用会保持现状。
	logger.Info("已启动 MediaMTX 状态巡检", "api", cfg.MediaMTX.APIURL, "app", cfg.MediaMTX.App, "interval", cfg.MonitorInterval.String())
	go mon.Run(ctx)

	// 回调自检要访问 MediaMTX 的 /v3，而它又依赖本进程的回调；给 Serve 一点
	// 调度时间，避免正确配置也报一条 ERROR。
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
		checkMTXCallback(ctx, mtxClient, logger, callbackAddr)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("收到退出信号，正在关闭服务")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			if !errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			logger.Warn("优雅停机超时，已强制关闭剩余连接", "addr", srv.Addr)
		}
	}
	logger.Info("服务已停止")
	return nil
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
