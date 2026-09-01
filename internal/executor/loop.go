package executor

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/worldmodel"
)

// Loop 基于统一世界模型的执行循环
type Loop struct {
	world    *worldmodel.Store
	executor ExecutorInterface
	promoter Promoter
	eventBus *PlannerEventBus
	logger   zerolog.Logger

	pollInterval time.Duration
	maxSteps     int
}

// NewLoop 创建执行循环
func NewLoop(
	world *worldmodel.Store,
	executor ExecutorInterface,
	promoter Promoter,
	eventBus *PlannerEventBus,
	logger zerolog.Logger,
) *Loop {
	return &Loop{
		world:        world,
		executor:     executor,
		promoter:     promoter,
		eventBus:     eventBus,
		logger:       logger,
		pollInterval: 2 * time.Second,
		maxSteps:     100,
	}
}

// Run 运行执行循环
func (l *Loop) Run(ctx context.Context, taskID string) (Report, error) {
	if taskID == "" {
		return Report{}, fmt.Errorf("execution loop: taskID 为空")
	}

	var rep Report
	ticker := time.NewTicker(l.pollInterval)
	defer ticker.Stop()

	// 初始检查一次
	if err := l.processPendingActions(ctx, taskID, &rep); err != nil {
		return rep, err
	}

	consecutiveEmptyPolls := 0
	maxEmptyPolls := 60  // 2 分钟无进展则退出（Planner 已完成初始规划）

	for {
		select {
		case <-ctx.Done():
			rep.StopWhy = stopCanceled
			return rep, ctx.Err()

		case <-ticker.C:
			if err := l.processPendingActions(ctx, taskID, &rep); err != nil {
				rep.StopWhy = stopError
				return rep, err
			}

			// 停止条件：达到最大步数
			if rep.Steps >= l.maxSteps {
				rep.StopWhy = stopMaxSteps
				return rep, nil
			}

			// 停止条件：无待执行 Action（等待 Planner 创建初始 action）
			pendingCount, err := l.countPendingActions(ctx, taskID)
			if err != nil {
				return rep, err
			}
			if pendingCount == 0 {
				consecutiveEmptyPolls++
				if consecutiveEmptyPolls >= maxEmptyPolls {
					rep.StopWhy = stopNoProgress
					return rep, nil
				}
			} else {
				consecutiveEmptyPolls = 0
			}
		}
	}
}

// processPendingActions 处理所有可执行的 Action（支持依赖调度）
func (l *Loop) processPendingActions(ctx context.Context, taskID string, rep *Report) error {
	// 获取所有 open 状态的 Move
	openActions, err := l.world.ListOpenActions(ctx, taskID)
	if err != nil {
		return fmt.Errorf("list open moves: %w", err)
	}

	if len(openActions) == 0 {
		return nil
	}

	// 获取已完成的 Move ID 集合
	completed, err := l.getCompletedMoveIDs(ctx, taskID)
	if err != nil {
		return fmt.Errorf("get completed moves: %w", err)
	}

	// 筛选出当前可执行的 Move（无依赖或依赖已满足）
	var executable []worldmodel.Node
	for _, m := range openActions {
		if m.CanExecute(completed) {
			executable = append(executable, m)
		}
	}

	if len(executable) == 0 {
		l.logger.Debug().
			Int("open", len(openActions)).
			Msg("有 open Move 但无可执行（等待依赖）")
		return nil
	}

	// 顺序执行可执行的 Move（TODO: 后续支持并行）
	for _, move := range executable {
		if err := ctx.Err(); err != nil {
			return err
		}

		// 标记为 running
		if err := l.world.UpdateActionState(ctx, move.ID, worldmodel.StateRunning, nil); err != nil {
			l.logger.Error().Err(err).Str("move_id", move.ID).Msg("mark running failed")
			continue
		}

		// 执行 Action
		execErr := l.executeMove(ctx, taskID, move, rep)

		// 更新状态
		if execErr != nil {
			errMsg := execErr.Error()
			if err := l.world.UpdateActionState(ctx, move.ID, worldmodel.StateFailed, &errMsg); err != nil {
				l.logger.Error().Err(err).Str("move_id", move.ID).Msg("mark failed failed")
			}
		} else {
			if err := l.world.UpdateActionState(ctx, move.ID, worldmodel.StateDone, nil); err != nil {
				l.logger.Error().Err(err).Str("move_id", move.ID).Msg("mark done failed")
			}

			// 发布 Action 完成事件
			if l.eventBus != nil {
				l.eventBus.PublishActionCompleted(taskID, move.ID)
			}

			// 更新已完成集合
			completed[move.ID] = true
		}

		rep.Steps++
	}

	return nil
}

// executeMove 执行单个 Move
func (l *Loop) executeMove(
	ctx context.Context,
	taskID string,
	move worldmodel.Node,
	rep *Report,
) error {
	l.logger.Info().
		Str("move_id", move.ID).
		Str("kind", string(move.Kind)).
		Int("priority", move.Priority).
		Msg("executing move")

	// Executor 执行 Action
	attempts, err := l.executor.Execute(ctx, move)
	if err != nil {
		l.logger.Error().Err(err).Str("move_id", move.ID).Msg("executor failed")
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

			// 创建溯源边：move → observation/discovery
			edge := worldmodel.Edge{
				TaskID:    taskID,
				SrcID:     move.ID,
				Rel:       worldmodel.RelGenerates,
				DstID:     node.ID,
				CreatedAt: time.Now(),
			}
			if err := l.world.CreateEdge(ctx, edge); err != nil {
				l.logger.Error().Err(err).Msg("create edge failed")
			}

			// 发布验证通过事件
			if l.eventBus != nil && node.TaskID != "" {
				l.eventBus.PublishVerificationPassed(node.TaskID, node.ID)
			}
		}
	}

	return nil
}

// getCompletedMoveIDs 获取已完成的 Move ID 集合
func (l *Loop) getCompletedMoveIDs(ctx context.Context, taskID string) (map[string]bool, error) {
	completedActions, err := l.world.ListCompletedActions(ctx, taskID)
	if err != nil {
		return nil, err
	}

	completed := make(map[string]bool, len(completedActions))
	for _, m := range completedActions {
		completed[m.ID] = true
	}

	return completed, nil
}

// countPendingActions 统计待执行 Action 数量
func (l *Loop) countPendingActions(ctx context.Context, taskID string) (int, error) {
	moves, err := l.world.ListOpenActions(ctx, taskID)
	if err != nil {
		return 0, err
	}
	return len(moves), nil
}
