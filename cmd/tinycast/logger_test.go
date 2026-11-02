package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// 显式把终端级别设成 error 时，信息级日志只进文件。
func TestBuildLoggerKeepsConsoleQuiet(t *testing.T) {
	var console, file bytes.Buffer
	log := buildLogger(slog.LevelError, slog.LevelInfo, &console, &file)

	log.Info("逐请求访问日志", "path", "/api/rooms")
	log.Warn("房间即将释放")
	log.Error("推流鉴权失败", "username", "alex")

	if strings.Contains(console.String(), "逐请求访问日志") || strings.Contains(console.String(), "房间即将释放") {
		t.Fatalf("终端不应出现信息级或警告级日志: %q", console.String())
	}
	if !strings.Contains(console.String(), "推流鉴权失败") {
		t.Fatalf("终端应保留报错日志: %q", console.String())
	}
	for _, want := range []string{"逐请求访问日志", "房间即将释放", "推流鉴权失败"} {
		if !strings.Contains(file.String(), want) {
			t.Fatalf("日志文件应完整记录 %q: %q", want, file.String())
		}
	}
}

// 未单独配置时终端与文件一致。
func TestBuildLoggerDefaultMirrorsFile(t *testing.T) {
	var console, file bytes.Buffer
	log := buildLogger(slog.LevelInfo, slog.LevelInfo, &console, &file)

	log.Info("推流开始", "username", "alex")

	for _, want := range []string{"推流开始"} {
		if !strings.Contains(console.String(), want) {
			t.Fatalf("终端应输出 %q: %q", want, console.String())
		}
		if !strings.Contains(file.String(), want) {
			t.Fatalf("日志文件应输出 %q: %q", want, file.String())
		}
	}
}

// 未配置 LOG_FILE 时终端仍然按自己的级别输出，且不会 panic。
func TestBuildLoggerWithoutFile(t *testing.T) {
	var console bytes.Buffer
	log := buildLogger(slog.LevelError, slog.LevelInfo, &console, nil)

	log.Info("不该出现")
	log.Error("必须出现")

	if strings.Contains(console.String(), "不该出现") {
		t.Fatalf("终端不应出现信息级日志: %q", console.String())
	}
	if !strings.Contains(console.String(), "必须出现") {
		t.Fatalf("终端应保留报错日志: %q", console.String())
	}
}

// fanout 的 WithAttrs / WithGroup 必须把属性带到每一个处理器。
func TestFanoutWithAttrsReachesEveryHandler(t *testing.T) {
	var a, b bytes.Buffer
	log := buildLogger(slog.LevelDebug, slog.LevelDebug, &a, &b).With("request", "r-1").WithGroup("http")

	log.Error("请求失败", "status", 500)

	for name, buf := range map[string]*bytes.Buffer{"终端": &a, "文件": &b} {
		out := buf.String()
		for _, want := range []string{"request=r-1", "http.status=500", "请求失败"} {
			if !strings.Contains(out, want) {
				t.Fatalf("%s缺少 %q: %q", name, want, out)
			}
		}
	}
}

func TestParseConsoleLevel(t *testing.T) {
	cases := []struct {
		raw      string
		fallback slog.Level
		want     slog.Level
	}{
		{"", slog.LevelInfo, slog.LevelInfo},
		{"", slog.LevelWarn, slog.LevelWarn},
		{"debug", slog.LevelInfo, slog.LevelDebug},
		{" INFO ", slog.LevelWarn, slog.LevelInfo},
		{"warning", slog.LevelInfo, slog.LevelWarn},
		{"error", slog.LevelInfo, slog.LevelError},
		{"nonsense", slog.LevelInfo, slog.LevelInfo},
	}
	for _, c := range cases {
		if got := parseConsoleLevel(c.raw, c.fallback); got != c.want {
			t.Fatalf("parseConsoleLevel(%q, %v) = %v，期望 %v", c.raw, c.fallback, got, c.want)
		}
	}
}

func TestParseLogLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"":         slog.LevelError,
		"info":     slog.LevelInfo,
		"DEBUG":    slog.LevelDebug,
		" debug ":  slog.LevelDebug,
		"warn":     slog.LevelWarn,
		"warning":  slog.LevelWarn,
		"error":    slog.LevelError,
		"nonsense": slog.LevelError,
	}
	for raw, want := range cases {
		if got := parseLogLevel(raw); got != want {
			t.Fatalf("parseLogLevel(%q) = %v，期望 %v", raw, got, want)
		}
	}
}

func TestKnownLogLevel(t *testing.T) {
	for _, raw := range []string{"debug", " INFO ", "warn", "warning", "error"} {
		if !knownLogLevel(raw) {
			t.Fatalf("%q 应被识别", raw)
		}
	}
	for _, raw := range []string{"", "nonsense", "trace"} {
		if knownLogLevel(raw) {
			t.Fatalf("%q 不应被识别", raw)
		}
	}
}
