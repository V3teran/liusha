package llmcall

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 llm_call 表的所有持久化操作。
type Store struct{ pool *pgxpool.Pool }

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Append 插入一条 LLM 调用记录，返回新 id。
// TaskID / EngagementID 为 nil 时落 NULL（外键 ON DELETE SET NULL）。
// Role 为空时按 schema 默认 ”；写入时显式传入便于 CountByRole 聚合。
func (s *Store) Append(ctx context.Context, c Call) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO llm_call
			(task_id, engagement_id, provider, model, in_tokens, out_tokens, cached_tokens,
			 cost_usd, latency_ms, finish_reason, error, role)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id`,
		c.TaskID, c.EngagementID, c.Provider, c.Model,
		c.InTokens, c.OutTokens, c.CachedTokens,
		c.CostUSD, c.LatencyMs, c.FinishReason, c.Error, c.Role,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("insert llm_call: %w", err)
	}
	return id, nil
}

// SumCostByEngagement 返回指定 engagement 下所有 LLM 调用的成本总和（美元）。
// 无任何记录时返回 0（COALESCE 兜底）。
// numeric(12,6) 经 ::float8 转型供 Go float64 直接 Scan。
func (s *Store) SumCostByEngagement(ctx context.Context, engagementID string) (float64, error) {
	var v float64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(cost_usd), 0)::float8
		FROM llm_call
		WHERE engagement_id=$1`, engagementID).Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("sum llm_call cost: %w", err)
	}
	return v, nil
}

// CountByRole 按 role 维度聚合 engagement 下的调用次数，便于验证多模型路由生效
// （黑客松借鉴：T21 RouteKey，T9.5 e2e 会断言 react.main / observer 各自有调用）。
func (s *Store) CountByRole(ctx context.Context, engagementID string) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT role, count(*)
		FROM llm_call
		WHERE engagement_id=$1
		GROUP BY role`, engagementID)
	if err != nil {
		return nil, fmt.Errorf("count llm_call by role: %w", err)
	}
	defer rows.Close()

	out := make(map[string]int)
	for rows.Next() {
		var role string
		var n int
		if err := rows.Scan(&role, &n); err != nil {
			return nil, fmt.Errorf("scan role count: %w", err)
		}
		out[role] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate role counts: %w", err)
	}
	return out, nil
}
