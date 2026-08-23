package cognition

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/executionplan"
	"github.com/V3teran/liusha/internal/planner"
)

// ExecutionLoop 基于 execution_plan 的异步执行循环。
// 与传统的 Loop 不同，ExecutionLoop 从 execution_plan 表持续读取待执行 Move，
// 执行完成后更新状态并发布事件，触发 Planner Agent 重新规划。
type ExecutionLoop struct {
	planStore *executionplan.Store
	executor  Executor
	promoter  Promoter
	eventBus  *EventBus
	logger    zerolog.Logger

	pollInterval time.Duration // 轮询 execution_plan 的间隔
	maxSteps     int           // 最大执行步数
}

// NewExecutionLoop 创建基于 execution_plan 的执行循环
func NewExecutionLoop(
	planStore *executionplan.Store,
	executor Executor,
	promoter Promoter,
	eventBus *EventBus,
	logger zerolog.Logger,
) *ExecutionLoop {
	return &ExecutionLoop{
		planStore:    planStore,
		executor:     executor,
		promoter:     promoter,
		eventBus:     eventBus,
		logger:       logger,
		pollInterval: 2 * time.Second, // 默认 2 秒轮询一次
		maxSteps:     50,
	}
}

// Run 运行执行循环：持续从 execution_plan 读取 Move 并执行
func (l *ExecutionLoop) Run(ctx context.Context, taskID string) (Report, error) {
	if taskID == "" {
		return Report{}, fmt.Errorf("execution loop: taskID 为空")
	}

	var rep Report
	ticker := time.NewTicker(l.pollInterval)
	defer ticker.Stop()

	// 初始检查一次
	if err := l.processPendingMoves(ctx, taskID, &rep); err != nil {
		return rep, err
	}

	for {
		select {
		case <-ctx.Done():
			rep.StopWhy = stopCanceled
			return rep, ctx.Err()

		case <-ticker.C:
			// 定期检查是否有新的待执行 Move
			if err := l.processPendingMoves(ctx, taskID, &rep); err != nil {
				return rep, err
			}

			// 检查是否达到最大步数
			if rep.Steps >= l.maxSteps {
				rep.StopWhy = stopMaxSteps
				return rep, nil
			}

			// 检查是否无待执行 Move 且已运行过至少一轮
			if rep.Steps > 0 {
				pendingCount, err := l.countPendingMoves(ctx, taskID)
				if err != nil {
					l.logger.Warn().Err(err).Msg("count pending moves failed")
					continue
				}
				if pendingCount == 0 {
					// 再等待一个周期，确保 Planner Agent 有机会产出新 Move
					time.Sleep(l.pollInterval)
					pendingCount, _ = l.countPendingMoves(ctx, taskID)
					if pendingCount == 0 {
						rep.StopWhy = stopExhausted
						return rep, nil
					}
				}
			}
		}
	}
}

// processPendingMoves 处理所有待执行的 Move
func (l *ExecutionLoop) processPendingMoves(ctx context.Context, taskID string, rep *Report) error {
	pendingMoves, err := l.planStore.ListPending(ctx, taskID)
	if err != nil {
		return fmt.Errorf("list pending moves: %w", err)
	}

	for _, pm := range pendingMoves {
		if err := ctx.Err(); err != nil {
			return err
		}

		// 标记为执行中
		if err := l.planStore.MarkExecuting(ctx, pm.ID); err != nil {
			l.logger.Error().Err(err).Str("move_id", pm.ID.String()).Msg("mark executing failed")
			continue
		}

		// 转换为 planner.Move
		move := planner.Move{
			Kind:     convertToPlannerMoveKind(pm.Kind),
			Target:   pm.TargetRef,
			Reason:   pm.Reason,
			Priority: pm.Priority,
		}

		// 执行 Move
		execErr := l.executeMove(ctx, taskID, pm.ID, move, rep)

		// 更新状态
		if execErr != nil {
			if err := l.planStore.MarkFailed(ctx, pm.ID, execErr.Error()); err != nil {
				l.logger.Error().Err(err).Str("move_id", pm.ID.String()).Msg("mark failed failed")
			}
		} else {
			if err := l.planStore.MarkCompleted(ctx, pm.ID); err != nil {
				l.logger.Error().Err(err).Str("move_id", pm.ID.String()).Msg("mark completed failed")
			}

			// 发布 Move 完成事件
			if l.eventBus != nil {
				l.eventBus.PublishMoveCompleted(taskID, pm.ID)
			}
		}

		rep.Steps++
	}

	return nil
}

// executeMove 执行单个 Move
func (l *ExecutionLoop) executeMove(
	ctx context.Context,
	taskID string,
	moveID uuid.UUID,
	move planner.Move,
	rep *Report,
) error {
	l.logger.Info().
		Str("task_id", taskID).
		Str("move_id", moveID.String()).
		Str("kind", string(move.Kind)).
		Str("target", move.Target.Locator).
		Msg("executing move")

	// Executor 执行 Move，产出 Attempt
	attempts, err := l.executor.Execute(ctx, move)
	if err != nil {
		l.logger.Error().Err(err).Str("move_id", moveID.String()).Msg("executor failed")
		return err
	}

	// Verifier 验证每个 Attempt
	for _, a := range attempts {
		rep.Attempts++
		node, err := l.promoter.Promote(ctx, a)
		if err != nil {
			l.logger.Error().Err(err).Msg("promote failed")
			return fmt.Errorf("promote failed: %w", err)
		}

		if node != nil {
			rep.Promoted++

			// 发布验证通过事件
			if l.eventBus != nil && node.TaskID != "" {
				if nodeID, parseErr := uuid.Parse(node.ID); parseErr == nil {
					l.eventBus.PublishVerificationPassed(node.TaskID, nodeID)
				}
			}
		}
	}

	return nil
}

// countPendingMoves 统计待执行 Move 数量
func (l *ExecutionLoop) countPendingMoves(ctx context.Context, taskID string) (int, error) {
	moves, err := l.planStore.ListPending(ctx, taskID)
	if err != nil {
		return 0, err
	}
	return len(moves), nil
}

// convertToPlannerMoveKind 转换 executionplan.MoveKind 到 planner.MoveKind
func convertToPlannerMoveKind(kind executionplan.MoveKind) planner.MoveKind {
	switch kind {
	case executionplan.MoveEnumerate:
		return planner.MoveEnumerate
	case executionplan.MoveProbe:
		return planner.MoveProbe
	case executionplan.MoveExploit:
		return planner.MoveExploit
	case executionplan.MoveEscalate:
		return planner.MoveEscalate
	case executionplan.MovePersist:
		return planner.MovePersist
	default:
		return planner.MoveEnumerate
	}
}
