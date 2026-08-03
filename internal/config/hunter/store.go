package hunter

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 hunter 配置表的持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// defaultMaxIterations 是 MaxIterations 留空时的回退值（与 D1 DDL DEFAULT 40 对齐）。
const defaultMaxIterations = 40

// colsSelect 是所有 SELECT / RETURNING 路径的统一列序，与 scan() 字段一一对应。
const colsSelect = "id, code, kind, name, description, body, tools, max_iterations, enabled, created_at, updated_at"

// validateKind 应用层校验 kind（与 DB CHECK 双保险）。
func validateKind(k Kind) error {
	switch k {
	case KindOrchestrator, KindDomain:
		return nil
	default:
		return fmt.Errorf("非法 kind %q（应为 orchestrator|domain）", k)
	}
}

// Create 插入一行配置猎手，返回回读的完整行（含 uuid+timestamps）。
func (s *Store) Create(ctx context.Context, p NewParams) (Hunter, error) {
	if err := validateKind(p.Kind); err != nil {
		return Hunter{}, fmt.Errorf("create hunter: %w", err)
	}
	tools, err := marshalTools(p.Tools)
	if err != nil {
		return Hunter{}, fmt.Errorf("create hunter: %w", err)
	}
	maxIter := p.MaxIterations
	if maxIter <= 0 {
		maxIter = defaultMaxIterations
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO hunter (code, kind, name, description, body, tools, max_iterations, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+colsSelect,
		p.Code, string(p.Kind), p.Name, p.Description, p.Body, tools, maxIter, p.Enabled)
	var h Hunter
	if err := scan(row, &h); err != nil {
		return Hunter{}, fmt.Errorf("create hunter %q: %w", p.Code, err)
	}
	return h, nil
}

// Update 按 code 全量更新一行配置猎手（code 是稳定引用键，不可改）。
func (s *Store) Update(ctx context.Context, p NewParams) (Hunter, error) {
	if err := validateKind(p.Kind); err != nil {
		return Hunter{}, fmt.Errorf("update hunter: %w", err)
	}
	tools, err := marshalTools(p.Tools)
	if err != nil {
		return Hunter{}, fmt.Errorf("update hunter: %w", err)
	}
	maxIter := p.MaxIterations
	if maxIter <= 0 {
		maxIter = defaultMaxIterations
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE hunter
		SET kind=$2, name=$3, description=$4, body=$5, tools=$6, max_iterations=$7, enabled=$8, updated_at=now()
		WHERE code=$1
		RETURNING `+colsSelect,
		p.Code, string(p.Kind), p.Name, p.Description, p.Body, tools, maxIter, p.Enabled)
	var h Hunter
	if err := scan(row, &h); err != nil {
		return Hunter{}, fmt.Errorf("update hunter %q: %w", p.Code, err)
	}
	return h, nil
}

// Delete 按 code 删除。被 playbook_hunter 引用时会撞 DB ON DELETE RESTRICT。
func (s *Store) Delete(ctx context.Context, code string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM hunter WHERE code=$1", code)
	if err != nil {
		return fmt.Errorf("delete hunter %q: %w", code, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("delete hunter %q: 不存在", code)
	}
	return nil
}

// GetByID 按主键读取。
func (s *Store) GetByID(ctx context.Context, id string) (Hunter, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+colsSelect+" FROM hunter WHERE id=$1", id)
	var h Hunter
	if err := scan(row, &h); err != nil {
		return Hunter{}, fmt.Errorf("get hunter %s: %w", id, err)
	}
	return h, nil
}

// GetByCode 按稳定引用名读取（代码与种子的主要访问路径）。
func (s *Store) GetByCode(ctx context.Context, code string) (Hunter, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+colsSelect+" FROM hunter WHERE code=$1", code)
	var h Hunter
	if err := scan(row, &h); err != nil {
		return Hunter{}, fmt.Errorf("get hunter %q: %w", code, err)
	}
	return h, nil
}

// List 按 code 升序列出配置猎手；onlyEnabled=true 时过滤 enabled=false。
func (s *Store) List(ctx context.Context, onlyEnabled bool) ([]Hunter, error) {
	q := "SELECT " + colsSelect + " FROM hunter"
	if onlyEnabled {
		q += " WHERE enabled=true"
	}
	q += " ORDER BY code ASC"
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list hunters: %w", err)
	}
	defer rows.Close()

	var out []Hunter
	for rows.Next() {
		var h Hunter
		if err := scan(rows, &h); err != nil {
			return nil, fmt.Errorf("scan hunter: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// GetOrchestrator 取全局唯一的编排猎手（kind='orchestrator' AND enabled）。
// 命中零条或多条均报错，以保证 swarm 装配时编排者全局唯一（见 D1）。
func (s *Store) GetOrchestrator(ctx context.Context) (Hunter, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT "+colsSelect+" FROM hunter WHERE kind='orchestrator' AND enabled=true ORDER BY code ASC")
	if err != nil {
		return Hunter{}, fmt.Errorf("get orchestrator: %w", err)
	}
	defer rows.Close()

	var out []Hunter
	for rows.Next() {
		var h Hunter
		if err := scan(rows, &h); err != nil {
			return Hunter{}, fmt.Errorf("scan orchestrator: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return Hunter{}, fmt.Errorf("iterate orchestrator: %w", err)
	}
	switch len(out) {
	case 1:
		return out[0], nil
	case 0:
		return Hunter{}, fmt.Errorf("get orchestrator: 无 enabled 编排猎手")
	default:
		return Hunter{}, fmt.Errorf("get orchestrator: 命中 %d 条编排猎手，应全局唯一", len(out))
	}
}

// marshalTools 把 []string 序列化为 jsonb；nil 落空数组。
func marshalTools(tools []string) ([]byte, error) {
	if tools == nil {
		tools = []string{}
	}
	b, err := json.Marshal(tools)
	if err != nil {
		return nil, fmt.Errorf("marshal tools: %w", err)
	}
	return b, nil
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scan 是 colsSelect 列序的统一反序列化点。
func scan(r scanner, h *Hunter) error {
	var kind string
	var tools []byte
	if err := r.Scan(&h.ID, &h.Code, &kind, &h.Name, &h.Description, &h.Body,
		&tools, &h.MaxIterations, &h.Enabled, &h.CreatedAt, &h.UpdatedAt); err != nil {
		return err
	}
	h.Kind = Kind(kind)
	if len(tools) > 0 {
		if err := json.Unmarshal(tools, &h.Tools); err != nil {
			return fmt.Errorf("unmarshal tools: %w", err)
		}
	}
	return nil
}
