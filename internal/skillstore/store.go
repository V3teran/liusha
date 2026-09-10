package skill

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 skill 配置表的持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const colsSelect = "id, code, category, name, description, body, is_builtin, enabled, created_at, updated_at"

// GetByID 按 uuid 读 Skill。
func (s *Store) GetByID(ctx context.Context, id string) (Skill, error) {
	q := "SELECT " + colsSelect + " FROM skill WHERE id=$1"
	return s.scanOne(s.pool.QueryRow(ctx, q, id))
}

// GetByCode 按 code 读 Skill。
func (s *Store) GetByCode(ctx context.Context, code string) (Skill, error) {
	q := "SELECT " + colsSelect + " FROM skill WHERE code=$1"
	return s.scanOne(s.pool.QueryRow(ctx, q, code))
}

// List 列出 Skill，支持分页和过滤。
func (s *Store) List(ctx context.Context, p ListParams) ([]Skill, error) {
	var whereParts []string
	var args []any
	argIdx := 1

	if p.Category != "" {
		whereParts = append(whereParts, fmt.Sprintf("category=$%d", argIdx))
		args = append(args, p.Category)
		argIdx++
	}

	if p.OnlyEnabled {
		whereParts = append(whereParts, "enabled=true")
	}

	if p.Search != "" {
		whereParts = append(whereParts, fmt.Sprintf("(name ILIKE $%d OR description ILIKE $%d)", argIdx, argIdx))
		args = append(args, "%"+p.Search+"%")
		argIdx++
	}

	where := ""
	if len(whereParts) > 0 {
		where = " WHERE " + strings.Join(whereParts, " AND ")
	}

	limit := p.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := p.Offset
	if offset < 0 {
		offset = 0
	}

	q := fmt.Sprintf("SELECT %s FROM skill%s ORDER BY category, code LIMIT %d OFFSET %d",
		colsSelect, where, limit, offset)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query skills: %w", err)
	}
	defer rows.Close()

	var out []Skill
	for rows.Next() {
		sk, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

// Create 创建新 Skill。
func (s *Store) Create(ctx context.Context, sk Skill) (Skill, error) {
	q := `
		INSERT INTO skill (code, category, name, description, body, is_builtin, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING ` + colsSelect

	return s.scanOne(s.pool.QueryRow(ctx, q,
		sk.Code, sk.Category, sk.Name, sk.Description, sk.Body, sk.IsBuiltin, sk.Enabled))
}

// Update 更新 Skill（按 ID）。
func (s *Store) Update(ctx context.Context, id string, p UpdateParams) (Skill, error) {
	var setParts []string
	var args []any
	argIdx := 1

	if p.Name != nil {
		setParts = append(setParts, fmt.Sprintf("name=$%d", argIdx))
		args = append(args, *p.Name)
		argIdx++
	}
	if p.Description != nil {
		setParts = append(setParts, fmt.Sprintf("description=$%d", argIdx))
		args = append(args, *p.Description)
		argIdx++
	}
	if p.Body != nil {
		setParts = append(setParts, fmt.Sprintf("body=$%d", argIdx))
		args = append(args, *p.Body)
		argIdx++
	}
	if p.Enabled != nil {
		setParts = append(setParts, fmt.Sprintf("enabled=$%d", argIdx))
		args = append(args, *p.Enabled)
		argIdx++
	}

	if len(setParts) == 0 {
		return Skill{}, fmt.Errorf("没有要更新的字段")
	}

	setParts = append(setParts, "updated_at=now()")
	args = append(args, id)

	q := fmt.Sprintf("UPDATE skill SET %s WHERE id=$%d RETURNING %s",
		strings.Join(setParts, ", "), argIdx, colsSelect)

	return s.scanOne(s.pool.QueryRow(ctx, q, args...))
}

// Delete 删除 Skill（仅非内置）。
func (s *Store) Delete(ctx context.Context, id string) error {
	q := "DELETE FROM skill WHERE id=$1 AND is_builtin=false"
	tag, err := s.pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("delete skill: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("skill 不存在或为内置 skill（不可删除）")
	}
	return nil
}

// scanOne 扫描单行。
func (s *Store) scanOne(row pgx.Row) (Skill, error) {
	var sk Skill
	err := row.Scan(
		&sk.ID, &sk.Code, &sk.Category, &sk.Name, &sk.Description,
		&sk.Body, &sk.IsBuiltin, &sk.Enabled, &sk.CreatedAt, &sk.UpdatedAt,
	)
	if err != nil {
		return Skill{}, err
	}
	return sk, nil
}

// scan 扫描多行。
func (s *Store) scan(rows pgx.Rows) (Skill, error) {
	var sk Skill
	err := rows.Scan(
		&sk.ID, &sk.Code, &sk.Category, &sk.Name, &sk.Description,
		&sk.Body, &sk.IsBuiltin, &sk.Enabled, &sk.CreatedAt, &sk.UpdatedAt,
	)
	return sk, err
}
