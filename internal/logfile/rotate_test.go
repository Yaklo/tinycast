package logfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// 滚动失败（例如 Windows 上文件被占用）后应继续写原文件，且不再每条日志重试。
func TestRotateFailureKeepsWriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tinycast.log")
	// 把 .1 变成目录，rename 必然失败；Keep=1 时不会先挪动 .1。
	if err := os.Mkdir(path+".1", 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := New(Options{Path: path, MaxBytes: 10, Keep: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if _, err := w.Write([]byte("first-line\n")); err != nil {
		t.Fatalf("滚动失败不应让写入报错: %v", err)
	}
	if _, err := w.Write([]byte("second-line\n")); err != nil {
		t.Fatalf("后续写入应继续: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "first-line") || !strings.Contains(string(data), "second-line") {
		t.Fatalf("日志应继续写入原文件: %q", data)
	}
	if !w.rotateBlocked {
		t.Fatal("失败后应标记为暂停滚动，避免每条日志重试")
	}

	// 障碍消失并到重试时间后，应该恢复滚动，而不是永远放弃。
	if err := os.Remove(path + ".1"); err != nil {
		t.Fatal(err)
	}
	w.rotateRetryAt = time.Time{}
	if _, err := w.Write([]byte("third-line\n")); err != nil {
		t.Fatalf("恢复后写入失败: %v", err)
	}
	if w.rotateBlocked {
		t.Fatal("重试成功后不应还标记为暂停滚动")
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("重试成功后应生成历史文件: %v", err)
	}
}
