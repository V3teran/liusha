package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ExplorationGraphClient 通过数据库直接查询探索图统计
type ExplorationGraphClient struct {
	pool *pgxpool.Pool
}

// NewExplorationGraphClient 创建客户端
func NewExplorationGraphClient(pool *pgxpool.Pool) *ExplorationGraphClient {
	return &ExplorationGraphClient{
		pool: pool,
	}
}

// GetTaskStats 查询任务的探索图节点统计
// 直接查询 exploration_node 表（working memory = 探索图）
func (c *ExplorationGraphClient) GetTaskStats(ctx context.Context, taskID string) (GraphStats, error) {
	query := `
		SELECT
			kind,
			COUNT(*) as count
		FROM exploration_node
		WHERE task_id = $1
		GROUP BY kind
	`

	rows, err := c.pool.Query(ctx, query, taskID)
	if err != nil {
		return GraphStats{}, fmt.Errorf("query exploration_node: %w", err)
	}
	defer rows.Close()

	stats := GraphStats{}
	for rows.Next() {
		var kind string
		var count int
		if err := rows.Scan(&kind, &count); err != nil {
			return GraphStats{}, fmt.Errorf("scan row: %w", err)
		}

		switch kind {
		case "objective":
			stats.Objectives = count
		case "action":
			stats.Actions = count
		case "observation":
			stats.Observations = count
		case "result":
			stats.Results = count
		}
	}

	if err := rows.Err(); err != nil {
		return GraphStats{}, fmt.Errorf("rows error: %w", err)
	}

	return stats, nil
}
