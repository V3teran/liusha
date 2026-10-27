// control.go 控制平面消费：轮询 task_control_event，把人工干预
// （terminate/pause/resume/adjust_goal/inject_action）翻译成对循环的操作。

package cognition

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/logx"
)

// controlPollInterval 是控制平面事件的轮询周期。
// 与完成检测器的检查节奏（5s）对齐：人工干预的生效延迟上限 ≈ 一个周期。
const controlPollInterval = 5 * time.Second

// startControlConsumer 轮询消费控制平面事件，返回停止函数。
// 每个事件独立处理并回写 processed/failed 状态，单条失败不拖累后续事件。
func startControlConsumer(
	ctx context.Context,
	taskID string,
	store *controlplane.Store,
	graph *explorationgraph.Store,
	detector *CompletionDetector,
	agents *agentLifecycle,
	logger zerolog.Logger,
) (stop func()) {
	logger = logger.With().Str("component", "control_consumer").Str("task_id", taskID).Logger()
	done := make(chan struct{})
	// stopCh 让 stop 函数能主动叫停消费循环。只有 ctx.Done() 一个退出条件
	// 是不够的：认知循环被 terminate 中止后 ctx 依然存活（它归 asynq 任务，
	// 不随 detector 中止取消），stop 函数若只等 goroutine 自然退出就会
	// 永久阻塞——defer 链卡死在第一环，任务终态永远落不了库（e2e 实测）。
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

			events, err := store.ListPending(ctx, taskID)
			if err != nil {
				logger.Warn().Err(err).Msg("拉取控制事件失败（下轮重试）")
				continue
			}

			for _, ev := range events {
				if err := handleControlEvent(ctx, taskID, graph, detector, agents, ev, logger); err != nil {
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
		}
	})

	return func() {
		close(stopCh)
		<-done
	}
}

// handleControlEvent 处理单条控制事件。
func handleControlEvent(
	ctx context.Context,
	taskID string,
	graph *explorationgraph.Store,
	detector *CompletionDetector,
	agents *agentLifecycle,
	ev controlplane.ControlEvent,
	logger zerolog.Logger,
) error {
	switch ev.Command {
	case controlplane.CommandTerminate:
		logger.Info().Msg("收到 terminate，中止任务")
		detector.Abort("control-plane: terminate")
		return nil

	case controlplane.CommandPause:
		logger.Info().Msg("收到 pause，停止 agents 并冻结完成判定")
		agents.Stop()
		detector.Pause()
		return nil

	case controlplane.CommandResume:
		logger.Info().Msg("收到 resume，恢复完成判定并重启 agents")
		detector.Resume()
		agents.Start()
		return nil

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
		node := explorationgraph.Node{
			ID:         uuid.New().String(),
			TaskID:     taskID,
			Kind:       core.KindObjective,
			Content:    mustJSON(map[string]string{"description": newGoal}),
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
// 注入节点标记 SourceUser/SourceID=control-plane，审计可溯源。
func injectAction(ctx context.Context, taskID string, graph *explorationgraph.Store, p controlplane.InjectActionPayload, logger zerolog.Logger) error {
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

	if _, err := graph.CreateNode(ctx, node); err != nil {
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
