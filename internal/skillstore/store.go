package skillstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/override"
	"github.com/V3teran/liusha/internal/overridestore"
)

// ErrConflict 标记技能标识已被占用（接口层映射为 409）。
var ErrConflict = errors.New("技能标识已存在")

// Store 持久化自建技能（custom_skill）与内置技能的用户层（skill_override）。
type Store struct {
	pool      *pgxpool.Pool
	overrides overridestore.Table
}

// New 用 pgxpool 构造 Store。
func New(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:      pool,
		overrides: overridestore.Table{Pool: pool, Name: "skill_override", Key: "skill_code"},
	}
}

// ── 自建技能 ───────────────────────────────────────────────────────────

const customCols = "code, category, name, description, body, enabled"

// ListCustom 返回全部自建技能（按 category、code 升序）。
func (s *Store) ListCustom(ctx context.Context) ([]Skill, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+customCols+" FROM custom_skill ORDER BY category, code")
	if err != nil {
		return nil, fmt.Errorf("读自建技能: %w", err)
	}
	defer rows.Close()
	var out []Skill
	for rows.Next() {
		sk, err := scanCustom(rows)
		if err != nil {
			return nil, fmt.Errorf("读自建技能: %w", err)
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

// CreateCustom 新建自建技能。标识已存在返回 ErrConflict。
func (s *Store) CreateCustom(ctx context.Context, sk Skill) (Skill, error) {
	row := s.pool.QueryRow(ctx,
		"INSERT INTO custom_skill ("+customCols+") VALUES ($1, $2, $3, $4, $5, $6) RETURNING "+customCols,
		sk.Code, sk.Category, sk.Name, sk.Description, sk.Body, sk.Enabled)
	created, err := scanCustom(row)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Skill{}, fmt.Errorf("%w：%s", ErrConflict, sk.Code)
		}
		return Skill{}, fmt.Errorf("新建自建技能 %s: %w", sk.Code, err)
	}
	return created, nil
}

// UpdateCustom 在锁行事务内用 fn 计算自建技能的新内容并写回。不存在返回 pgx.ErrNoRows。
func (s *Store) UpdateCustom(ctx context.Context, code string, fn func(Skill) (Content, error)) (Skill, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Skill{}, fmt.Errorf("修改自建技能 %s: %w", code, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	cur, err := scanCustom(tx.QueryRow(ctx, "SELECT "+customCols+" FROM custom_skill WHERE code=$1 FOR UPDATE", code))
	if err != nil {
		return Skill{}, fmt.Errorf("自建技能 %s: %w", code, err)
	}
	c, err := fn(cur)
	if err != nil {
		return Skill{}, err
	}
	updated, err := scanCustom(tx.QueryRow(ctx,
		`UPDATE custom_skill SET name=$2, description=$3, body=$4, enabled=$5, updated_at=now()
		 WHERE code=$1 RETURNING `+customCols,
		code, c.Name, c.Description, c.Body, c.Enabled))
	if err != nil {
		return Skill{}, fmt.Errorf("修改自建技能 %s: %w", code, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Skill{}, fmt.Errorf("修改自建技能 %s: %w", code, err)
	}
	return updated, nil
}

// DeleteCustom 在调用方事务内删除自建技能（与清理各 agent 的引用原子落定）。不存在返回 pgx.ErrNoRows。
func (s *Store) DeleteCustom(ctx context.Context, tx pgx.Tx, code string) error {
	tag, err := tx.Exec(ctx, "DELETE FROM custom_skill WHERE code=$1", code)
	if err != nil {
		return fmt.Errorf("删除自建技能 %s: %w", code, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("自建技能 %s: %w", code, pgx.ErrNoRows)
	}
	return nil
}

func scanCustom(r pgx.Row) (Skill, error) {
	sk := Skill{IsBuiltin: false}
	err := r.Scan(&sk.Code, &sk.Category, &sk.Name, &sk.Description, &sk.Body, &sk.Enabled)
	return sk, err
}

// ── 内置技能的用户层 ───────────────────────────────────────────────────

// Overrides 读出全部内置技能的用户层（code → 用户层）。
func (s *Store) Overrides(ctx context.Context) (map[string]override.Overrides, error) {
	return s.overrides.All(ctx)
}

// UpdateOverrides 在锁行事务内用 fn 计算某内置技能的新用户层与生效值，写回两者。
// 生效值存为 last_saved：内置技能下线后，用户自定义过的据此转为自建技能。
func (s *Store) UpdateOverrides(ctx context.Context, code string, fn func(override.Overrides) (override.Overrides, Skill, error)) (override.Overrides, error) {
	return s.overrides.Update(ctx, code, func(ctx context.Context, tx pgx.Tx, cur override.Overrides) (override.Overrides, error) {
		next, effective, err := fn(cur)
		if err != nil {
			return nil, err
		}
		if len(next) == 0 {
			return next, nil
		}
		saved, err := json.Marshal(effective)
		if err != nil {
			return nil, fmt.Errorf("序列化技能 %s: %w", code, err)
		}
		if _, err := tx.Exec(ctx, "UPDATE skill_override SET last_saved=$2 WHERE skill_code=$1", code, saved); err != nil {
			return nil, fmt.Errorf("写技能 %s 的 last_saved: %w", code, err)
		}
		return next, nil
	})
}

// AdoptRetired 把出厂内容里已不存在、但用户自定义过的内置技能转为同 code 的自建技能
// （内容取 last_saved），并删除其用户层。各 agent 对它的绑定按 code 继续有效。
// 已有同 code 的自建技能时不覆盖。幂等。返回转换的 code。
func (s *Store) AdoptRetired(ctx context.Context, builtinCodes []string) ([]string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("转换已下线内置技能: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx,
		`DELETE FROM skill_override WHERE NOT (skill_code = ANY($1)) RETURNING skill_code, last_saved`,
		builtinCodes)
	if err != nil {
		return nil, fmt.Errorf("转换已下线内置技能: %w", err)
	}
	type retired struct {
		code  string
		saved Skill
	}
	var list []retired
	for rows.Next() {
		var r retired
		var raw []byte
		if err := rows.Scan(&r.code, &raw); err != nil {
			rows.Close()
			return nil, fmt.Errorf("转换已下线内置技能: %w", err)
		}
		if err := json.Unmarshal(raw, &r.saved); err != nil {
			rows.Close()
			return nil, fmt.Errorf("技能 %s 的 last_saved 损坏: %w", r.code, err)
		}
		list = append(list, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("转换已下线内置技能: %w", err)
	}

	adopted := make([]string, 0, len(list))
	for _, r := range list {
		sk := r.saved
		if _, err := tx.Exec(ctx,
			"INSERT INTO custom_skill ("+customCols+") VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (code) DO NOTHING",
			r.code, sk.Category, sk.Name, sk.Description, sk.Body, sk.Enabled); err != nil {
			return nil, fmt.Errorf("转换技能 %s: %w", r.code, err)
		}
		adopted = append(adopted, r.code)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("转换已下线内置技能: %w", err)
	}
	return adopted, nil
}
