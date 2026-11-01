package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotateKeepsHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tinycast.log")
	w, err := New(Options{Path: path, MaxBytes: 100, Keep: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	line := strings.Repeat("x", 40) + "\n"
	for i := 0; i < 10; i++ {
		if _, err := w.Write([]byte(line)); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("当前日志不存在: %v", err)
	}
	for _, suffix := range []string{".1", ".2"} {
		if _, err := os.Stat(path + suffix); err != nil {
			t.Fatalf("历史日志 %s 不存在: %v", suffix, err)
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("Keep=2 时不应保留 .3，err=%v", err)
	}
}

func TestWriteAfterCloseFails(t *testing.T) {
	w, err := New(Options{Path: filepath.Join(t.TempDir(), "a.log")})
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if _, err := w.Write([]byte("x")); err == nil {
		t.Fatal("关闭后写入应当报错")
	}
	if err := w.Close(); err != nil {
		t.Fatalf("重复关闭不应报错: %v", err)
	}
}

func TestInvalidPath(t *testing.T) {
	if _, err := New(Options{Path: ""}); err == nil {
		t.Fatal("空路径应当报错")
	}
}
