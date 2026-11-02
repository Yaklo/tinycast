// Package logfile 提供一个按大小滚动的日志文件写入器。
//
// 生产环境希望日志落到磁盘，又不引入第三方依赖，所以这里只用标准库实现：
// 超过上限就改名为 xxx.log.1、xxx.log.2 …… 并淘汰最老的若干份。
package logfile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RotatingWriter 把日志追加写入文件，文件超过 MaxBytes 时滚动。
type RotatingWriter struct {
	path     string
	maxBytes int64
	keep     int

	mu   sync.Mutex
	file *os.File
	size int64
	dead bool
	// rotateBlocked 表示滚动失败过一次（通常是 Windows 上文件被占用）。
	// 失败后继续写原文件；隔一段时间再试，既不会每条日志都 rename，
	// 也不会永远放弃滚动让文件无限增长。
	rotateBlocked bool
	rotateRetryAt time.Time
}

// rotateRetryInterval 是滚动失败后的重试间隔。Windows 上文件占用通常只持续
// 几秒到几分钟，5 分钟后重试足够，期间文件最多多长 5 分钟的日志。
const rotateRetryInterval = 5 * time.Minute

// Options 描述滚动策略。
type Options struct {
	// Path 是日志文件路径，目录不存在时会自动创建。
	Path string
	// MaxBytes 是单文件上限（字节），<=0 时默认 16MB。
	MaxBytes int64
	// Keep 是保留的历史文件个数，<=0 时默认 3。
	Keep int
}

const (
	defaultMaxBytes = 16 << 20
	defaultKeep     = 3
)

// New 打开（或创建）日志文件，返回可并发写入的 io.WriteCloser。
func New(opt Options) (*RotatingWriter, error) {
	if opt.Path == "" {
		return nil, fmt.Errorf("logfile: 路径不能为空")
	}
	if opt.MaxBytes <= 0 {
		opt.MaxBytes = defaultMaxBytes
	}
	if opt.Keep <= 0 {
		opt.Keep = defaultKeep
	}
	if dir := filepath.Dir(opt.Path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("logfile: 创建日志目录失败: %w", err)
		}
	}
	w := &RotatingWriter{path: opt.Path, maxBytes: opt.MaxBytes, keep: opt.Keep}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *RotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("logfile: 打开 %s 失败: %w", w.path, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("logfile: 读取 %s 状态失败: %w", w.path, err)
	}
	w.file = f
	w.size = info.Size()
	return nil
}

// Write 实现 io.Writer。
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dead || w.file == nil {
		return 0, fmt.Errorf("logfile: 写入器已关闭")
	}
	if w.size+int64(len(p)) > w.maxBytes && w.size > 0 {
		if !w.rotateBlocked || !time.Now().Before(w.rotateRetryAt) {
			if err := w.rotate(); err != nil {
				return 0, err
			}
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// Close 关闭文件，可重复调用。
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dead {
		return nil
	}
	w.dead = true
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *RotatingWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		// 关闭失败后文件句柄状态不确定，先重新打开再继续写；
		// 否则后续每次 Write 都会命中已关闭的句柄，日志静默消失。
		w.file = nil
		if openErr := w.open(); openErr != nil {
			w.dead = true
			fmt.Fprintf(os.Stderr, "logfile: 关闭旧日志失败且无法重新打开，后续日志将被丢弃: %v / %v\n", err, openErr)
			return fmt.Errorf("logfile: 关闭旧日志失败且无法重新打开: %w", err)
		}
		w.rotateBlocked = true
		w.rotateRetryAt = time.Now().Add(rotateRetryInterval)
		fmt.Fprintf(os.Stderr, "logfile: 关闭旧日志失败，已继续写入原文件，%s 后重试滚动: %v\n", rotateRetryInterval, err)
		return nil
	}
	w.file = nil
	// 从最老的一份开始往后挪：xxx.log.2 -> xxx.log.3，xxx.log.1 -> xxx.log.2
	for i := w.keep - 1; i >= 1; i-- {
		old := fmt.Sprintf("%s.%d", w.path, i)
		newer := fmt.Sprintf("%s.%d", w.path, i+1)
		_ = os.Rename(old, newer)
	}
	if err := os.Rename(w.path, w.path+".1"); err != nil && !os.IsNotExist(err) {
		// Windows 上日志文件被杀毒软件或查看器占用时 rename 会失败。
		// 此时必须把原文件重新打开继续写，否则后续日志会永久静默丢失。
		if openErr := w.open(); openErr != nil {
			w.dead = true
			fmt.Fprintf(os.Stderr, "logfile: 滚动日志失败且无法重新打开，后续日志将被丢弃: %v / %v\n", err, openErr)
			return fmt.Errorf("logfile: 滚动日志失败且无法重新打开: %w", err)
		}
		// 已经重新打开原文件，让本次写入继续；只往 stderr 提醒一次，
		// 不要把错误返回给 Write，否则当前这条日志会丢。
		w.rotateBlocked = true
		w.rotateRetryAt = time.Now().Add(rotateRetryInterval)
		fmt.Fprintf(os.Stderr, "logfile: 滚动日志失败，已继续写入原文件，%s 后重试滚动: %v\n", rotateRetryInterval, err)
		return nil
	}
	if err := w.open(); err != nil {
		w.dead = true
		fmt.Fprintf(os.Stderr, "logfile: 滚动后无法打开新日志文件，后续日志将被丢弃: %v\n", err)
		return err
	}
	w.rotateBlocked = false
	w.rotateRetryAt = time.Time{}
	return nil
}

var _ io.WriteCloser = (*RotatingWriter)(nil)
