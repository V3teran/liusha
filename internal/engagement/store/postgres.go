// Package store 实现 engagement 聚合根的持久化，目前只提供 Postgres 实现。
// 统一错误语义 (ErrNotFound / ErrVersionMismatch) 以便上层无需感知 pgx。
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/engagement"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound: 查询的 engagement 不存在。
	ErrNotFound = errors.New("engagement: not found")
	// ErrVersionMismatch: 乐观锁失败（或目标行不存在）。
	// 调用方通常无需区分两者——两种情况都意味着本次更新无效。
	ErrVersionMismatch = errors.New("engagement: version mismatch (optimistic lock)")
)

// Postgres 使用 pgxpool 实现 engagement 存储。
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres 构造 Postgres 存储；连接池由调用方管理生命周期。
func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

// Create 插入新的 engagement 记录。scope / meta 序列化为 jsonb。
// 为允许空 objective 写成 NULL（数据库允许），通过 nullableString 做转换。
func (s *Postgres) Create(ctx context.Context, e engagement.Engagement) error {
	scope, err := json.Marshal(e.Scope)
	if err != nil {
		return fmt.Errorf("marshal scope: %w", err)
	}
	var meta []byte
	if e.Meta != nil {
		if meta, err = json.Marshal(e.Meta); err != nil {
			return fmt.Errorf("marshal meta: %w", err)
		}
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO engagement
			(id, tenant_id, mode, scope, status, objective, version, started_at, updated_at, meta)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, e.ID, e.TenantID, string(e.Mode), scope, string(e.Status),
		nullableString(e.Objective), e.Version, e.StartedAt, e.UpdatedAt, meta)
	return err
}

// Get 按主键查询；不存在时返回 ErrNotFound。
func (s *Postgres) Get(ctx context.Context, id uuid.UUID) (engagement.Engagement, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, tenant_id, mode, scope, status, COALESCE(objective,''),
		       version, started_at, updated_at, ended_at, meta
		FROM engagement WHERE id = $1
	`, id)
	return scanEngagement(row)
}

// ListByTenant 按租户列出 engagement，按 started_at 倒序，使用 limit 约束结果。
func (s *Postgres) ListByTenant(ctx context.Context, tenantID uuid.UUID, limit int) ([]engagement.Engagement, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, mode, scope, status, COALESCE(objective,''),
		       version, started_at, updated_at, ended_at, meta
		FROM engagement
		WHERE tenant_id = $1
		ORDER BY started_at DESC
		LIMIT $2
	`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]engagement.Engagement, 0)
	for rows.Next() {
		e, err := scanEngagement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Archive 以乐观锁方式将 engagement 归档：只有当前 version 匹配才更新。
// 影响行数为 0 时视为版本冲突（也涵盖 id 不存在的情况，语义上一致）。
func (s *Postgres) Archive(ctx context.Context, id uuid.UUID, expectedVersion int64) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE engagement
		SET status = 'archived', version = version + 1, updated_at = now(), ended_at = now()
		WHERE id = $1 AND version = $2
	`, id, expectedVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrVersionMismatch
	}
	return nil
}

// scanEngagement 把一行扫描成 Engagement。pgx.Row 接口由 QueryRow 与 Rows 共用。
func scanEngagement(row pgx.Row) (engagement.Engagement, error) {
	var (
		e            engagement.Engagement
		mode, status string
		scope, meta  []byte
		endedAt      *time.Time
	)
	err := row.Scan(&e.ID, &e.TenantID, &mode, &scope, &status, &e.Objective,
		&e.Version, &e.StartedAt, &e.UpdatedAt, &endedAt, &meta)
	if errors.Is(err, pgx.ErrNoRows) {
		return engagement.Engagement{}, ErrNotFound
	}
	if err != nil {
		return engagement.Engagement{}, err
	}
	e.Mode = engagement.Mode(mode)
	e.Status = engagement.Status(status)
	e.EndedAt = endedAt
	if err := json.Unmarshal(scope, &e.Scope); err != nil {
		return engagement.Engagement{}, fmt.Errorf("unmarshal scope: %w", err)
	}
	if len(meta) > 0 {
		if err := json.Unmarshal(meta, &e.Meta); err != nil {
			return engagement.Engagement{}, fmt.Errorf("unmarshal meta: %w", err)
		}
	}
	return e, nil
}

// nullableString 把空字符串映射为 SQL NULL，避免写入 "" 污染可空列语义。
func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
