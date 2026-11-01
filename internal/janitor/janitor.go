// Package janitor 负责后台维护任务：释放超时的 GRACE 房间，
// 并在配置了 SRS HTTP API 时核对在线客户端、修复丢失的回调。
package janitor

import (
	"context"
	"log/slog"
	"time"

	"tinycast/internal/hub"
	"tinycast/internal/srsapi"
)

// minClientAge 是核对前的最小存活时间。
//
// SRS 的 /api/v1/clients/ 有短暂延迟，刚建立的推流/观看连接可能还没出现，
// 因此只核对存在时间超过该阈值的会话，避免误判。
const minClientAge = 10 * time.Second

// Options 是后台任务参数。
type Options struct {
	Hub       *hub.Hub
	Log       *slog.Logger
	Interval  time.Duration
	App       string
	Inspector *srsapi.Inspector
}

// Runner 是后台清理任务。
type Runner struct {
	opts Options
}

// New 创建 Runner。
func New(opts Options) *Runner {
	if opts.Interval <= 0 {
		opts.Interval = 15 * time.Second
	}
	return &Runner{opts: opts}
}

// Run 启动定时任务，直到 ctx 被取消。
func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(r.opts.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.tick(ctx)
		}
	}
}

func (r *Runner) tick(ctx context.Context) {
	for _, username := range r.opts.Hub.ExpireGrace() {
		r.opts.Log.Info("保留期结束，已释放房间", "username", username)
	}

	if r.opts.Inspector == nil {
		return
	}

	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	clients, err := r.opts.Inspector.Clients(callCtx)
	if err != nil {
		// SRS 不可达时不做任何修改：宁可依赖回调，也不要误删状态。
		r.opts.Log.Debug("查询 SRS 客户端列表失败", "err", err)
		return
	}

	alive := make(map[string]struct{}, len(clients))
	for _, c := range clients {
		if c.ID == "" {
			continue
		}
		// 只在两边都带 app 时才比较：SRS 6 的 /api/v1/clients/ 对推流连接
		// （type=fmle-publish）经常返回 app=null、stream=vid-xxx，
		// 若按 app 过滤掉所有空 app 的条目，alive 会永远是空的，
		// 于是每个存活超过 minClientAge 的推流都会被误判成"已断开"。
		if r.opts.App != "" && c.App != "" && c.App != r.opts.App {
			continue
		}
		alive[c.ID] = struct{}{}
	}

	unpublished, pruned := r.opts.Hub.ReconcileClients(func(_, clientID string) bool {
		_, ok := alive[clientID]
		return ok
	}, minClientAge)

	if unpublished > 0 || pruned > 0 {
		r.opts.Log.Warn("已按 SRS 在线状态修正内存状态（回调可能丢失）",
			"断开推流", unpublished, "清理观看会话", pruned)
	}
}
