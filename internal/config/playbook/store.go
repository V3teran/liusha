package playbook

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	cfghunter "github.com/V3teran/liusha/internal/config/hunter"
)

// Store 封装 playbook + playbook_hunter 表的持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是 playbook 所有 SELECT / RETURNING 路径的统一列序。
const colsSelect = "id, code, name, description, enabled, created_at, updated_at"

// hunterCols 是 ListHunters JOIN 回读 cfghunter.Hunter 的列序（表别名 h）。
const hunterCols = "h.id, h.code, h.kind, h.name, h.description, h.body, h.tools, h.max_iterations, h.enabled, h.created_at, h.updated_at"

// Create 插入一行 playbook，返回回读的完整行。
func (s *Store) Create(ctx context.Context, p NewParams) (Playbook, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO playbook (code, name, description, enabled)
		VALUES ($1, $2, $3, $4)
		RETURNING `+colsSelect,
		p.Code, p.Name, p.Description, p.Enabled)
	var pb Playbook
	if err := scan(row, &pb); err != nil {
		return Playbook{}, fmt.Errorf("create playbook %q: %w", p.Code, err)
	}
	return pb, nil
}

// Update 按 code 全量更新一行 playbook。
func (s *Store) Update(ctx context.Context, p NewParams) (Playbook, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE playbook
		SET name=$2, description=$3, enabled=$4, updated_at=now()
		WHERE code=$1
		RETURNING `+colsSelect,
		p.Code, p.Name, p.Description, p.Enabled)
	var pb Playbook
	if err := scan(row, &pb); err != nil {
		return Playbook{}, fmt.Errorf("update playbook %q: %w", p.Code, err)
	}
	return pb, nil
}

// Delete 按 code 删除 playbook。组合关系随 ON DELETE CASCADE 清除；
// 被 scenario 引用时会撞 scenario 的 ON DELETE RESTRICT。
func (s *Store) Delete(ctx context.Context, code string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM playbook WHERE code=$1", code)
	if err != nil {
		return fmt.Errorf("delete playbook %q: %w", code, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("delete playbook %q: 不存在", code)
	}
	return nil
}

// GetByID 按主键读取。
func (s *Store) GetByID(ctx context.Context, id string) (Playbook, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+colsSelect+" FROM playbook WHERE id=$1", id)
	var pb Playbook
	if err := scan(row, &pb); err != nil {
		return Playbook{}, fmt.Errorf("get playbook %s: %w", id, err)
	}
	return pb, nil
}

// GetByCode 按 code 读取。
func (s *Store) GetByCode(ctx context.Context, code string) (Playbook, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+colsSelect+" FROM playbook WHERE code=$1", code)
	var pb Playbook
	if err := scan(row, &pb); err != nil {
		return Playbook{}, fmt.Errorf("get playbook %q: %w", code, err)
	}
	return pb, nil
}

// List 按 code 升序列出所有 playbook。
func (s *Store) List(ctx context.Context) ([]Playbook, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+colsSelect+" FROM playbook ORDER BY code ASC")
	if err != nil {
		return nil, fmt.Errorf("list playbooks: %w", err)
	}
	defer rows.Close()

	var out []Playbook
	for rows.Next() {
		var pb Playbook
		if err := scan(rows, &pb); err != nil {
			return nil, fmt.Errorf("scan playbook: %w", err)
		}
		out = append(out, pb)
	}
	return out, rows.Err()
}

// SetHunters 事务内重设 playbook 的猎手组合（先删后插，按 position）。幂等。
func (s *Store) SetHunters(ctx context.Context, playbookID string, items []PlaybookHunter) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("set hunters: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "DELETE FROM playbook_hunter WHERE playbook_id=$1", playbookID); err != nil {
		return fmt.Errorf("set hunters: clear: %w", err)
	}
	for _, it := range items {
		if _, err := tx.Exec(ctx, `
			INSERT INTO playbook_hunter (playbook_id, hunter_id, position)
			VALUES ($1, $2, $3)`,
			playbookID, it.HunterID, it.Position); err != nil {
			return fmt.Errorf("set hunters: insert %s: %w", it.HunterID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("set hunters: commit: %w", err)
	}
	return nil
}

// ListHunters 按 position 升序返回 playbook 组合内的领域猎手，
// 用于 solo 拼 body / swarm 列子代理。
func (s *Store) ListHunters(ctx context.Context, playbookID string) ([]cfghunter.Hunter, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+hunterCols+`
		FROM playbook_hunter ph
		JOIN hunter h ON h.id = ph.hunter_id
		WHERE ph.playbook_id=$1
		ORDER BY ph.position ASC`, playbookID)
	if err != nil {
		return nil, fmt.Errorf("list playbook hunters: %w", err)
	}
	defer rows.Close()

	var out []cfghunter.Hunter
	for rows.Next() {
		h, err := scanHunter(rows)
		if err != nil {
			return nil, fmt.Errorf("scan playbook hunter: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scan 是 playbook colsSelect 列序的统一反序列化点。
func scan(r scanner, pb *Playbook) error {
	return r.Scan(&pb.ID, &pb.Code, &pb.Name, &pb.Description, &pb.Enabled, &pb.CreatedAt, &pb.UpdatedAt)
}

// scanHunter 按 hunterCols 列序反序列化一行 cfghunter.Hunter（JOIN 结果）。
func scanHunter(r pgx.Row) (cfghunter.Hunter, error) {
	var h cfghunter.Hunter
	var kind string
	var tools []byte
	if err := r.Scan(&h.ID, &h.Code, &kind, &h.Name, &h.Description, &h.Body,
		&tools, &h.MaxIterations, &h.Enabled, &h.CreatedAt, &h.UpdatedAt); err != nil {
		return cfghunter.Hunter{}, err
	}
	h.Kind = cfghunter.Kind(kind)
	if len(tools) > 0 {
		if err := json.Unmarshal(tools, &h.Tools); err != nil {
			return cfghunter.Hunter{}, fmt.Errorf("unmarshal tools: %w", err)
		}
	}
	return h, nil
}
