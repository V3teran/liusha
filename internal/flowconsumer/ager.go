package flowconsumer

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/window"
	"github.com/V3teran/liusha/internal/worker"
)

// agerTickInterval 是 ager 扫描频率；与 max_age 解耦（后者控的是"多老才关"）。
// 5s 既能让低流量场景下窗口及时关闭，又不至于压扁 PG。
const agerTickInterval = 5 * time.Second

// Ager 周期把超时 open window 关闭并 enqueue sniffer。
//
// 业界最佳实践：业务"窗口关闭"有两路触发——
//
//	路径 A：consumer 写入时 batch 满 → window.OpenOrAppend 自然返回 justClosed=true（已实现）
//	路径 B：低流量场景，batch 未满但超时 → 本 ager 兜底（CloseStaleAll → enqueue）
//
// 这样 e2e 即便只有 18 个请求（不足 20 batch）也能在 max_age 后被分析。
type Ager struct {
	windows *window.Store
	tasks   *task.Store
	enq     *worker.Client
	logger  zerolog.Logger
	maxAgeS int
	tickInt time.Duration
}

// NewAger 构造。maxAgeSec 取自 config.proxy.window_max_age_seconds。
func NewAger(windows *window.Store, tasks *task.Store, enq *worker.Client, maxAgeSec int, logger zerolog.Logger) *Ager {
	return &Ager{
		windows: windows,
		tasks:   tasks,
		enq:     enq,
		logger:  logger,
		maxAgeS: maxAgeSec,
		tickInt: agerTickInterval,
	}
}

// Run 阻塞至 ctx 取消；每 tickInt 扫一次 stale window。
func (a *Ager) Run(ctx context.Context) error {
	a.logger.Info().Int("max_age_sec", a.maxAgeS).Dur("tick", a.tickInt).Msg("flowconsumer ager 已启动")
	t := time.NewTicker(a.tickInt)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			a.tick(ctx)
		}
	}
}

func (a *Ager) tick(ctx context.Context) {
	closed, err := a.windows.CloseStaleAll(ctx, a.maxAgeS)
	if err != nil {
		a.logger.Warn().Err(err).Msg("CloseStaleAll 失败")
		return
	}
	for _, ref := range closed {
		taskID, _, err := EnqueueSnifferTask(ctx, a.tasks, a.enq, ref.EngagementID, ref.WindowID)
		if err != nil {
			a.logger.Warn().Err(err).
				Str("eid", ref.EngagementID).Str("window_id", ref.WindowID).
				Msg("ager: sniffer 入队失败")
			continue
		}
		a.logger.Info().
			Str("eid", ref.EngagementID).Str("window_id", ref.WindowID).Str("task_id", taskID).
			Msg("窗口超时关闭（ager），已投递 sniffer 任务")
	}
}
