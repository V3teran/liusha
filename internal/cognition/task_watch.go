// task_watch.go 任务行监视：认知循环观察任务生命周期的唯一接入点。
//
// 暂停 / 继续 / 停止只写任务行（与 API 响应同事务生效，没有投递失败一说）；循环按固定
// 节奏读任务行，把终态与暂停转成进程内的停止信号。

package cognition

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/task"
)

// taskWatchInterval 是任务行的轮询周期。与完成检测器的检查节奏（5s）对齐：人工操作的
// 生效延迟上限约为一个周期。
const taskWatchInterval = 5 * time.Second

// TaskStateReader 是监视需要的任务面（最小接口，*task.Store 天然满足）。
type TaskStateReader interface {
	GetByID(ctx context.Context, id string) (task.Task, error)
}

// StopReasonPaused 是暂停退出的停止原因（导出供 runner 收尾分流）：
// executeTask 据此识别——run 落 aborted、task 保持 active、目标保持 open、
// 会话身份不回收（继续时复用）。
const StopReasonPaused = "user_paused"

// StopReasonTerminated 是任务终态退出的停止原因：任务行已被停止侧置为终态
// （用户停止 / 判死），本 run 携此原因落 aborted，收尾不再写任务行。
const StopReasonTerminated = "task_terminated"

// taskWatcher 轮询任务行：终态或暂停即停 agents、携停止原因结束本 run；目标被调整即
// 通知（规划者立即按新目标重规划、监察者立即复核在途动作）。通知只让变化更快生效，
// 正确性不依赖它：规划结论以目标依据为前置落图（见 explorationgraph.GoalBasis）。
type taskWatcher struct {
	taskID        string
	tasks         TaskStateReader
	goal          string              // 上次看到的目标（run 开始时的根目标）
	stopRun       func(reason string) // 停 agents 并以 reason 结束本 run
	onGoalChanged func()              // 目标被调整
	logger        zerolog.Logger
}

// start 启动轮询，返回停止函数。只等 ctx 退出是不够的：run 被中止后 ctx 依然存活（它
// 归 asynq 任务，不随完成检测器的中止取消），停止函数须能主动叫停轮询，否则收尾的
// defer 链会卡在这里、任务终态落不了库。
func (w *taskWatcher) start(ctx context.Context) (stop func()) {
	done := make(chan struct{})
	stopCh := make(chan struct{})
	logx.Go(w.logger, "task-watch", func() {
		defer close(done)
		ticker := time.NewTicker(taskWatchInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			case <-ticker.C:
				if w.observe(ctx) {
					return
				}
			}
		}
	})
	return func() {
		close(stopCh)
		<-done
	}
}

// observe 读一次任务行并据此处置，返回本 run 是否已被结束。读失败记录后等下一轮。
func (w *taskWatcher) observe(ctx context.Context) bool {
	tk, err := w.tasks.GetByID(ctx, w.taskID)
	if err != nil {
		w.logger.Warn().Err(err).Msg("读取任务行失败（下轮重试）")
		return false
	}
	switch {
	case tk.Status != task.StatusActive:
		w.logger.Info().Str("status", string(tk.Status)).Msg("观察到任务终态，停止 agents 并结束本 run")
		w.stopRun(StopReasonTerminated)
		return true
	case tk.Paused:
		// 任务行保持 active+paused，探索图与工作区原样保留，继续时全新 run 从图续跑。
		w.logger.Info().Msg("观察到任务暂停，停止 agents 并结束本 run")
		w.stopRun(StopReasonPaused)
		return true
	}
	if tk.Goal != w.goal {
		w.goal = tk.Goal
		w.logger.Info().Msg("观察到任务目标被调整，通知规划者重规划、监察者复核")
		w.onGoalChanged()
	}
	return false
}
