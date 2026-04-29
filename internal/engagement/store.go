package engagement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 engagement 表的所有持久化操作。
type Store struct{ pool *pgxpool.Pool }

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT 路径的统一列序，与 scan() 的字段顺序一一对应。
const colsSelect = "id, tenant_id, mode, scope_host, status, memory_facts, memory_ideas, memory_hints, created_at, last_activity_at"

// LookupOrCreateProxy 是 LookupOrCreate 的便利包装：
// tenant 固定 "default"、mode 固定 ModeProxy，仅返回 engagement ID。
// 用于 httpapi POST /engagement/proxy 的窄接口实现。
func (s *Store) LookupOrCreateProxy(ctx context.Context, host string) (string, error) {
	e, err := s.LookupOrCreate(ctx, "default", host, ModeProxy)
	if err != nil {
		return "", err
	}
	return e.ID, nil
}

// LookupOrCreate 返回 (tenant, host) 下当前 active 的 engagement；
// 若不存在则懒创建一行。这是 v1 唯一的 engagement 入口。
func (s *Store) LookupOrCreate(ctx context.Context, tenant, host string, mode Mode) (Engagement, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+colsSelect+`
		FROM engagement
		WHERE tenant_id=$1 AND scope_host=$2 AND status='active'
		LIMIT 1`, tenant, host)
	var e Engagement
	err := scan(row, &e)
	if err == nil {
		return e, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Engagement{}, fmt.Errorf("lookup engagement: %w", err)
	}
	row = s.pool.QueryRow(ctx, `
		INSERT INTO engagement (tenant_id, mode, scope_host, status)
		VALUES ($1,$2,$3,'active')
		RETURNING `+colsSelect, tenant, mode, host)
	if err := scan(row, &e); err != nil {
		return Engagement{}, fmt.Errorf("insert engagement: %w", err)
	}
	return e, nil
}

// GetByID 按主键读取（不限 status）。
func (s *Store) GetByID(ctx context.Context, id string) (Engagement, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+colsSelect+" FROM engagement WHERE id=$1", id)
	var e Engagement
	if err := scan(row, &e); err != nil {
		return Engagement{}, fmt.Errorf("get engagement %s: %w", id, err)
	}
	return e, nil
}

// Abort 把 engagement 置为 aborted（释放 active 唯一约束位）。
func (s *Store) Abort(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE engagement SET status='aborted', last_activity_at=now() WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("abort engagement %s: %w", id, err)
	}
	return nil
}

// Touch 仅刷新 last_activity_at，用于活动心跳。
func (s *Store) Touch(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE engagement SET last_activity_at=now() WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("touch engagement %s: %w", id, err)
	}
	return nil
}

// ReadState 一次读取 memory 三层并组装成 State JSON（供 ReadState action 使用）。
func (s *Store) ReadState(ctx context.Context, id string) ([]byte, error) {
	var facts, ideas, hints []byte
	err := s.pool.QueryRow(ctx,
		`SELECT memory_facts, memory_ideas, memory_hints FROM engagement WHERE id=$1`, id).
		Scan(&facts, &ideas, &hints)
	if err != nil {
		return nil, fmt.Errorf("read state %s: %w", id, err)
	}
	return json.Marshal(State{Facts: facts, Ideas: ideas, Hints: hints})
}

// AppendFact 追加一条事实到 memory_facts。entry 形如 {"category":"evidence|boundary", "content":...}；
// 按 category 推到 evidence 或 boundaries 数组；其余 category 直接报错。
func (s *Store) AppendFact(ctx context.Context, id string, entry []byte) error {
	return s.appendInto(ctx, id, "memory_facts", entry, factCategoryToKey)
}

// AppendIdea 追加一条假设到 memory_ideas.hypotheses。
func (s *Store) AppendIdea(ctx context.Context, id string, entry []byte) error {
	return s.appendInto(ctx, id, "memory_ideas", entry, fixedKey("hypotheses"))
}

// AppendHint 追加一条提示到 memory_hints.hints。
func (s *Store) AppendHint(ctx context.Context, id string, entry []byte) error {
	return s.appendInto(ctx, id, "memory_hints", entry, fixedKey("hints"))
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scan 是 colsSelect 列序的统一反序列化点。
func scan(r scanner, e *Engagement) error {
	return r.Scan(&e.ID, &e.TenantID, &e.Mode, &e.ScopeHost, &e.Status,
		&e.MemoryFacts, &e.MemoryIdeas, &e.MemoryHints,
		&e.CreatedAt, &e.LastActivityAt)
}

// keyFn 把 entry 字节流映射为目标 jsonb 子键名（如 "evidence" / "hypotheses"）。
type keyFn func(entry []byte) (string, error)

// factCategoryToKey 解析 entry.category 并映射到 memory_facts 子键。
func factCategoryToKey(entry []byte) (string, error) {
	var p struct {
		Category string `json:"category"`
	}
	if err := json.Unmarshal(entry, &p); err != nil {
		return "", fmt.Errorf("parse fact entry: %w", err)
	}
	switch p.Category {
	case "evidence":
		return "evidence", nil
	case "boundary":
		return "boundaries", nil
	default:
		return "", fmt.Errorf("invalid fact category %q", p.Category)
	}
}

// fixedKey 返回一个总是产出固定子键的 keyFn（适用于 ideas/hints 这类只有一个数组的列）。
func fixedKey(k string) keyFn {
	return func([]byte) (string, error) { return k, nil }
}

// 最大保留条数；超过则裁剪到末尾 maxEntries 条。
const maxEntries = 100

// appendInto 通用 jsonb 数组追加：col[key] = (col[key] || []) || [entry]，再裁剪到 maxEntries 条末尾。
// 第二个 UPDATE（裁剪）即使失败也不影响主流程（追加已成功）。
func (s *Store) appendInto(ctx context.Context, id, col string, entry []byte, kf keyFn) error {
	key, err := kf(entry)
	if err != nil {
		return err
	}
	// 用 jsonb_set + COALESCE 兼容空对象；jsonb 数组拼接由 || 完成。
	q := fmt.Sprintf(`
		UPDATE engagement
		SET %s = jsonb_set(
			COALESCE(%s, '{}'::jsonb),
			$1,
			COALESCE(%s->$2, '[]'::jsonb) || $3::jsonb
		),
		last_activity_at = now()
		WHERE id=$4`, col, col, col)
	if _, err := s.pool.Exec(ctx, q, "{"+key+"}", key, entry, id); err != nil {
		return fmt.Errorf("append %s: %w", col, err)
	}
	// 滚动裁剪到末尾 maxEntries 条；非关键路径，错误忽略。
	trim := fmt.Sprintf(`
		UPDATE engagement
		SET %s = jsonb_set(%s, $1,
			CASE WHEN jsonb_array_length(%s->$2) > %d
				THEN (
					SELECT COALESCE(jsonb_agg(v), '[]'::jsonb)
					FROM (
						SELECT v FROM jsonb_array_elements(%s->$2) v
						OFFSET GREATEST(jsonb_array_length(%s->$2)-%d, 0)
					) sub
				)
				ELSE %s->$2
			END)
		WHERE id=$3`, col, col, col, maxEntries, col, col, maxEntries, col)
	_, _ = s.pool.Exec(ctx, trim, "{"+key+"}", key, id)
	return nil
}
