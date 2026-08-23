package planner

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/worldmodel"
)

// worldReader 收窄为只读：Planner 永不写图，写由 Verifier 晋升门独占。
type worldReader interface {
	ListNodes(ctx context.Context, taskID string) ([]worldmodel.Node, error)
	ListEdges(ctx context.Context, taskID string) ([]worldmodel.Edge, error)
	ListMoves(ctx context.Context, taskID string) ([]worldmodel.Move, error)
}

type Planner struct {
	world    worldReader
	strategy Strategy
}

// New 的 strategy 为 nil 时用默认杀伤链启发式。
func New(world worldReader, strategy Strategy) *Planner {
	if strategy == nil {
		strategy = NewKillChainStrategy()
	}
	return &Planner{world: world, strategy: strategy}
}

// Plan 返回空切片表示 frontier 耗尽，由上层决定是否收尾。
func (p *Planner) Plan(ctx context.Context, taskID string) ([]Move, error) {
	if taskID == "" {
		return nil, fmt.Errorf("planner: taskID 为空")
	}
	nodes, err := p.world.ListNodes(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("planner: 读节点失败: %w", err)
	}
	edges, err := p.world.ListEdges(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("planner: 读边失败: %w", err)
	}

	// 读取历史 Move 记录（供 LLMStrategy 使用）
	moveRecords, _ := p.world.ListMoves(ctx, taskID)

	// 推导 Frontier
	frontier := deriveFrontier(nodes, edges)

	// 调用 Strategy 排序（传入上下文信息）
	ranked, err := p.strategy.Rank(ctx, RankRequest{
		Frontier:    frontier,
		TopK:        []worldmodel.Node{}, // TODO: 实现 TopK 节点选取
		MoveRecords: moveRecords,
		State:       map[string]interface{}{},
	})
	if err != nil {
		return nil, fmt.Errorf("planner: strategy.Rank 失败: %w", err)
	}

	return ranked, nil
}
