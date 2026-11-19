package skillstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/builtin"
	"github.com/V3teran/liusha/internal/builtinsync"
)

// ErrConflict 标记技能标识已被占用（接口层映射为 409）。
var ErrConflict = errors.New("技能标识已存在")

// Store 持久化技能（skill 表）。
type Store struct {
	pool *pgxpool.Pool
}

// New 用 pgxpool 构造 Store。
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const cols = "code, category, name, description, body, enabled, is_builtin, seed_hash, deleted_at IS NOT NULL"

// table 是出厂同步视角的技能表：category 随出厂内容。
var table = builtinsync.Table{Name: "skill", Extra: []string{"category"}}

// List 返回全部技能行（含已删除的内置技能，按 category、code 升序）。
func (s *Store) List(ctx context.Context) ([]Record, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+cols+" FROM skill ORDER BY category, code")
	if err != nil {
		return nil, fmt.Errorf("读技能: %w", err)
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("读技能: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Create 新建自建技能。标识已存在返回 ErrConflict。
func (s *Store) Create(ctx context.Context, sk Skill) (Record, error) {
	created, err := scan(s.pool.QueryRow(ctx,
		`INSERT INTO skill (code, category, name, description, body, enabled, is_builtin)
		 VALUES ($1, $2, $3, $4, $5, $6, false) RETURNING `+cols,
		sk.Code, sk.Category, sk.Name, sk.Description, sk.Body, sk.Enabled))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Record{}, fmt.Errorf("%w：%s", ErrConflict, sk.Code)
		}
		return Record{}, fmt.Errorf("新建技能 %s: %w", sk.Code, err)
	}
	return created, nil
}

// Update 在锁行事务内用 fn 计算技能的新内容并写回。内置技能编辑后的内容恰与当前出厂相同时，
// 基线随之更新（不算自定义）。不存在（含已删除）返回 pgx.ErrNoRows。
func (s *Store) Update(ctx context.Context, code string, fn func(Record) (Content, error)) (Record, error) {
	var updated Record
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		cur, err := scan(tx.QueryRow(ctx, "SELECT "+cols+" FROM skill WHERE code=$1 AND deleted_at IS NULL FOR UPDATE", code))
		if err != nil {
			return fmt.Errorf("技能 %s: %w", code, err)
		}
		c, err := fn(cur)
		if err != nil {
			return err
		}
		seedHash := cur.SeedHash
		if cur.IsBuiltin {
			b, ok := builtin.SkillByCode(code)
			seedHash = builtinsync.AfterEdit(cur.SeedHash, builtinsync.Hash(c.Name, c.Description, c.Body), SeedHash(b), ok)
		}
		updated, err = scan(tx.QueryRow(ctx,
			`UPDATE skill SET name=$2, description=$3, body=$4, enabled=$5, seed_hash=$6, updated_at=now()
			 WHERE code=$1 RETURNING `+cols,
			code, c.Name, c.Description, c.Body, c.Enabled, seedHash))
		if err != nil {
			return fmt.Errorf("修改技能 %s: %w", code, err)
		}
		return nil
	})
	return updated, err
}

// Delete 在调用方事务内删除技能（与清理各 agent 的引用原子落定）：自建技能删行；内置技能记
// 删除标记，出厂同步不再补回。不存在（含已删除）返回 pgx.ErrNoRows。
func (s *Store) Delete(ctx context.Context, tx pgx.Tx, code string) error {
	tag, err := tx.Exec(ctx, "DELETE FROM skill WHERE code=$1 AND NOT is_builtin", code)
	if err == nil && tag.RowsAffected() == 0 {
		tag, err = tx.Exec(ctx, "UPDATE skill SET deleted_at=now(), updated_at=now() WHERE code=$1 AND is_builtin AND deleted_at IS NULL", code)
	}
	if err != nil {
		return fmt.Errorf("删除技能 %s: %w", code, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("技能 %s: %w", code, pgx.ErrNoRows)
	}
	return nil
}

// Restore 把用户删除的内置技能恢复成当前出厂内容（启用）。不是已删除的内置技能、或出厂已下线
// 时返回 pgx.ErrNoRows。
func (s *Store) Restore(ctx context.Context, code string) error {
	b, ok := builtin.SkillByCode(code)
	if !ok {
		return fmt.Errorf("内置技能 %s: %w", code, pgx.ErrNoRows)
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return table.Restore(ctx, tx, seedOf(b))
	})
}

// SyncBuiltins 把出厂技能同步进表（见 internal/builtinsync）：用户没改过的跟随新版，改过的不动；
// 已下线的内置技能，改过的转为自建，没改过的删除。各 agent 按 code 引用技能，转自建后照常有效。
func (s *Store) SyncBuiltins(ctx context.Context) ([]builtinsync.Action, error) {
	return builtinsync.Sync(ctx, s.pool, table, Seeds(), nil)
}

// Seeds 返回出厂技能的同步视图。
func Seeds() []builtinsync.SeedRow {
	var out []builtinsync.SeedRow
	for _, b := range builtin.Skills() {
		out = append(out, seedOf(b))
	}
	return out
}

func seedOf(b builtin.Skill) builtinsync.SeedRow {
	return builtinsync.SeedRow{Code: b.Code, Name: b.Name, Description: b.Description, Body: b.Body, Extra: []any{b.Category}}
}

func scan(r pgx.Row) (Record, error) {
	var rec Record
	err := r.Scan(&rec.Code, &rec.Category, &rec.Name, &rec.Description, &rec.Body, &rec.Enabled, &rec.IsBuiltin, &rec.SeedHash, &rec.Deleted)
	return rec, err
}
