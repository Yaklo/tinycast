package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEnvFileSetsValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tinycast.env")
	content := strings.Join([]string{
		"# 注释行会被忽略",
		"",
		"HTTP_ADDR=:9090",
		"  ROOM_GRACE = 120s  ",
		"PUBLIC_BASE_URL=\"https://live.example.com\"",
		"export LOG_LEVEL=debug",
	}, "\r\n") + "\r\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, k := range []string{"HTTP_ADDR", "ROOM_GRACE", "PUBLIC_BASE_URL", "LOG_LEVEL"} {
		t.Setenv(k, "") // 注册清理
		os.Unsetenv(k)
	}

	n, err := LoadEnvFile(path)
	if err != nil {
		t.Fatalf("LoadEnvFile: %v", err)
	}
	if n != 4 {
		t.Fatalf("应设置 4 个变量，实际 %d", n)
	}
	for k, want := range map[string]string{
		"HTTP_ADDR":       ":9090",
		"ROOM_GRACE":      "120s",
		"PUBLIC_BASE_URL": "https://live.example.com",
		"LOG_LEVEL":       "debug",
	} {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q，期望 %q", k, got, want)
		}
	}
}

func TestLoadEnvFileKeepsExistingEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tinycast.env")
	if err := os.WriteFile(path, []byte("HTTP_ADDR=:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HTTP_ADDR", ":8080")

	if _, err := LoadEnvFile(path); err != nil {
		t.Fatalf("LoadEnvFile: %v", err)
	}
	if got := os.Getenv("HTTP_ADDR"); got != ":8080" {
		t.Fatalf("已有环境变量不应被文件覆盖，实际 %q", got)
	}
}

func TestLoadEnvFileRejectsBrokenLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tinycast.env")
	if err := os.WriteFile(path, []byte("HTTP_ADDR=:1\n这一行没有等号\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEnvFile(path); err == nil {
		t.Fatal("缺少 = 的行应当报错")
	}
}

func TestLoadEnvFileMissing(t *testing.T) {
	if _, err := LoadEnvFile(filepath.Join(t.TempDir(), "nope.env")); err == nil {
		t.Fatal("文件不存在时应当报错")
	}
}

func TestLoadEnvFileAppliesToLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tinycast.env")
	content := "HTTP_ADDR=:7070\nROOM_GRACE=3m\nSRS_RTMP_PUBLIC_BASE=rtmp://live.example.com/live\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"HTTP_ADDR", "ROOM_GRACE", "SRS_RTMP_PUBLIC_BASE", "JANITOR_INTERVAL"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	if _, err := LoadEnvFile(path); err != nil {
		t.Fatalf("LoadEnvFile: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != ":7070" || cfg.RoomGrace.String() != "3m0s" {
		t.Fatalf("配置未生效：addr=%s grace=%s", cfg.HTTPAddr, cfg.RoomGrace)
	}
	if cfg.SRS.RTMPPublicBase != "rtmp://live.example.com/live" {
		t.Fatalf("RTMP 地址未生效：%s", cfg.SRS.RTMPPublicBase)
	}
}
