package passivesession

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 passive_session 表的所有持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT 路径的统一列序，与 scan() 字段顺序一一对应。
const colsSelect = "id, host, status, created_at, expires_at, " +
	"ended_at, error_message, flow_count, finding_count, agent_run_count"

// LookupActiveByHost 找指定 host 的 active session。
// 不存在时返回 (Session{}, false, nil)，非空错误才表示真异常。
//
// 唯一索引 passive_session_active_host_uniq 保证 1 host 最多 1 行 active。
func (s *Store) LookupActiveByHost(ctx context.Context, host string) (Session, bool, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+colsSelect+`
		FROM passive_session
		WHERE status='active' AND host=$1
		LIMIT 1`, host)
	var sess Session
	err := scan(row, &sess)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, fmt.Errorf("lookup active passive %s: %w", host, err)
	}
	return sess, true, nil
}

// Create 建一个新 passive session。expires_at = now + ttl。
//
// 唯一索引 passive_session_active_host_uniq 会拒绝同 host 并发创建。
// caller（Rotator）需先 LookupActiveByHost 判断，必要时先 Abort 旧的再调本方法。
func (s *Store) Create(ctx context.Context, host string, ttl time.Duration) (Session, error) {
	if host == "" {
		return Session{}, fmt.Errorf("create passive session: host 必填")
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO passive_session (host, status, expires_at)
		VALUES ($1, 'active', now() + ($2::text)::interval)
		RETURNING `+colsSelect, host, fmt.Sprintf("%d seconds", int(ttl.Seconds())))
	var sess Session
	if err := scan(row, &sess); err != nil {
		return Session{}, fmt.Errorf("create passive session: %w", err)
	}
	return sess, nil
}

// LookupOrCreate 业务入口便利方法：找 host 的 active session，不存在则建新。
func (s *Store) LookupOrCreate(ctx context.Context, host string, ttl time.Duration) (Session, error) {
	if sess, ok, err := s.LookupActiveByHost(ctx, host); err != nil {
		return Session{}, err
	} else if ok {
		return sess, nil
	}
	return s.Create(ctx, host, ttl)
}

const (
	defaultListLimit = 20
	maxListLimit     = 200
)

// List 按 created_at DESC 列出最近的 passive sessions。
func (s *Store) List(ctx context.Context, limit int) ([]Session, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	rows, err := s.pool.Query(ctx,
		"SELECT "+colsSelect+" FROM passive_session ORDER BY created_at DESC LIMIT $1",
		limit)
	if err != nil {
		return nil, fmt.Errorf("list passive sessions: %w", err)
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		var sess Session
		if err := scan(rows, &sess); err != nil {
			return nil, fmt.Errorf("scan passive session: %w", err)
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

// GetByID 按主键读取（不限 status）。
func (s *Store) GetByID(ctx context.Context, id string) (Session, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+colsSelect+" FROM passive_session WHERE id=$1", id)
	var sess Session
	if err := scan(row, &sess); err != nil {
		return Session{}, fmt.Errorf("get passive session %s: %w", id, err)
	}
	return sess, nil
}

// Abort 把 session 置为 aborted（释放 active host 唯一约束位），同时写入
// ended_at / error_message，并子查询重算 *_count 三字段做精确兜底。
//
// 注意 polymorphic owner：finding / agent_run 走 (owner_type, owner_id) 过滤；
// http_flow 走专属 passive_session_id 直 FK 列。
func (s *Store) Abort(ctx context.Context, id, errMsg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE passive_session SET
			status='aborted',
			ended_at=now(),
			error_message=$1,
			flow_count=(SELECT count(*) FROM http_flow
			            WHERE passive_session_id=$2),
			finding_count=(SELECT count(*) FROM finding
			               WHERE owner_type='passive_session' AND owner_id=$2),
			agent_run_count=(SELECT count(*) FROM agent_run
			                 WHERE owner_type='passive_session' AND owner_id=$2)
		WHERE id=$2`, errMsg, id)
	if err != nil {
		return fmt.Errorf("abort passive session %s: %w", id, err)
	}
	return nil
}

// IncrementFlowCount / IncrementFindingCount / IncrementAgentRunCount 用于
// best-effort 维护运行期实时计数；失败仅 log warn 不阻塞业务，Abort 精确兜底。
func (s *Store) IncrementFlowCount(ctx context.Context, id string, n int) error {
	return s.incrementCounter(ctx, id, "flow_count", n)
}

func (s *Store) IncrementFindingCount(ctx context.Context, id string, n int) error {
	return s.incrementCounter(ctx, id, "finding_count", n)
}

func (s *Store) IncrementAgentRunCount(ctx context.Context, id string, n int) error {
	return s.incrementCounter(ctx, id, "agent_run_count", n)
}

// incrementCounter 是 3 个 IncrementXxx 的共用实现；col 由调用方控制（白名单值），
// 不接受用户输入，无 SQL 注入风险。
func (s *Store) incrementCounter(ctx context.Context, id, col string, n int) error {
	if n == 0 {
		return nil
	}
	q := fmt.Sprintf(`UPDATE passive_session SET %s = %s + $1 WHERE id=$2`, col, col)
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
func scan(r scanner, s *Session) error {
	return r.Scan(&s.ID, &s.Host, &s.Status,
		&s.CreatedAt, &s.ExpiresAt,
		&s.EndedAt, &s.ErrorMessage,
		&s.FlowCount, &s.FindingCount, &s.AgentRunCount)
}
