package builtinsync

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Table 描述一张「出厂种子 + 用户数据」的单表。表与列名来自代码常量，不接受外部输入。
// 公共列：code、name、description、body、enabled、is_builtin、seed_hash、updated_at。
type Table struct {
	Name  string   // 表名：skill / scenario
	Extra []string // 随出厂内容、用户不可编辑的附加列（如技能的 category），按 Seed.Extra 的顺序
}

// SeedRow 是一份出厂内容。
type SeedRow struct {
	Code        string
	Name        string
	Description string
	Body        string
	Extra       []any // 与 Table.Extra 一一对应
}

// RetireFunc 在删除一个出厂已下线的内置行（未改过，或已被用户删除）前调用：返回 true 则改为
// 转自建保留（如场景仍被定时模板引用）。在同步事务内执行。
type RetireFunc func(ctx context.Context, tx pgx.Tx, code string) (keep bool, err error)

// Sync 把出厂种子同步进表（事务 + 表级锁，多副本并发启动只有一个在写）。返回实际执行的动作。
// 读行时加行锁：用户编辑同样先锁行再「读 → 算 → 写」，两者串行，同步不会覆盖刚保存的修改，
// 编辑也不会基于同步前的旧内容写回。
func Sync(ctx context.Context, pool *pgxpool.Pool, t Table, seeds []SeedRow, retire RetireFunc) ([]Action, error) {
	var done []Action
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", "builtinsync/"+t.Name); err != nil {
			return fmt.Errorf("锁 %s: %w", t.Name, err)
		}
		rows, err := readRows(ctx, tx, t.Name)
		if err != nil {
			return err
		}
		byCode := make(map[string]SeedRow, len(seeds))
		plan := make([]Seed, 0, len(seeds))
		for _, s := range seeds {
			byCode[s.Code] = s
			plan = append(plan, Seed{Code: s.Code, Hash: Hash(s.Name, s.Description, s.Body)})
		}
		for _, a := range Plan(rows, plan) {
			op, err := t.apply(ctx, tx, a, byCode[a.Code], retire)
			if err != nil {
				return fmt.Errorf("同步 %s[%s]: %w", t.Name, a.Code, err)
			}
			done = append(done, Action{Code: a.Code, Op: op})
		}
		return t.refreshExtra(ctx, tx, seeds)
	})
	if err != nil {
		return nil, fmt.Errorf("同步出厂%s: %w", t.Name, err)
	}
	return done, nil
}

func readRows(ctx context.Context, tx pgx.Tx, table string) ([]Row, error) {
	rs, err := tx.Query(ctx, fmt.Sprintf("SELECT code, name, description, body, is_builtin, deleted_at IS NOT NULL, seed_hash FROM %s FOR UPDATE", table)) // #nosec G201 -- 表名为代码常量
	if err != nil {
		return nil, fmt.Errorf("读 %s: %w", table, err)
	}
	defer rs.Close()
	var out []Row
	for rs.Next() {
		var r Row
		var name, desc, body string
		if err := rs.Scan(&r.Code, &name, &desc, &body, &r.IsBuiltin, &r.Deleted, &r.SeedHash); err != nil {
			return nil, fmt.Errorf("读 %s: %w", table, err)
		}
		r.Hash = Hash(name, desc, body)
		out = append(out, r)
	}
	return out, rs.Err()
}

func (t Table) apply(ctx context.Context, tx pgx.Tx, a Action, s SeedRow, retire RetireFunc) (Op, error) {
	hash := Hash(s.Name, s.Description, s.Body)
	switch a.Op {
	case OpInsert:
		cols := append([]string{"code", "name", "description", "body", "is_builtin", "seed_hash"}, t.Extra...)
		args := append([]any{s.Code, s.Name, s.Description, s.Body, true, hash}, s.Extra...)
		_, err := tx.Exec(ctx, fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", t.Name, strings.Join(cols, ", "), placeholders(len(cols))), args...) // #nosec G201 -- 表名与列名为代码常量
		return a.Op, err
	case OpUpdate:
		sets := []string{"name=$2", "description=$3", "body=$4", "seed_hash=$5"}
		args := []any{s.Code, s.Name, s.Description, s.Body, hash}
		for i, c := range t.Extra {
			sets = append(sets, fmt.Sprintf("%s=$%d", c, len(args)+1))
			args = append(args, s.Extra[i])
		}
		_, err := tx.Exec(ctx, fmt.Sprintf("UPDATE %s SET %s, updated_at=now() WHERE code=$1", t.Name, strings.Join(sets, ", ")), args...) // #nosec G201 -- 表名与列名为代码常量
		return a.Op, err
	case OpBaseline:
		_, err := tx.Exec(ctx, fmt.Sprintf("UPDATE %s SET seed_hash=$2 WHERE code=$1", t.Name), a.Code, hash) // #nosec G201 -- 表名为代码常量
		return a.Op, err
	case OpRetire:
		if retire != nil {
			keep, err := retire(ctx, tx, a.Code)
			if err != nil {
				return a.Op, err
			}
			if keep {
				return OpDetach, t.detach(ctx, tx, a.Code)
			}
		}
		_, err := tx.Exec(ctx, fmt.Sprintf("DELETE FROM %s WHERE code=$1", t.Name), a.Code) // #nosec G201 -- 表名为代码常量
		return a.Op, err
	case OpDetach:
		return a.Op, t.detach(ctx, tx, a.Code)
	}
	return a.Op, fmt.Errorf("未知同步动作 %d", a.Op)
}

// Restore 在调用方事务内把一个被用户删除的内置行恢复成出厂内容（启用、清除删除标记、基线为
// 当前出厂）。不是被删除的内置行时返回 pgx.ErrNoRows。
func (t Table) Restore(ctx context.Context, tx pgx.Tx, s SeedRow) error {
	sets := []string{"name=$2", "description=$3", "body=$4", "seed_hash=$5", "enabled=true", "deleted_at=NULL"}
	args := []any{s.Code, s.Name, s.Description, s.Body, Hash(s.Name, s.Description, s.Body)}
	for i, c := range t.Extra {
		sets = append(sets, fmt.Sprintf("%s=$%d", c, len(args)+1))
		args = append(args, s.Extra[i])
	}
	tag, err := tx.Exec(ctx, fmt.Sprintf("UPDATE %s SET %s, updated_at=now() WHERE code=$1 AND is_builtin AND deleted_at IS NOT NULL", t.Name, strings.Join(sets, ", ")), args...) // #nosec G201 -- 表名与列名为代码常量
	if err != nil {
		return fmt.Errorf("恢复 %s[%s]: %w", t.Name, s.Code, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%s[%s] 不是已删除的内置项: %w", t.Name, s.Code, pgx.ErrNoRows)
	}
	return nil
}

func (t Table) detach(ctx context.Context, tx pgx.Tx, code string) error {
	_, err := tx.Exec(ctx, fmt.Sprintf("UPDATE %s SET is_builtin=false, seed_hash=NULL, deleted_at=NULL, updated_at=now() WHERE code=$1", t.Name), code) // #nosec G201 -- 表名为代码常量
	return err
}

// refreshExtra 让内置行的附加列跟随出厂（用户不可编辑，与内容是否改过无关）。
func (t Table) refreshExtra(ctx context.Context, tx pgx.Tx, seeds []SeedRow) error {
	for _, s := range seeds {
		for i, c := range t.Extra {
			if _, err := tx.Exec(ctx, fmt.Sprintf("UPDATE %s SET %s=$2 WHERE code=$1 AND is_builtin AND %s IS DISTINCT FROM $2", t.Name, c, c), s.Code, s.Extra[i]); err != nil { // #nosec G201 -- 表名与列名为代码常量
				return fmt.Errorf("同步 %s[%s].%s: %w", t.Name, s.Code, c, err)
			}
		}
	}
	return nil
}

func placeholders(n int) string {
	ps := make([]string, n)
	for i := range ps {
		ps[i] = fmt.Sprintf("$%d", i+1)
	}
	return strings.Join(ps, ", ")
}
