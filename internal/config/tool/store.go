package tool

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 tool 目录表的持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT 路径的统一列序，与 scan() 字段一一对应。
const colsSelect = "name, kind, category, description, sort_order, synced_at"

// buildFilter 拼装 List/Count 共用的 WHERE 子句与参数（DRY，防两处漂移）。
// 返回的 SQL 以 " WHERE ..." 开头或为空串；参数按占位符顺序排列。
func buildFilter(p ListParams) (string, []any) {
	var conds []string
	var args []any
	if p.Kind != "" {
		args = append(args, string(p.Kind))
		conds = append(conds, fmt.Sprintf("kind=$%d", len(args)))
	}
	if q := strings.TrimSpace(p.Q); q != "" {
		args = append(args, "%"+q+"%")
		conds = append(conds, fmt.Sprintf("(name ILIKE $%d OR description ILIKE $%d)", len(args), len(args)))
	}
	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// List 按 (kind, sort_order, name) 稳定序列出工具，支持模糊/体系过滤与分页。
// Limit<=0 时不分页（返回过滤后全量）。
func (s *Store) List(ctx context.Context, p ListParams) ([]Tool, error) {
	where, args := buildFilter(p)
	q := "SELECT " + colsSelect + " FROM tool" + where + " ORDER BY kind ASC, sort_order ASC, name ASC"
	if p.Limit > 0 {
		args = append(args, p.Limit)
		q += fmt.Sprintf(" LIMIT $%d", len(args))
		args = append(args, p.Offset)
		q += fmt.Sprintf(" OFFSET $%d", len(args))
	}
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	defer rows.Close()

	var out []Tool
	for rows.Next() {
		var t Tool
		if err := scan(rows, &t); err != nil {
			return nil, fmt.Errorf("scan tool: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Count 返回与 List 相同过滤条件下的总行数（分页 total）。
func (s *Store) Count(ctx context.Context, p ListParams) (int, error) {
	where, args := buildFilter(p)
	var n int
	if err := s.pool.QueryRow(ctx, "SELECT COUNT(*) FROM tool"+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count tools: %w", err)
	}
	return n, nil
}

// Get 按 name 读取单个工具。
func (s *Store) Get(ctx context.Context, name string) (Tool, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+colsSelect+" FROM tool WHERE name=$1", name)
	var t Tool
	if err := scan(row, &t); err != nil {
		return Tool{}, fmt.Errorf("get tool %q: %w", name, err)
	}
	return t, nil
}

// Upsert 幂等写入一条工具目录（reconcile 用）；按 name 冲突则覆盖全部字段并刷新 synced_at。
func (s *Store) Upsert(ctx context.Context, t Tool) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO tool (name, kind, category, description, sort_order, synced_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (name) DO UPDATE SET
			kind=EXCLUDED.kind, category=EXCLUDED.category, description=EXCLUDED.description,
			sort_order=EXCLUDED.sort_order, synced_at=now()`,
		t.Name, string(t.Kind), t.Category, t.Description, t.SortOrder)
	if err != nil {
		return fmt.Errorf("upsert tool %q: %w", t.Name, err)
	}
	return nil
}

// PruneExceptKind 删除某一体系(kind)下 name 不在 keep 集合内的工具（reconcile 清理下线工具）。
// 按 kind 限定作用域：tools.yaml 加载失败(manifest nil)时只同步 function，绝不误删既有 cli 目录。
// keep 为空视为「本体系无有效目录」，为防误删该体系全部行，直接返回不删。
func (s *Store) PruneExceptKind(ctx context.Context, kind Kind, keep []string) (int, error) {
	if len(keep) == 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, "DELETE FROM tool WHERE kind=$1 AND name <> ALL($2)", string(kind), keep)
	if err != nil {
		return 0, fmt.Errorf("prune %s tools: %w", kind, err)
	}
	return int(tag.RowsAffected()), nil
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scan 是 colsSelect 列序的统一反序列化点。
func scan(r scanner, t *Tool) error {
	var kind string
	if err := r.Scan(&t.Name, &kind, &t.Category, &t.Description,
		&t.SortOrder, &t.SyncedAt); err != nil {
		return err
	}
	t.Kind = Kind(kind)
	return nil
}
