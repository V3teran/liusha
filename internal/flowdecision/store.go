package flowdecision

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 flow_decision 表的所有持久化操作。
type Store struct{ pool *pgxpool.Pool }

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Append 写入一条 flow_decision，返回带 ID/CreatedAt 的副本。
//
// jsonb 字段空 raw 自动落空数组 '[]'（与 SQL DEFAULT 一致），避免 NOT NULL 冲突。
// 失败处理由调用方决定（埋点场景应 best-effort 不阻塞主流程）。
func (s *Store) Append(ctx context.Context, d Decision) (Decision, error) {
	d.AttackSurfaces = defaultJSONArray(d.AttackSurfaces)
	d.CredentialLocations = defaultJSONArray(d.CredentialLocations)
	d.RequiredSkills = defaultJSONArray(d.RequiredSkills)

	row := s.pool.QueryRow(ctx, `
		INSERT INTO flow_decision
			(engagement_id, flow_id, task_id, operation, resource_scope,
			 attack_surfaces, carries_auth, credential_locations,
			 required_skills, reasoning)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, created_at`,
		d.EngagementID, d.FlowID, d.TaskID, d.Operation, d.ResourceScope,
		d.AttackSurfaces, d.CarriesAuth, d.CredentialLocations,
		d.RequiredSkills, d.Reasoning)
	if err := row.Scan(&d.ID, &d.CreatedAt); err != nil {
		return Decision{}, fmt.Errorf("insert flow_decision: %w", err)
	}
	return d, nil
}

// ListByEngagement 按 engagement_id 拉所有 decision，按 created_at 升序。
// 主要给报告 / 调试用；当前 ReAct 运行时不读它（写完即归档）。
func (s *Store) ListByEngagement(ctx context.Context, engagementID string) ([]Decision, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, engagement_id, flow_id, task_id, operation, resource_scope,
		       attack_surfaces, carries_auth, credential_locations,
		       required_skills, reasoning, created_at
		FROM flow_decision
		WHERE engagement_id=$1
		ORDER BY created_at ASC`, engagementID)
	if err != nil {
		return nil, fmt.Errorf("list flow_decision: %w", err)
	}
	defer rows.Close()

	var out []Decision
	for rows.Next() {
		var d Decision
		if err := rows.Scan(&d.ID, &d.EngagementID, &d.FlowID, &d.TaskID,
			&d.Operation, &d.ResourceScope, &d.AttackSurfaces, &d.CarriesAuth,
			&d.CredentialLocations, &d.RequiredSkills, &d.Reasoning,
			&d.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan flow_decision: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// defaultJSONArray 把 nil/空 raw 替换成 '[]'，否则透传原值。
// pgx 对 nil json.RawMessage 会写 NULL，与 jsonb NOT NULL 列冲突。
func defaultJSONArray(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("[]")
	}
	return raw
}
