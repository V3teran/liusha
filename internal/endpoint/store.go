package endpoint

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 endpoint 表的所有持久化操作。
type Store struct{ pool *pgxpool.Pool }

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT / RETURNING 路径的统一列序（0058 加 name 后 7 字段）。
const colsSelect = "id, owner_id, host, method, path, name, discovered_at"

// Upsert 写入 endpoint：
//   - 新 (owner_id, host, method, path) → INSERT
//   - 已存在 → 保留原行（discovered_at 不变）
//
// caller 必填：OwnerID / Host / Method / Path。Method 自动 ToUpper，Path 由 caller 模板化。
//
// 设计反思（0057）：删 status 字段后，重复 write_endpoint 无副作用；
// ON CONFLICT DO UPDATE owner_id=owner_id 是 no-op，仅为让 RETURNING 始终返当前行。
func (s *Store) Upsert(ctx context.Context, e Endpoint) (Endpoint, error) {
	if e.OwnerID == "" {
		return Endpoint{}, fmt.Errorf("endpoint.Upsert: OwnerID 必填")
	}
	if e.Host == "" {
		return Endpoint{}, fmt.Errorf("endpoint.Upsert: Host 必填")
	}
	if e.Method == "" {
		return Endpoint{}, fmt.Errorf("endpoint.Upsert: Method 必填")
	}
	if e.Path == "" {
		return Endpoint{}, fmt.Errorf("endpoint.Upsert: Path 必填")
	}
	method := strings.ToUpper(strings.TrimSpace(e.Method))
	name := strings.TrimSpace(e.Name) // 可空

	// 已存在时更新 name（commander 后续 recon 可能补充名字），其他字段不变
	row := s.pool.QueryRow(ctx, `
		INSERT INTO endpoint (owner_id, host, method, path, name)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''))
		ON CONFLICT (owner_id, host, method, path) DO UPDATE
		  SET name = COALESCE(EXCLUDED.name, endpoint.name)  -- 新 name 优先，无则保留旧
		RETURNING `+colsSelect,
		e.OwnerID, e.Host, method, e.Path, name)

	var saved Endpoint
	if err := scan(row, &saved); err != nil {
		return Endpoint{}, fmt.Errorf("upsert endpoint: %w", err)
	}
	return saved, nil
}

// ListByOwner 拉 (owner_id, host) 范围的全部 endpoint，按 discovered_at 升序。
//
// host 为空时返回本 owner 全部 host 的 endpoint（graphview 跨 host 视图用）。
func (s *Store) ListByOwner(ctx context.Context, ownerID, host string) ([]Endpoint, error) {
	if ownerID == "" {
		return nil, fmt.Errorf("endpoint.ListByOwner: ownerID 必填")
	}
	q := `SELECT ` + colsSelect + ` FROM endpoint WHERE owner_id = $1`
	args := []any{ownerID}
	if host != "" {
		q += ` AND host = $2`
		args = append(args, host)
	}
	q += ` ORDER BY discovered_at ASC`

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list endpoints by owner: %w", err)
	}
	defer rows.Close()

	var out []Endpoint
	for rows.Next() {
		var e Endpoint
		if err := scan(rows, &e); err != nil {
			return nil, fmt.Errorf("scan endpoint: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate endpoints: %w", err)
	}
	return out, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scan(r scanner, e *Endpoint) error {
	var name *string // nullable
	if err := r.Scan(
		&e.ID, &e.OwnerID, &e.Host, &e.Method, &e.Path, &name, &e.DiscoveredAt,
	); err != nil {
		return err
	}
	if name != nil {
		e.Name = *name
	}
	return nil
}
