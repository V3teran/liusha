// control.go 控制平面消费：认知循环对控制面的唯一接入点。
//
// 两条信息流，职责分明：
//   - 生命周期（暂停/继续/停止）：只看 task 行——终态或 paused 即停 agents、
//     携精确停止原因结束本 run。行状态与 API 响应同事务生效，无投递失败一说。
//   - 内容干预（adjust_goal/inject_action）：走 task_control_event 队列，落到
//     探索图上由后续规划消费。

package cognition

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/task"
)

// controlPollInterval 是控制平面的轮询周期（任务行 + 内容事件同节奏）。
// 与完成检测器的检查节奏（5s）对齐：人工干预的生效延迟上限 ≈ 一个周期。
const controlPollInterval = 5 * time.Second

// startControlConsumer 轮询观察任务生命周期并消费内容事件，返回停止函数。
// 每个事件独立处理并回写 processed/failed 状态，单条失败不拖累后续事件。
func startControlConsumer(
	ctx context.Context,
	taskID string,
	store *controlplane.Store,
	graph *explorationgraph.Store,
	detector *CompletionDetector,
	agents *agentLifecycle,
	tasks TaskStateReader,
	logger zerolog.Logger,
) (stop func()) {
	logger = logger.With().Str("component", "control_consumer").Logger()
	done := make(chan struct{})
	// stopCh 让 stop 函数能主动叫停消费循环。只有 ctx.Done() 一个退出条件
	// 是不够的：认知循环被中止后 ctx 依然存活（它归 asynq 任务，不随 detector
	// 中止取消），stop 函数若只等 goroutine 自然退出就会永久阻塞——defer 链
	// 卡死在第一环，任务终态永远落不了库（e2e 实测）。
	stopCh := make(chan struct{})

	logx.Go(logger, "control-consumer", func() {
		defer close(done)
		ticker := time.NewTicker(controlPollInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			case <-ticker.C:
			}

			// 生命周期观察（先于内容事件——终态任务的干预无意义，且滞留的
			// pending 事件随退出一并清理，不跨生命周期迟到消费）。
			tk, gerr := tasks.GetByID(ctx, taskID)
			if gerr != nil {
				logger.Warn().Err(gerr).Msg("读取任务生命周期失败（下轮重试）")
				continue
			}
			if tk.Status != task.StatusActive {
				logger.Info().Str("status", string(tk.Status)).
					Msg("观察到任务终态，停止 agents 并结束本 run")
				if n, rerr := store.ResolvePending(ctx, taskID); rerr != nil {
					logger.Warn().Err(rerr).Msg("清理滞留控制事件失败（续接侧 FollowUp 兜底）")
				} else if n > 0 {
					logger.Info().Int("resolved", n).Msg("任务终态，滞留控制事件已清理")
				}
				agents.Stop()
				detector.Abort(StopReasonTerminated)
				return
			}

			events, err := store.ListPending(ctx, taskID)
			if err != nil {
				logger.Warn().Err(err).Msg("拉取控制事件失败（下轮重试）")
				continue
			}

			for _, ev := range events {
				if err := handleControlEvent(ctx, taskID, graph, ev, logger); err != nil {
					logger.Error().Err(err).Str("event_id", ev.ID.String()).
						Str("command", string(ev.Command)).Msg("控制事件处理失败")
					if markErr := store.MarkFailed(ctx, ev.ID, err.Error()); markErr != nil {
						logger.Warn().Err(markErr).Str("event_id", ev.ID.String()).Msg("标记 failed 失败")
					}
					continue
				}
				if err := store.MarkProcessed(ctx, ev.ID); err != nil {
					logger.Warn().Err(err).Str("event_id", ev.ID.String()).Msg("标记 processed 失败")
				}
			}

			// 暂停观察（事件之后——暂停期间注入的内容事件留给继续后的规划消费）：
			// 停 agents、以 user_paused 结束本 run。任务行保持 active+paused，
			// 探索图与工作区原样保留，继续时全新 run 从图续跑。
			if tk.Paused {
				logger.Info().Msg("观察到任务暂停，停止 agents 并结束本 run")
				agents.Stop()
				detector.Abort(StopReasonPaused)
				return
			}
		}
	})

	return func() {
		close(stopCh)
		<-done
	}
}

// TaskStateReader 是控制消费者需要的任务面（最小接口，*task.Store 天然满足）：
// 单次读回任务行，供生命周期（终态/暂停 → 干净退出）的统一判定。
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

// handleControlEvent 处理单条内容干预事件。
func handleControlEvent(
	ctx context.Context,
	taskID string,
	graph *explorationgraph.Store,
	ev controlplane.ControlEvent,
	logger zerolog.Logger,
) error {
	switch ev.Command {
	case controlplane.CommandAdjustGoal:
		var p controlplane.AdjustGoalPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return err
		}
		if p.NewGoal == "" {
			return errInvalidPayload("new_goal 不能为空")
		}
		return adjustObjective(ctx, taskID, graph, p.NewGoal, logger)

	case controlplane.CommandInjectAction:
		var p controlplane.InjectActionPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return err
		}
		if p.Reason == "" {
			return errInvalidPayload("reason 不能为空（人工注入的意图）")
		}
		return injectAction(ctx, taskID, graph, p, logger)

	default:
		return errInvalidPayload("未知命令: " + string(ev.Command))
	}
}

// adjustObjective 改写 objective 节点的 description——planner 每轮规划从
// 探索图读取目标，下一轮即按新目标行动。无 objective 节点时创建。
func adjustObjective(ctx context.Context, taskID string, graph *explorationgraph.Store, newGoal string, logger zerolog.Logger) error {
	objectives, err := graph.ListNodesByKind(ctx, taskID, core.KindObjective)
	if err != nil {
		return err
	}

	if len(objectives) == 0 {
		// ck_state_by_kind 要求 objective.state 非空——兜底创建与
		// ensureRootObjective 同口径落 open 初始态。
		state := explorationgraph.StateOpen
		node := explorationgraph.Node{
			ID:         uuid.New().String(),
			TaskID:     taskID,
			Kind:       core.KindObjective,
			Content:    mustJSON(map[string]string{"description": newGoal}),
			State:      &state,
			SourceType: explorationgraph.SourceUser,
			SourceID:   "control-plane",
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		if _, err := graph.CreateNode(ctx, node); err != nil {
			return err
		}
		logger.Info().Str("goal", newGoal).Msg("无既有目标，已创建新 objective 节点")
		return nil
	}

	// 保留原 content 的其余字段，只改 description
	obj := objectives[0]
	var content map[string]interface{}
	if err := json.Unmarshal(obj.Content, &content); err != nil {
		content = map[string]interface{}{}
	}
	content["description"] = newGoal

	if err := graph.UpdateNodeContent(ctx, obj.ID, mustJSON(content)); err != nil {
		return err
	}
	logger.Info().Str("node_id", obj.ID).Str("goal", newGoal).Msg("目标已调整")
	return nil
}

// injectAction 把人工注入的意图落成 open 的 action 节点——
// planner 轮询发现后纳入规划（或直接由 executor 认领执行）。
// 注入节点标记 SourceUser/SourceID=control-plane，审计可溯源；与规划产出的
// 动作同样以根目标为归属（generates 边与节点原子落图）。
func injectAction(ctx context.Context, taskID string, graph *explorationgraph.Store, p controlplane.InjectActionPayload, logger zerolog.Logger) error {
	objectives, err := graph.ListNodesByKind(ctx, taskID, core.KindObjective)
	if err != nil {
		return fmt.Errorf("读取目标: %w", err)
	}
	root, ok := explorationgraph.RootObjective(objectives)
	if !ok {
		return fmt.Errorf("任务 %s 没有根目标，注入的动作无处归属", taskID)
	}

	node := explorationgraph.Node{
		ID:     uuid.New().String(),
		TaskID: taskID,
		Kind:   core.KindAction,
		Content: mustJSON(map[string]interface{}{
			"type":        p.Kind,
			"instruction": p.Reason,
			"origin":      "control-plane",
		}),
		State:      ptrState(explorationgraph.StateOpen),
		Priority:   mapPriority(p.Priority),
		SourceType: explorationgraph.SourceUser,
		SourceID:   "control-plane",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	edges := []explorationgraph.Edge{{
		TaskID: taskID, SrcID: root.ID, Rel: explorationgraph.RelGenerates, DstID: node.ID, CreatedAt: time.Now(),
	}}
	if err := graph.CreateNodeWithEdges(ctx, node, edges); err != nil {
		return err
	}
	logger.Info().Str("kind", p.Kind).Str("reason", p.Reason).Msg("已注入 action")
	return nil
}

// mapPriority 把控制平面的数字优先级映射到探索图优先级（0=medium 缺省，1=high，≥2=critical，<0=low）。
func mapPriority(p int) explorationgraph.Priority {
	switch {
	case p < 0:
		return explorationgraph.PriorityLow
	case p == 1:
		return explorationgraph.PriorityHigh
	case p >= 2:
		return explorationgraph.PriorityCritical
	default:
		return explorationgraph.PriorityMedium
	}
}

func ptrState(s explorationgraph.State) *explorationgraph.State { return &s }

var mustJSON = explorationgraph.MustJSON

type errInvalidPayload string

func (e errInvalidPayload) Error() string { return string(e) }
