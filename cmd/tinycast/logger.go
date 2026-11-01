package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"

	"tinycast/internal/logfile"
)

// consoleLevel 决定终端的最低输出级别：非 debug 模式只让报错上屏。
//
// 逐请求访问日志与巡检日志只写进日志文件，终端保持安静；
// LOG_LEVEL=debug 时终端与文件一致，方便本地排查。
func consoleLevel(level slog.Level) slog.Level {
	if level <= slog.LevelDebug {
		return level
	}
	return slog.LevelError
}

// parseLogLevel 把 LOG_LEVEL 解析成级别，无法识别时按 info 处理。
func parseLogLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// newLogger 构造日志器：终端只输出报错（debug 模式除外），
// 配置了 LOG_FILE 时再按 LOG_LEVEL 把完整日志按大小滚动落盘。
func newLogger() (*slog.Logger, io.Closer, error) {
	level := parseLogLevel(os.Getenv("LOG_LEVEL"))

	var file io.Writer
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
		file = w
		closer = w
	}

	return buildLogger(level, os.Stdout, file), closer, nil
}

// buildLogger 按级别组装日志器：console 走 consoleLevel 过滤，file 走完整级别。
//
// 两个处理器用 fanout 组合，而不是 io.MultiWriter：多路写入必须各自持有一份级别，
// 否则终端与文件只能共用一个门槛。
func buildLogger(level slog.Level, console, file io.Writer) *slog.Logger {
	handlers := []slog.Handler{
		slog.NewTextHandler(console, &slog.HandlerOptions{Level: consoleLevel(level)}),
	}
	if file != nil {
		handlers = append(handlers, slog.NewTextHandler(file, &slog.HandlerOptions{Level: level}))
	}
	return slog.New(fanout{handlers: handlers})
}

// fanout 把一条记录分发给多个处理器，任一处理器启用即启用。
type fanout struct {
	handlers []slog.Handler
}

func (f fanout) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (f fanout) Handle(ctx context.Context, rec slog.Record) error {
	var errs []error
	for _, h := range f.handlers {
		if !h.Enabled(ctx, rec.Level) {
			continue
		}
		// 每个处理器都要一份可重复读取的记录（Record 复制是廉价的）。
		if err := h.Handle(ctx, rec.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithAttrs(attrs)
	}
	return fanout{handlers: next}
}

func (f fanout) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithGroup(name)
	}
	return fanout{handlers: next}
}
