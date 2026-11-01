package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// 非 debug 模式下终端只留报错，信息级日志只进文件。
func TestBuildLoggerKeepsConsoleQuiet(t *testing.T) {
	var console, file bytes.Buffer
	log := buildLogger(slog.LevelInfo, &console, &file)

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

// debug 模式下终端与文件一致，方便本地排查。
func TestBuildLoggerDebugMirrorsConsole(t *testing.T) {
	var console, file bytes.Buffer
	log := buildLogger(slog.LevelDebug, &console, &file)

	log.Debug("收到 SRS 回调", "action", "publish")
	log.Info("推流开始", "username", "alex")

	for _, want := range []string{"收到 SRS 回调", "推流开始"} {
		if !strings.Contains(console.String(), want) {
			t.Fatalf("debug 模式终端应输出 %q: %q", want, console.String())
		}
		if !strings.Contains(file.String(), want) {
			t.Fatalf("debug 模式日志文件应输出 %q: %q", want, file.String())
		}
	}
}

// 未配置 LOG_FILE 时终端仍然只有报错，且不会 panic。
func TestBuildLoggerWithoutFile(t *testing.T) {
	var console bytes.Buffer
	log := buildLogger(slog.LevelInfo, &console, nil)

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
	log := buildLogger(slog.LevelDebug, &a, &b).With("request", "r-1").WithGroup("http")

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

func TestParseLogLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"":         slog.LevelInfo,
		"info":     slog.LevelInfo,
		"DEBUG":    slog.LevelDebug,
		" debug ":  slog.LevelDebug,
		"warn":     slog.LevelWarn,
		"warning":  slog.LevelWarn,
		"error":    slog.LevelError,
		"nonsense": slog.LevelInfo,
	}
	for raw, want := range cases {
		if got := parseLogLevel(raw); got != want {
			t.Fatalf("parseLogLevel(%q) = %v，期望 %v", raw, got, want)
		}
	}
}
