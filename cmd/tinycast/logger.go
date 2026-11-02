package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"tinycast/internal/logfile"
)

// parseConsoleLevel 解析终端级别。未配置时跟随文件级别，
// 这样"终端没输出、文件里却有日志"不会再次发生；想看详细输出时设 info/debug。
func parseConsoleLevel(raw string, fallback slog.Level) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return fallback
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return fallback
	}
}

// parseLogLevel 把 LOG_LEVEL 解析成级别，未配置或无法识别时按 error 处理。
func parseLogLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelError
	}
}

// knownLogLevel 判断配置值是否是受支持的级别，用来在笔误时提醒而不是静默降级。
func knownLogLevel(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug", "info", "warn", "warning", "error":
		return true
	default:
		return false
	}
}

// newLogger 构造日志器：终端级别默认与文件一致（LOG_LEVEL），
// 可用 LOG_CONSOLE_LEVEL 单独调整；配置了 LOG_FILE 时再按大小滚动落盘。
//
// 日志文件打不开时退化为仅终端，而不是让整个服务起不来：只读文件系统、
// 卷属主配错的容器很常见，少一份文件日志不应该变成 crash-loop。
func newLogger() (*slog.Logger, io.Closer) {
	rawLevel := strings.TrimSpace(os.Getenv("LOG_LEVEL"))
	if rawLevel != "" && !knownLogLevel(rawLevel) {
		fmt.Fprintf(os.Stderr, "tinycast: 无法识别 LOG_LEVEL=%q，已按 error 处理\n", rawLevel)
	}
	level := parseLogLevel(rawLevel)
	console := parseConsoleLevel(os.Getenv("LOG_CONSOLE_LEVEL"), level)

	var file io.Writer
	var closer io.Closer
	if path := strings.TrimSpace(os.Getenv("LOG_FILE")); path != "" {
		w, err := logfile.New(logfile.Options{
			Path:     path,
			MaxBytes: envInt64("LOG_MAX_MB", 16) << 20,
			Keep:     int(envInt64("LOG_KEEP", 3)),
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "tinycast: 无法打开日志文件 %s：%v；本次只输出到终端\n", path, err)
		} else {
			file = w
			closer = w
		}
	}

	return buildLogger(console, level, os.Stdout, file), closer
}

// buildLogger 按级别组装日志器：console 与 file 各用各的门槛。
//
// 两个处理器用 fanout 组合，而不是 io.MultiWriter：多路写入必须各自持有一份级别，
// 否则终端与文件只能共用一个门槛。
func buildLogger(consoleLevel, fileLevel slog.Level, console, file io.Writer) *slog.Logger {
	handlers := []slog.Handler{
		slog.NewTextHandler(console, &slog.HandlerOptions{Level: consoleLevel}),
	}
	if file != nil {
		handlers = append(handlers, slog.NewTextHandler(file, &slog.HandlerOptions{Level: fileLevel}))
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
