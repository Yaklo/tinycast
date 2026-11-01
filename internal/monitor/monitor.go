// Package monitor 把 MediaMTX 的推流清单翻译成房间状态。
//
// 为什么用轮询而不是回调：MediaMTX 只在连接建立时回调鉴权，没有"推流开始/结束"事件，
// 但 /v3/paths/list 会实时列出每条路径（断开后整条消失）。一秒一次的轮询足以让
// 首页状态与人数在肉眼看来是实时的，而且实现只有几十行、没有任何额外组件。
package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"tinycast/internal/hub"
	"tinycast/internal/mtx"
)

// Monitor 周期性同步 MediaMTX → 内存状态。
// maxAPIFailures 是连续拉取失败的容忍次数；超过后把 LIVE 房间降级为 GRACE，
// 避免 MediaMTX 已经挂掉而页面一直显示“正在直播”。
const maxAPIFailures = 5

type Monitor struct {
	hub            *hub.Hub
	client         *mtx.Client
	app            string
	interval       time.Duration
	viewerTimeout  time.Duration
	bindingTimeout time.Duration
	logger         *slog.Logger
	failures       int
	degraded       bool
}

// Options 汇总 Monitor 的可调参数。
type Options struct {
	// App 是 MediaMTX 的路径前缀（例如 live），用于把用户名映射成 live/alex。
	App string
	// Interval 是轮询间隔。
	Interval time.Duration
	// ViewerTimeout 是观看心跳超时时间。
	ViewerTimeout time.Duration
	// BindingTimeout 是"鉴权通过但没推成"的绑定保留时间。
	BindingTimeout time.Duration
	// Logger 为空时使用 slog.Default()。
	Logger *slog.Logger
}

// New 创建一个 Monitor。
func New(h *hub.Hub, client *mtx.Client, opts Options) *Monitor {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Monitor{
		hub:            h,
		client:         client,
		app:            opts.App,
		interval:       opts.Interval,
		viewerTimeout:  opts.ViewerTimeout,
		bindingTimeout: opts.BindingTimeout,
		logger:         opts.Logger,
	}
}

// Run 阻塞轮询，直到 ctx 被取消。启动时先同步一次，避免首页空等一个周期。
func (m *Monitor) Run(ctx context.Context) {
	// 巡检 goroutine 里的 panic 不能拖垮整个进程（HTTP 有 recover，这里没有）。
	defer func() {
		if rec := recover(); rec != nil {
			m.logger.Error("MediaMTX 状态巡检发生 panic", "panic", rec, "stack", string(debug.Stack()))
		}
	}()

	m.Tick(ctx)

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.Tick(ctx)
		}
	}
}

// Tick 执行一轮同步：确认推流、标记断流、清理会话与过期身份。
//
// 返回值表示内存状态是否发生变化，方便测试断言。
func (m *Monitor) Tick(ctx context.Context) bool {
	changed := false

	paths, err := m.client.Paths(ctx)
	if err != nil {
		m.failures++
		switch {
		case m.failures == 1:
			// 偶发失败保持现状：MediaMTX 重启会让所有流短暂消失，
			// 若据此把全部房间打成 GRACE，观众会看到一片假警报。
			m.logger.Warn("拉取 MediaMTX 推流清单失败，本轮保持现状", "err", err)
		case m.failures >= maxAPIFailures && !m.degraded:
			m.degraded = true
			n := 0
			for _, room := range m.hub.Rooms() {
				if room.State == hub.StateLive && m.hub.MarkGrace(room.Username) {
					n++
				}
			}
			m.logger.Error("MediaMTX 控制接口持续不可用，已把直播中的房间降级为断流保留",
				"err", err, "failures", m.failures, "rooms", n)
			if n > 0 {
				changed = true
			}
		case m.failures%60 == 0:
			m.logger.Warn("MediaMTX 控制接口仍不可用", "err", err, "failures", m.failures)
		}
	} else {
		if m.degraded {
			m.logger.Info("MediaMTX 控制接口已恢复")
			m.degraded = false
		}
		m.failures = 0
		changed = m.syncPaths(paths) || changed
	}

	if n := m.hub.PruneViewers(m.viewerTimeout); n > 0 {
		m.logger.Debug("清理无心跳的观看会话", "count", n)
		changed = true
	}
	for _, username := range m.hub.ExpireGrace() {
		m.logger.Info("保留期结束，已释放房间", "username", username)
		changed = true
	}
	for _, username := range m.hub.ExpireBindings(m.bindingTimeout) {
		m.logger.Info("身份绑定超时未推流，已释放", "username", username)
		changed = true
	}

	if changed {
		m.hub.Broadcast()
	}
	return changed
}

// syncPaths 把推流清单与内存房间对齐。
func (m *Monitor) syncPaths(paths map[string]mtx.Path) bool {
	changed := false
	seen := make(map[string]struct{}, len(paths))

	for name, path := range paths {
		app, username := mtx.SplitStreamName(name)
		if app != m.app || !path.Ready {
			continue
		}
		seen[username] = struct{}{}
		if m.hub.ConfirmLive(username, mediaOf(path)) {
			m.logger.Info("推流开始", "username", username, "path", name, "codec", videoCodecOf(path))
			changed = true
		}
	}

	// 内存里还是 LIVE、但清单里已经没有对应路径 → 断流，进入保留期。
	for _, room := range m.hub.Rooms() {
		if room.State != hub.StateLive {
			continue
		}
		if _, ok := seen[room.Username]; ok {
			continue
		}
		if m.hub.MarkGrace(room.Username) {
			m.logger.Info("推流断开，进入保留期", "username", room.Username)
			changed = true
		}
	}
	return changed
}

// mediaOf 从推流清单里提炼出前端需要的编码参数。
func mediaOf(path mtx.Path) *hub.MediaInfo {
	info := &hub.MediaInfo{}
	for _, track := range path.Tracks2 {
		switch normalizeCodec(track.Codec) {
		case "H264", "H265", "AV1", "VP8", "VP9", "MPEG-4 Video":
			if info.VideoCodec == "" {
				info.VideoCodec = normalizeCodec(track.Codec)
				info.VideoProfile = track.CodecProps.Profile
				info.VideoLevel = track.CodecProps.Level
				info.Width = track.CodecProps.Width
				info.Height = track.CodecProps.Height
			}
		default:
			if info.AudioCodec == "" {
				info.AudioCodec = normalizeCodec(track.Codec)
			}
		}
	}
	if info.VideoCodec == "" && info.AudioCodec == "" {
		return nil
	}
	return info
}

// normalizeCodec 把 MediaMTX 的编码名换成前端与日志里更好读的写法。
func normalizeCodec(codec string) string {
	switch strings.ToUpper(strings.TrimSpace(codec)) {
	case "MPEG-4 AUDIO", "MPEG4-AUDIO", "AAC":
		return "AAC"
	case "MPEG-4 VIDEO":
		return "MPEG-4 Video"
	case "G711", "G.711":
		return "G711"
	case "LPCM":
		return "LPCM"
	case "OPUS":
		return "Opus"
	case "H264", "H265", "AV1", "VP8", "VP9":
		return strings.ToUpper(strings.TrimSpace(codec))
	}
	return strings.TrimSpace(codec)
}

func videoCodecOf(path mtx.Path) string {
	for _, track := range path.Tracks2 {
		switch normalizeCodec(track.Codec) {
		case "H264", "H265", "AV1", "VP8", "VP9", "MPEG-4 Video":
			return normalizeCodec(track.Codec)
		}
	}
	return ""
}

// Describe 返回便于日志阅读的一行描述（供 main 启动时打印）。
func (m *Monitor) Describe() string {
	return fmt.Sprintf("app=%s interval=%s viewer_timeout=%s", m.app, m.interval, m.viewerTimeout)
}
