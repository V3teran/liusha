package engagement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 engagement 表的所有持久化操作。
//
// 短期工作笔记 notes 已迁出 PG（迁至 internal/notes 包 Redis）。本 Store 不再
// 持有 notes 相关字段或方法，专注 engagement 行级生命周期与计数。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT 路径的统一列序，与 scan() 的字段顺序一一对应。
const colsSelect = "id, mode, scope, status, created_at, expires_at, " +
	"ended_at, error_message, flow_count, finding_count, agent_run_count"

// passiveDefaultScope 是 passive session 的默认 scope（接受任意 host 流量）。
var passiveDefaultScope = json.RawMessage(`{"any":true}`)

// LookupActivePassive 找当前 active passive session。
// 不存在时返回 (Engagement{}, false, nil)，非空错误才表示真异常。
//
// 唯一索引 engagement_active_passive_uniq 保证最多 1 行。
func (s *Store) LookupActivePassive(ctx context.Context) (Engagement, bool, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+colsSelect+`
		FROM engagement
		WHERE status='active' AND mode='passive'
		LIMIT 1`)
	var e Engagement
	err := scan(row, &e)
	if errors.Is(err, pgx.ErrNoRows) {
		return Engagement{}, false, nil
	}
	if err != nil {
		return Engagement{}, false, fmt.Errorf("lookup active passive: %w", err)
	}
	return e, true, nil
}

// CreatePassiveSession 建一个新的 passive session。expires_at = now + ttl。
//
// 唯一索引 engagement_active_passive_uniq 会拒绝并发创建：若已有 active passive
// session，本调用会返回唯一约束错误。caller（Rotator）需先 LookupActivePassive
// 判断，必要时先 Abort 旧的再调本方法。
func (s *Store) CreatePassiveSession(ctx context.Context, ttl time.Duration) (Engagement, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO engagement (mode, scope, status, expires_at)
		VALUES ('passive', $1, 'active', now() + ($2::text)::interval)
		RETURNING `+colsSelect, passiveDefaultScope, fmt.Sprintf("%d seconds", int(ttl.Seconds())))
	var e Engagement
	if err := scan(row, &e); err != nil {
		return Engagement{}, fmt.Errorf("create passive session: %w", err)
	}
	return e, nil
}

// LookupOrCreatePassiveSession 是业务入口便利方法：找当前 active passive session，
// 不存在则建新。给 httpapi passiveScanHandler / 其他业务入口用；
// Rotator 内部不应使用本方法（无法判断是否需要轮转）。
func (s *Store) LookupOrCreatePassiveSession(ctx context.Context, ttl time.Duration) (Engagement, error) {
	if eng, ok, err := s.LookupActivePassive(ctx); err != nil {
		return Engagement{}, err
	} else if ok {
		return eng, nil
	}
	return s.CreatePassiveSession(ctx, ttl)
}

// CreateActiveSession 建一个 active engagement——主动扫描场景。
//
// scope 由 caller 序列化好（标准形态 {"brief":"..."}）。
// 不设 expires_at：active 任务跑完即终态，无时间窗轮转。
// 不受 engagement_active_passive_uniq 唯一索引限制（该索引 WHERE mode='passive'），
// 可并发创建多个 active engagement。
func (s *Store) CreateActiveSession(ctx context.Context, scope json.RawMessage) (Engagement, error) {
	if len(scope) == 0 {
		return Engagement{}, fmt.Errorf("create active session: scope 必填")
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO engagement (mode, scope, status)
		VALUES ('active', $1, 'active')
		RETURNING `+colsSelect, []byte(scope))
	var e Engagement
	if err := scan(row, &e); err != nil {
		return Engagement{}, fmt.Errorf("create active session: %w", err)
	}
	return e, nil
}

// 列表查询的限制：默认 20，硬上限 200（防 caller 传巨大 limit 拖死 DB）。
const (
	defaultListLimit = 20
	maxListLimit     = 200
)

// List 按 created_at DESC 列出最近的 engagements。
// limit<=0 时回退到 defaultListLimit（20），>maxListLimit（200）截到 maxListLimit。
// 按 host 查找走 finding/flow 等子资源（它们都按 host 索引）。
func (s *Store) List(ctx context.Context, limit int) ([]Engagement, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	rows, err := s.pool.Query(ctx,
		"SELECT "+colsSelect+" FROM engagement ORDER BY created_at DESC LIMIT $1",
		limit)
	if err != nil {
		return nil, fmt.Errorf("list engagements: %w", err)
	}
	defer rows.Close()

	var out []Engagement
	for rows.Next() {
		var e Engagement
		if err := scan(rows, &e); err != nil {
			return nil, fmt.Errorf("scan engagement: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
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
			flow_count=(SELECT count(*) FROM http_flow WHERE engagement_id=$2),
			finding_count=(SELECT count(*) FROM finding   WHERE engagement_id=$2),
			agent_run_count=(SELECT count(*) FROM agent_run WHERE engagement_id=$2)
		WHERE id=$2`, errMsg, id)
	if err != nil {
		return fmt.Errorf("abort engagement %s: %w", id, err)
	}
	return nil
}

// IncrementFlowCount / IncrementFindingCount / IncrementAgentRunCount 用于
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

func (s *Store) IncrementAgentRunCount(ctx context.Context, id string, n int) error {
	return s.incrementCounter(ctx, id, "agent_run_count", n)
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

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scan 是 colsSelect 列序的统一反序列化点。
func scan(r scanner, e *Engagement) error {
	return r.Scan(&e.ID, &e.Mode, &e.Scope, &e.Status,
		&e.CreatedAt, &e.ExpiresAt,
		&e.EndedAt, &e.ErrorMessage,
		&e.FlowCount, &e.FindingCount, &e.AgentRunCount)
}
