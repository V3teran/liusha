// Package overridestore 是用户自定义层的表访问器：每张自定义表以资源键为主键、
// 一行一资源，fields 列存 override.Overrides。三类资源（agent、内置技能、系统设置）
// 共用同一读写协议：
//
//   - 只在被自定义时有行；用户层清空即删行；
//   - 写入在事务内取资源级锁后「读 → 计算 → 写」，同一资源的并发写（含删行）串行化，
//     章程编辑与工具装配并发也不会互相覆盖。
package overridestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/override"
)

// Table 描述一张自定义表。表名与键列名来自代码常量，不接受外部输入。
type Table struct {
	Pool *pgxpool.Pool
	Name string // 表名，如 agent_override
	Key  string // 主键列名，如 agent_code
}

// All 读出全部资源的用户层（资源键 → 用户层）。
func (t Table) All(ctx context.Context) (map[string]override.Overrides, error) {
	rows, err := t.Pool.Query(ctx, fmt.Sprintf("SELECT %s, fields FROM %s", t.Key, t.Name)) // #nosec G201 -- 表名与列名为代码常量
	if err != nil {
		return nil, fmt.Errorf("读 %s: %w", t.Name, err)
	}
	defer rows.Close()
	out := map[string]override.Overrides{}
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, fmt.Errorf("读 %s: %w", t.Name, err)
		}
		ov, err := decode(raw)
		if err != nil {
			return nil, fmt.Errorf("读 %s[%s]: %w", t.Name, key, err)
		}
		out[key] = ov
	}
	return out, rows.Err()
}

// Get 读出一个资源的用户层；未自定义时返回空用户层。
func (t Table) Get(ctx context.Context, key string) (override.Overrides, error) {
	var raw []byte
	err := t.Pool.QueryRow(ctx, fmt.Sprintf("SELECT fields FROM %s WHERE %s=$1", t.Name, t.Key), key).Scan(&raw) // #nosec G201 -- 表名与列名为代码常量
	if errors.Is(err, pgx.ErrNoRows) {
		return override.Overrides{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读 %s[%s]: %w", t.Name, key, err)
	}
	ov, err := decode(raw)
	if err != nil {
		return nil, fmt.Errorf("读 %s[%s]: %w", t.Name, key, err)
	}
	return ov, nil
}

// UpdateFunc 在锁行事务内计算新的用户层。tx 供同事务写附属列（如技能的 last_saved）。
type UpdateFunc func(ctx context.Context, tx pgx.Tx, current override.Overrides) (override.Overrides, error)

// Update 在事务内取资源级锁，交由 fn 计算新的用户层并写回；结果为空即删行。
// 返回写入后的用户层。fn 返回错误时事务回滚，原值不变。
func (t Table) Update(ctx context.Context, key string, fn UpdateFunc) (override.Overrides, error) {
	tx, err := t.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("写 %s[%s]: %w", t.Name, key, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	next, err := t.UpdateTx(ctx, tx, key, fn)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("提交 %s[%s]: %w", t.Name, key, err)
	}
	return next, nil
}

// UpdateTx 在调用方事务内执行与 Update 相同的协议（资源级锁 → 读 → fn → 写），提交归
// 调用方——用于必须与其它写入原子落定的修改（如删技能时清理各 agent 的引用）。
func (t Table) UpdateTx(ctx context.Context, tx pgx.Tx, key string, fn UpdateFunc) (override.Overrides, error) {
	// 资源级事务锁（表 + 资源键）：同一资源的写入完全串行，不依赖行是否存在——行锁
	// 挡不住「用户层清空即删行」：已有行时 INSERT … ON CONFLICT 不加锁，另一事务可在
	// 其后、FOR UPDATE 之前删掉该行，本事务随即查不到行，把固定资源误报成不存在。
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", t.Name+"/"+key); err != nil {
		return nil, fmt.Errorf("锁 %s[%s]: %w", t.Name, key, err)
	}
	// 再确保行存在：fn 可能在同事务写附属列（如技能的 last_saved），需要行在。结果为空时
	// 在同一事务内删除，外部看不到空行。
	if _, err := tx.Exec(ctx, fmt.Sprintf("INSERT INTO %s (%s) VALUES ($1) ON CONFLICT DO NOTHING", t.Name, t.Key), key); err != nil { // #nosec G201 -- 表名与列名为代码常量
		return nil, fmt.Errorf("写 %s[%s]: %w", t.Name, key, err)
	}
	var raw []byte
	if err := tx.QueryRow(ctx, fmt.Sprintf("SELECT fields FROM %s WHERE %s=$1 FOR UPDATE", t.Name, t.Key), key).Scan(&raw); err != nil { // #nosec G201 -- 表名与列名为代码常量
		return nil, fmt.Errorf("锁 %s[%s]: %w", t.Name, key, err)
	}
	current, err := decode(raw)
	if err != nil {
		return nil, fmt.Errorf("读 %s[%s]: %w", t.Name, key, err)
	}

	next, err := fn(ctx, tx, current)
	if err != nil {
		return nil, err
	}

	if len(next) == 0 {
		_, err = tx.Exec(ctx, fmt.Sprintf("DELETE FROM %s WHERE %s=$1", t.Name, t.Key), key) // #nosec G201 -- 表名与列名为代码常量
	} else {
		_, err = tx.Exec(ctx, fmt.Sprintf("UPDATE %s SET fields=$2, updated_at=now() WHERE %s=$1", t.Name, t.Key), key, encode(next)) // #nosec G201 -- 表名与列名为代码常量
	}
	if err != nil {
		return nil, fmt.Errorf("写 %s[%s]: %w", t.Name, key, err)
	}
	return next, nil
}

func decode(raw []byte) (override.Overrides, error) {
	ov := override.Overrides{}
	if len(raw) == 0 {
		return ov, nil
	}
	if err := json.Unmarshal(raw, &ov); err != nil {
		return nil, err
	}
	return ov, nil
}

func encode(ov override.Overrides) []byte {
	b, err := json.Marshal(ov)
	if err != nil {
		panic(fmt.Sprintf("overridestore: 序列化用户层失败: %v", err))
	}
	return b
}
