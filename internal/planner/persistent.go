// Package planner 提供从 execution_plan 表读取 Move 的实现。
// 这是事件驱动架构的关键：Planner Agent 异步写入 execution_plan，
// Executor 从表中读取待执行的 Move，形成生产者-消费者模式。
package planner

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/executionplan"
)

// PersistentPlanner 从 execution_plan 表读取待执行的 Move（由 Planner Agent 产出）
type PersistentPlanner struct {
	planStore *executionplan.Store
}

// NewPersistentPlanner 创建基于持久化的 Planner
func NewPersistentPlanner(planStore *executionplan.Store) *PersistentPlanner {
	return &PersistentPlanner{planStore: planStore}
}

// Plan 从 execution_plan 表读取待执行的 Move（按优先级排序）
func (p *PersistentPlanner) Plan(ctx context.Context, taskID string) ([]Move, error) {
	if taskID == "" {
		return nil, fmt.Errorf("persistent planner: taskID 为空")
	}

	// 查询 pending 状态的 Move
	pendingMoves, err := p.planStore.ListPending(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("persistent planner: 读取 execution_plan 失败: %w", err)
	}

	// 转换为 planner.Move 格式
	moves := make([]Move, 0, len(pendingMoves))
	for _, pm := range pendingMoves {
		moves = append(moves, Move{
			Kind:     convertMoveKind(pm.Kind),
			Target:   pm.TargetRef,
			OnNodeID: "", // execution_plan 不存储 OnNodeID（frontier 概念属于内存规划器）
			Reason:   pm.Reason,
			Priority: pm.Priority,
		})
	}

	return moves, nil
}

// convertMoveKind 转换 execution_plan.MoveKind 到 planner.MoveKind
func convertMoveKind(kind executionplan.MoveKind) MoveKind {
	switch kind {
	case executionplan.MoveEnumerate:
		return MoveEnumerate
	case executionplan.MoveProbe:
		return MoveProbe
	case executionplan.MoveExploit:
		return MoveExploit
	case executionplan.MoveEscalate:
		return MoveEscalate
	case executionplan.MovePersist:
		return MovePersist
	default:
		return MoveEnumerate
	}
}

// HybridPlanner 结合持久化 Planner 和内存 Planner：
// 优先从 execution_plan 读取（Planner Agent 产出），
// 若无待执行 Move，降级到内存 Planner（兜底策略）
type HybridPlanner struct {
	persistent *PersistentPlanner
	fallback   *Planner // 内存规划器（KillChain/LLM Strategy）
}

// NewHybridPlanner 创建混合 Planner
func NewHybridPlanner(planStore *executionplan.Store, world worldReader, strategy Strategy) *HybridPlanner {
	return &HybridPlanner{
		persistent: NewPersistentPlanner(planStore),
		fallback:   New(world, strategy),
	}
}

// Plan 优先从 execution_plan 读取，若为空则使用内存规划器兜底
func (h *HybridPlanner) Plan(ctx context.Context, taskID string) ([]Move, error) {
	// 优先从持久化层读取
	moves, err := h.persistent.Plan(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("hybrid planner: persistent 读取失败: %w", err)
	}

	// 若有待执行 Move，直接返回
	if len(moves) > 0 {
		return moves, nil
	}

	// 降级到内存规划器（兜底，确保 Planner Agent 未就绪时系统仍可运行）
	moves, err = h.fallback.Plan(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("hybrid planner: fallback 规划失败: %w", err)
	}

	return moves, nil
}
