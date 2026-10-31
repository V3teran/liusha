package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// analyzeAndProcessResults 分析增量 Results 并处理（水位由调用方维护）。
func (a *Agent) analyzeAndProcessResults(ctx context.Context, results []explorationgraph.Node) error {
	a.logger.Info().Int("result_count", len(results)).Msg("开始分析 Results")

	if len(results) == 0 {
		return nil
	}

	// 2. 调用 Planner 分析 Results
	analysis, err := a.planner.AnalyzeResults(ctx, a.graph, a.taskID, results)
	if err != nil {
		return fmt.Errorf("analyze results: %w", err)
	}

	a.logger.Info().
		Bool("completed", analysis.Completed).
		Int("new_objectives", len(analysis.NewObjectives)).
		Int("continuation_actions", len(analysis.ContinuationActions)).
		Str("reasoning", analysis.Reasoning).
		Msg("Result 分析完成")

	// 3. 处理分析结果

	// 情况 A：当前 Objective 已完成
	if analysis.Completed {
		a.logger.Info().
			Int("evidence_count", len(analysis.Evidence)).
			Msg("当前 Objective 已完成")

		// 如果有新 Objectives，创建它们
		if len(analysis.NewObjectives) > 0 {
			return a.createNewObjectives(ctx, analysis.NewObjectives)
		}

		a.logger.Info().Msg("当前 Objective 完成，但没有新方向")
		return nil
	}

	// 情况 B：生成 ContinuationActions（在当前 Objective 下继续）
	if len(analysis.ContinuationActions) > 0 {
		a.logger.Info().Msg("在当前 Objective 下生成新 Actions")
		return a.createContinuationActions(ctx, analysis.ContinuationActions)
	}

	// 情况 C：生成新 Objectives（新方向）
	if len(analysis.NewObjectives) > 0 {
		a.logger.Info().Msg("发现新探索方向，生成新 Objectives")
		return a.createNewObjectives(ctx, analysis.NewObjectives)
	}

	// 情况 D：什么都不做
	a.logger.Info().Msg("分析完成，无需生成新节点")
	return nil
}

// createNewObjectives 创建新的 Objective 节点，连同 Result → Objective 的
// TRIGGERS 溯源边原子落图。
func (a *Agent) createNewObjectives(ctx context.Context, objectives []NewObjective) error {
	for _, obj := range objectives {
		objID := uuid.New().String()

		content := map[string]interface{}{
			"description": obj.Description,
			"reasoning":   obj.Reasoning,
		}
		contentJSON, err := json.Marshal(content)
		if err != nil {
			a.logger.Error().Err(err).Msg("序列化 Objective 内容失败")
			continue
		}

		// objective 类型节点必须有非空 state（DB 约束 ck_state_by_kind；
		// 根目标创建处同理）。漏设会让派生目标在 PG 后端被约束拒绝、静默丢弃。
		openState := explorationgraph.StateOpen
		node := explorationgraph.Node{
			ID:         objID,
			TaskID:     a.taskID,
			Kind:       core.KindObjective,
			Content:    contentJSON,
			Priority:   obj.Priority,
			State:      &openState,
			SourceType: explorationgraph.SourcePlanner,
			SourceID:   "result-analyzer",
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}

		edges := make([]explorationgraph.Edge, 0, len(obj.TriggeredBy))
		for _, resultID := range obj.TriggeredBy {
			edges = append(edges, explorationgraph.Edge{
				TaskID:    a.taskID,
				SrcID:     resultID,
				Rel:       explorationgraph.RelTriggers,
				DstID:     objID,
				CreatedAt: time.Now(),
			})
		}

		if err := a.graph.CreateNodeWithEdges(ctx, node, edges); err != nil {
			a.logger.Error().
				Err(err).
				Str("objective_id", objID).
				Msg("创建 Objective 节点失败")
			continue
		}

		a.logger.Info().
			Str("objective_id", objID).
			Str("description", obj.Description).
			Msg("新 Objective 已创建")
	}

	return nil
}

// createContinuationActions 在根 Objective 下创建延续 Action 节点，
// 目标归属边（objective → action）与触发溯源边（result → action）一并原子落图。
func (a *Agent) createContinuationActions(ctx context.Context, actions []ContinuationAction) error {
	currentObjective, err := a.rootObjective(ctx)
	if err != nil {
		return err
	}

	for _, action := range actions {
		actionID := uuid.New().String()

		content := map[string]interface{}{
			"instruction": action.Instruction,
			"reasoning":   action.Reasoning,
		}
		contentJSON, err := json.Marshal(content)
		if err != nil {
			a.logger.Error().Err(err).Msg("序列化 Action 内容失败")
			continue
		}

		openState := explorationgraph.StateOpen
		node := explorationgraph.Node{
			ID:         actionID,
			TaskID:     a.taskID,
			Kind:       core.KindAction,
			Content:    contentJSON,
			Priority:   action.Priority,
			State:      &openState,
			SourceType: explorationgraph.SourcePlanner,
			SourceID:   "result-analyzer",
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}

		edges := []explorationgraph.Edge{{
			TaskID:    a.taskID,
			SrcID:     currentObjective.ID,
			Rel:       explorationgraph.RelGenerates,
			DstID:     actionID,
			CreatedAt: time.Now(),
		}}
		for _, resultID := range action.TriggeredBy {
			edges = append(edges, explorationgraph.Edge{
				TaskID:    a.taskID,
				SrcID:     resultID,
				Rel:       explorationgraph.RelTriggers,
				DstID:     actionID,
				CreatedAt: time.Now(),
			})
		}

		if err := a.graph.CreateNodeWithEdges(ctx, node, edges); err != nil {
			a.logger.Error().
				Err(err).
				Str("action_id", actionID).
				Msg("创建 Action 节点失败")
			continue
		}

		a.logger.Info().
			Str("action_id", actionID).
			Str("instruction", action.Instruction).
			Msg("新 Action 已创建")

		// 发布 ActionProposed 事件
		a.eventBus.PublishActionProposed(a.taskID, actionID)
	}

	return nil
}

// GraphPlanner 规划接口（由 planner.New 提供）
type GraphPlanner interface {
	Plan(ctx context.Context, graph *explorationgraph.Store, taskID string, functionTools []string) ([]explorationgraph.Node, error)
	AnalyzeResults(ctx context.Context, graph *explorationgraph.Store, taskID string, results []explorationgraph.Node) (*ResultAnalysis, error)
}

// ResultAnalysis 是对 Results 的分析结果
