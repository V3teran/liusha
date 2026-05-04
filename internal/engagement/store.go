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
// v0015：新增 ended_at / error_message / *_count 字段。
const colsSelect = "id, tenant_id, mode, scope_host, status, memory_notes, created_at, " +
	"ended_at, error_message, flow_count, finding_count, react_run_count"

// LookupOrCreateProxy 是 LookupOrCreate 的便利包装。
func (s *Store) LookupOrCreateProxy(ctx context.Context, host string) (string, error) {
	e, err := s.LookupOrCreate(ctx, "default", host, ModeProxy)
	if err != nil {
		return "", err
	}
	return e.ID, nil
}

// LookupOrCreate 返回 (tenant, host) 下当前 active engagement；不存在则懒创建。
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

// Abort 把 engagement 置为 aborted（释放 active 唯一约束位），同时写入 ended_at /
// error_message，并子查询重算 *_count 三字段做精确兜底（active 期间增量维护可能漂移）。
//
// errMsg 空字符串表示正常结束（无错误）；非空表示因失败而中止。
func (s *Store) Abort(ctx context.Context, id, errMsg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE engagement SET
			status='aborted',
			ended_at=now(),
			error_message=$1,
			flow_count=(SELECT count(*) FROM http_flow      WHERE engagement_id=$2),
			finding_count=(SELECT count(*) FROM vuln_finding WHERE engagement_id=$2),
			react_run_count=(SELECT count(*) FROM react_run  WHERE engagement_id=$2)
		WHERE id=$2`, errMsg, id)
	if err != nil {
		return fmt.Errorf("abort engagement %s: %w", id, err)
	}
	return nil
}

// IncrementFlowCount / IncrementFindingCount / IncrementReactRunCount 用于
// vulnfinding/flow/reactrun 写路径上 best-effort 维护 active 期间的实时计数。
//
// 单条 +1 路径，调用方若失败仅日志（与 LLM instrument 同模式）；
// Abort 时会用 SELECT count(*) 重算精确兜底，因此偶尔漂移可容忍。
func (s *Store) IncrementFlowCount(ctx context.Context, id string, n int) error {
	return s.incrementCounter(ctx, id, "flow_count", n)
}

func (s *Store) IncrementFindingCount(ctx context.Context, id string, n int) error {
	return s.incrementCounter(ctx, id, "finding_count", n)
}

func (s *Store) IncrementReactRunCount(ctx context.Context, id string, n int) error {
	return s.incrementCounter(ctx, id, "react_run_count", n)
}

// incrementCounter 是 3 个 IncrementXxx 的共用实现；col 由调用方控制（白名单内值），
// 不接受用户输入，无 SQL 注入风险。
func (s *Store) incrementCounter(ctx context.Context, id, col string, n int) error {
	if n == 0 {
		return nil
	}
	q := fmt.Sprintf(`UPDATE engagement SET %s = %s + $1 WHERE id=$2`, col, col)
	if _, err := s.pool.Exec(ctx, q, n, id); err != nil {
		return fmt.Errorf("increment %s: %w", col, err)
	}
	return nil
}

// ReadState 一次读取 memory_notes（不过滤；admin/debug 用途）。
//
// 子 ReAct 应改用 ReadStateScoped 拿到带 NotesLimit 截断的视图。
func (s *Store) ReadState(ctx context.Context, id string) ([]byte, error) {
	return s.ReadStateScoped(ctx, id, ReadOpts{})
}

// ReadStateScoped 读 memory_notes 并按 NotesLimit 截断。
//
// v1.2 收尾：notes 是 engagement-scope 共享（per host），所有 task 共看；
// done_validator 凭 entry.task_id 字段判定本 task 是否写过（不在此过滤）。
//
// jsonb 已被 appendInto 滚动到 maxEntries 内，读全量然后 Go 侧截断。
func (s *Store) ReadStateScoped(ctx context.Context, id string, opts ReadOpts) ([]byte, error) {
	var notes []byte
	err := s.pool.QueryRow(ctx,
		`SELECT memory_notes FROM engagement WHERE id=$1`, id).
		Scan(&notes)
	if err != nil {
		return nil, fmt.Errorf("read state %s: %w", id, err)
	}

	notes = trimNotes(notes, defaultIfZero(opts.NotesLimit, defaultNotesLimit))
	return json.Marshal(State{Notes: notes})
}

// AppendNote 追加一条 note 到 memory_notes.notes 数组。
//
// entry 形如 {"kind":"observation|hypothesis|boundary","content":"...","status":"...","task_id":"...","scope":"engagement"}；
// store 不解析也不强制结构——take_note 工具层已 enum 校验。
func (s *Store) AppendNote(ctx context.Context, id string, entry []byte) error {
	return s.appendInto(ctx, id, "memory_notes", entry, fixedKey("notes"))
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scan 是 colsSelect 列序的统一反序列化点。
func scan(r scanner, e *Engagement) error {
	return r.Scan(&e.ID, &e.TenantID, &e.Mode, &e.ScopeHost, &e.Status,
		&e.MemoryNotes, &e.CreatedAt,
		&e.EndedAt, &e.ErrorMessage,
		&e.FlowCount, &e.FindingCount, &e.ReactRunCount)
}

// keyFn 把 entry 字节流映射为目标 jsonb 子键名。
type keyFn func(entry []byte) (string, error)

// fixedKey 返回总是产出固定子键的 keyFn。
func fixedKey(k string) keyFn {
	return func([]byte) (string, error) { return k, nil }
}

// 最大保留 notes 条数；超过则裁剪到末尾 maxEntries 条。
const maxEntries = 200

// 默认 ReadStateScoped 截断值；0 入参时使用。负数表示不截断。
const defaultNotesLimit = 100

func defaultIfZero(v, d int) int {
	if v == 0 {
		return d
	}
	return v
}

// appendInto 通用 jsonb 数组追加：col[key] = (col[key] || []) || [entry]，再裁剪到 maxEntries 条末尾。
func (s *Store) appendInto(ctx context.Context, id, col string, entry []byte, kf keyFn) error {
	key, err := kf(entry)
	if err != nil {
		return err
	}
	q := fmt.Sprintf(`
		UPDATE engagement
		SET %s = jsonb_set(
			COALESCE(%s, '{}'::jsonb),
			$1,
			COALESCE(%s->$2, '[]'::jsonb) || $3::jsonb
		)
		WHERE id=$4`, col, col, col)
	if _, err := s.pool.Exec(ctx, q, "{"+key+"}", key, entry, id); err != nil {
		return fmt.Errorf("append %s: %w", col, err)
	}
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

// trimNotes 把 {notes:[...]} 的 notes 数组截到末尾 limit 条；limit ≤ 0 视为不截断。
//
// 容错：raw 解析失败原值返回，保证 ReadStateScoped 不崩。
func trimNotes(raw []byte, limit int) []byte {
	if len(raw) == 0 || limit <= 0 {
		return raw
	}
	var obj map[string][]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return raw
	}
	arr := obj["notes"]
	if len(arr) > limit {
		arr = arr[len(arr)-limit:]
		obj["notes"] = arr
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return raw
	}
	return out
}
