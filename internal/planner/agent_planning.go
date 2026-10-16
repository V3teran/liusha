package planner

import (
	"context"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

func (a *Agent) planActions(ctx context.Context) error {
	a.logger.Debug().
		Str("task_id", a.taskID).
		Str("caller", "planActions").
		Msg("▶ planActions 入口")

	defer a.logger.Debug().
		Str("task_id", a.taskID).
		Str("caller", "planActions").
		Msg("◀ planActions 出口")

	a.logger.Debug().Str("task_id", a.taskID).Msg("开始规划 Action")

	// 1. 分析新出现的 Results（失败仅记录：分析不是规划的前置硬依赖）
	if err := a.analyzeNewResults(ctx); err != nil {
		a.logger.Warn().Err(err).Msg("分析新 Results 失败（继续规划）")
	}

	// 2. 检查是否已有可执行的 Action
	hasExecutable, err := a.checkExecutableActions(ctx)
	if err != nil {
		return err
	}
	if hasExecutable {
		return nil
	}

	// 3. 生成新的 Actions
	actions, err := a.generateNewActions(ctx)
	if err != nil {
		return err
	}

	// 4. 如果没有生成新 Action，发布收敛事件
	if len(actions) == 0 {
		a.handleNoActions(ctx)
		return nil
	}

	// 5. 将新 Action 写入探索图
	return a.writeActionsFromPlan(ctx, actions)
}

// rootObjective 返回任务的根目标节点（创建序最早——根目标由任务入口创建，
// planner 派生的次级目标晚于它）。规划产出的 Action 以根目标为章程归属。
func (a *Agent) rootObjective(ctx context.Context) (explorationgraph.Node, error) {
	objectives, err := a.graph.ListNodesByKind(ctx, a.taskID, core.KindObjective)
	if err != nil {
		return explorationgraph.Node{}, fmt.Errorf("获取 Objectives 失败: %w", err)
	}
	if len(objectives) == 0 {
		return explorationgraph.Node{}, fmt.Errorf("当前无 Objective 节点")
	}
	return objectives[0], nil
}

// writeActionsFromPlan 将 planner 返回的 action 节点连同章程归属边
// （根 objective → action）原子写入图。
func (a *Agent) writeActionsFromPlan(ctx context.Context, actions []explorationgraph.Node) error {
	a.logger.Info().
		Int("action_count", len(actions)).
		Msg("准备写入 Actions 到探索图")

	primaryObjective, err := a.rootObjective(ctx)
	if err != nil {
		a.logger.Error().Err(err).Msg("获取根 Objective 失败")
	}

	failed := 0
	for i, action := range actions {
		a.logger.Info().
			Int("index", i).
			Str("action_id", action.ID).
			Msg("准备创建 Action")

		if primaryObjective.ID != "" {
			edge := explorationgraph.Edge{
				TaskID:    a.taskID,
				SrcID:     primaryObjective.ID,
				Rel:       explorationgraph.RelGenerates,
				DstID:     action.ID,
				CreatedAt: time.Now(),
			}
			if err := a.graph.CreateNodeWithEdges(ctx, action, []explorationgraph.Edge{edge}); err != nil {
				failed++
				a.logger.Error().
					Err(err).
					Str("action_id", action.ID).
					Msg("写入 Action 失败")
				continue
			}
		} else {
			if _, err := a.graph.CreateNode(ctx, action); err != nil {
				failed++
				a.logger.Error().
					Err(err).
					Str("action_id", action.ID).
					Msg("写入 Action 失败")
				continue
			}
		}

		a.logger.Info().
			Str("action_id", action.ID).
			Str("priority", string(action.Priority)).
			Msg("新 Action 已生成")

		// 发布 ActionProposed 事件
		a.eventBus.PublishActionProposed(a.taskID, action.ID)
	}

	if failed > 0 {
		return fmt.Errorf("写入 Action 失败 %d/%d 条（详见日志）", failed, len(actions))
	}
	return nil
}

// analyzeAndProcessResults 分析增量 Results 并处理（水位由调用方维护）。
