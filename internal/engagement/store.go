package engagement

import (
	"context"
	"errors"
	"fmt"

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
const colsSelect = "id, mode, target_host, status, created_at, " +
	"ended_at, error_message, flow_count, finding_count, agent_run_count"

// LookupOrCreateProxy 是 LookupOrCreate 的便利包装。
func (s *Store) LookupOrCreateProxy(ctx context.Context, host string) (string, error) {
	e, err := s.LookupOrCreate(ctx, host, ModeProxy)
	if err != nil {
		return "", err
	}
	return e.ID, nil
}

// LookupOrCreate 返回 host 下当前 active engagement；不存在则懒创建。
func (s *Store) LookupOrCreate(ctx context.Context, host string, mode Mode) (Engagement, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+colsSelect+`
		FROM engagement
		WHERE target_host=$1 AND status='active'
		LIMIT 1`, host)
	var e Engagement
	err := scan(row, &e)
	if err == nil {
		return e, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Engagement{}, fmt.Errorf("lookup engagement: %w", err)
	}
	row = s.pool.QueryRow(ctx, `
		INSERT INTO engagement (mode, target_host, status)
		VALUES ($1,$2,'active')
		RETURNING `+colsSelect, mode, host)
	if err := scan(row, &e); err != nil {
		return Engagement{}, fmt.Errorf("insert engagement: %w", err)
	}
	return e, nil
}

// 列表查询的限制：默认 20，硬上限 200（防 caller 传巨大 limit 拖死 DB）。
const (
	defaultListLimit = 20
	maxListLimit     = 200
)

// List 按 created_at DESC 列出最近的 engagements；host 为空时不过滤；
// limit<=0 时回退到 defaultListLimit（20），>maxListLimit（200）截到 maxListLimit。
//
// 主要给 viewer/前端做下拉列表用：返回全部字段，前端自己挑展示哪些。
func (s *Store) List(ctx context.Context, host string, limit int) ([]Engagement, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	var rows pgx.Rows
	var err error
	if host == "" {
		rows, err = s.pool.Query(ctx,
			"SELECT "+colsSelect+" FROM engagement ORDER BY created_at DESC LIMIT $1",
			limit)
	} else {
		rows, err = s.pool.Query(ctx,
			"SELECT "+colsSelect+" FROM engagement WHERE target_host=$1 ORDER BY created_at DESC LIMIT $2",
			host, limit)
	}
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
	return r.Scan(&e.ID, &e.Mode, &e.TargetHost, &e.Status,
		&e.CreatedAt,
		&e.EndedAt, &e.ErrorMessage,
		&e.FlowCount, &e.FindingCount, &e.AgentRunCount)
}
