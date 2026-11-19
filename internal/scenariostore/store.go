package scenariostore

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/builtin"
	"github.com/V3teran/liusha/internal/builtinsync"
)

// Store 持久化场景（scenario 表）。
type Store struct {
	pool *pgxpool.Pool
}

// New 用 pgxpool 构造 Store。
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const cols = "code, name, description, body, enabled, is_builtin, seed_hash, deleted_at IS NOT NULL"

var table = builtinsync.Table{Name: "scenario"}

// List 返回全部场景行（含已删除的内置场景；内置在前，再按 code 升序）。
func (s *Store) List(ctx context.Context) ([]Record, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+cols+" FROM scenario ORDER BY is_builtin DESC, code")
	if err != nil {
		return nil, fmt.Errorf("读场景: %w", err)
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("读场景: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Create 新建自建场景。标识已存在返回 ErrConflict。
func (s *Store) Create(ctx context.Context, sc Scenario) (Record, error) {
	created, err := scan(s.pool.QueryRow(ctx,
		`INSERT INTO scenario (code, name, description, body, enabled, is_builtin)
		 VALUES ($1, $2, $3, $4, $5, false) RETURNING `+cols,
		sc.Code, sc.Name, sc.Description, sc.Body, sc.Enabled))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Record{}, fmt.Errorf("%w：%s", ErrConflict, sc.Code)
		}
		return Record{}, fmt.Errorf("新建场景 %s: %w", sc.Code, err)
	}
	return created, nil
}

// Update 锁住场景行，用 fn 计算新内容并写回。停用前确认没有定时模板引用它；内置场景编辑后的
// 内容恰与当前出厂相同时，基线随之更新（不算自定义）。不存在返回 pgx.ErrNoRows。
func (s *Store) Update(ctx context.Context, code string, fn func(Record) (Content, error)) (Record, error) {
	var updated Record
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		cur, err := lockRow(ctx, tx, code)
		if err != nil {
			return err
		}
		c, err := fn(cur)
		if err != nil {
			return err
		}
		if cur.Enabled && !c.Enabled {
			if err := ensureUnreferenced(ctx, tx, code, "停用"); err != nil {
				return err
			}
		}
		seedHash := cur.SeedHash
		if cur.IsBuiltin {
			b, ok := builtin.ScenarioByCode(code)
			seedHash = builtinsync.AfterEdit(cur.SeedHash, builtinsync.Hash(c.Name, c.Description, c.Body), SeedHash(b), ok)
		}
		updated, err = scan(tx.QueryRow(ctx,
			`UPDATE scenario SET name=$2, description=$3, body=$4, enabled=$5, seed_hash=$6, updated_at=now()
			 WHERE code=$1 RETURNING `+cols,
			code, c.Name, c.Description, c.Body, c.Enabled, seedHash))
		if err != nil {
			return fmt.Errorf("修改场景 %s: %w", code, err)
		}
		return nil
	})
	return updated, err
}

// Delete 删除场景：自建场景删行；内置场景记删除标记，出厂同步不再补回。被定时模板引用返回
// ErrInUse；不存在（含已删除）返回 pgx.ErrNoRows。已建任务读自身快照，不受影响。
func (s *Store) Delete(ctx context.Context, code string) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		cur, err := lockRow(ctx, tx, code)
		if err != nil {
			return err
		}
		if err := ensureUnreferenced(ctx, tx, code, "删除"); err != nil {
			return err
		}
		sql := "DELETE FROM scenario WHERE code=$1"
		if cur.IsBuiltin {
			sql = "UPDATE scenario SET deleted_at=now(), updated_at=now() WHERE code=$1"
		}
		if _, err := tx.Exec(ctx, sql, code); err != nil {
			return fmt.Errorf("删除场景 %s: %w", code, err)
		}
		return nil
	})
}

// Restore 把用户删除的内置场景恢复成当前出厂内容（启用）。不是已删除的内置场景、或出厂已下线
// 时返回 pgx.ErrNoRows。
func (s *Store) Restore(ctx context.Context, code string) error {
	b, ok := builtin.ScenarioByCode(code)
	if !ok {
		return fmt.Errorf("内置场景 %s: %w", code, pgx.ErrNoRows)
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return table.Restore(ctx, tx, seedOf(b))
	})
}

// LockUsable 在调用方事务内锁住场景行并确认场景存在且启用——新建引用它的定时模板前调用：
// 与场景的停用、删除串行，模板建成后引用的场景必然存在且启用。不可用返回 ErrUnavailable。
func LockUsable(ctx context.Context, tx pgx.Tx, code string) error {
	cur, err := lockRow(ctx, tx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotFound(code)
	}
	if err != nil {
		return err
	}
	return Usable(cur.Scenario)
}

// Usable 报告场景能否用于新任务：已停用返回 ErrUnavailable。
func Usable(sc Scenario) error {
	if !sc.Enabled {
		return fmt.Errorf("%w：「%s」已停用", ErrUnavailable, sc.Name)
	}
	return nil
}

// NotFound 返回点名的场景不存在时的错误（ErrUnavailable）。
func NotFound(code string) error {
	return fmt.Errorf("%w：%s 不存在", ErrUnavailable, code)
}

// SyncBuiltins 把出厂场景同步进表（见 internal/builtinsync）。已下线、没改过的内置场景若仍被
// 定时模板引用，转为自建保留（模板只引用存在的场景）。
func (s *Store) SyncBuiltins(ctx context.Context) ([]builtinsync.Action, error) {
	return builtinsync.Sync(ctx, s.pool, table, Seeds(), func(ctx context.Context, tx pgx.Tx, code string) (bool, error) {
		names, err := referencingSchedules(ctx, tx, code)
		return len(names) > 0, err
	})
}

// Seeds 返回出厂场景的同步视图。
func Seeds() []builtinsync.SeedRow {
	var out []builtinsync.SeedRow
	for _, b := range builtin.Scenarios() {
		out = append(out, seedOf(b))
	}
	return out
}

func seedOf(b builtin.Scenario) builtinsync.SeedRow {
	return builtinsync.SeedRow{Code: b.Code, Name: b.Name, Description: b.Description, Body: b.Body}
}

// lockRow 在调用方事务内锁住场景行并读出。场景的修改（含停用）、删除、出厂同步与引用它的
// 定时模板新建都先锁这一行：「核对没有引用 → 停用 / 删除」与「核对存在且启用 → 建模板」彼此
// 串行（读已提交下，后到者锁到行后的下一条语句看得见先到者已提交的写入）。
func lockRow(ctx context.Context, tx pgx.Tx, code string) (Record, error) {
	cur, err := scan(tx.QueryRow(ctx, "SELECT "+cols+" FROM scenario WHERE code=$1 AND deleted_at IS NULL FOR UPDATE", code))
	if err != nil {
		return Record{}, fmt.Errorf("场景 %s: %w", code, err)
	}
	return cur, nil
}

// ensureUnreferenced 确认没有定时模板（含停用中的：重新启用就会到点触发）引用该场景。
func ensureUnreferenced(ctx context.Context, tx pgx.Tx, code, verb string) error {
	names, err := referencingSchedules(ctx, tx, code)
	if err != nil {
		return err
	}
	if len(names) > 0 {
		return fmt.Errorf("%w：定时模板「%s」在使用它，先修改或删除这些模板再%s", ErrInUse, strings.Join(names, "」「"), verb)
	}
	return nil
}

func referencingSchedules(ctx context.Context, tx pgx.Tx, code string) ([]string, error) {
	rows, err := tx.Query(ctx, "SELECT title FROM cron_schedule WHERE scenario=$1 ORDER BY created_at", code)
	if err != nil {
		return nil, fmt.Errorf("查引用场景 %s 的定时模板: %w", code, err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("查引用场景 %s 的定时模板: %w", code, err)
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

func scan(r pgx.Row) (Record, error) {
	var rec Record
	err := r.Scan(&rec.Code, &rec.Name, &rec.Description, &rec.Body, &rec.Enabled, &rec.IsBuiltin, &rec.SeedHash, &rec.Deleted)
	return rec, err
}
