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
// error_message 用 COALESCE 把 NULL 折成空串（Session.ErrorMessage 是 string 不接 NULL）。
const colsSelect = "id, host, status, created_at, expires_at, " +
	"ended_at, COALESCE(error_message, ''), conversation_id"

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

// SetConversationID 绑定 passive 会话的对话流 id（阶段2）。建会话后回填，幂等。
func (s *Store) SetConversationID(ctx context.Context, id, convID string) error {
	_, err := s.pool.Exec(ctx,
		"UPDATE passive_session SET conversation_id=$1 WHERE id=$2", convID, id)
	if err != nil {
		return fmt.Errorf("set passive session %s conversation: %w", id, err)
	}
	return nil
}

// BumpExpiry 把 active session 的 expires_at 推到 now()+idle（阶段4 滑动 idle 截止）。
//
// expires_at 语义从「创建绝对截止」改为「滑动 idle 截止」：每次流量/插话续命，持续交互永不过期，
// 真闲置 idle 后才被 Sweep(expires_at < now()) 释放——实现「记忆永久 + 资源 idle 释放」。
// 仅作用于 active 行（已 aborted 的不复活）。
func (s *Store) BumpExpiry(ctx context.Context, id string, idle time.Duration) error {
	_, err := s.pool.Exec(ctx,
		"UPDATE passive_session SET expires_at=now() + ($1::text)::interval WHERE id=$2 AND status='active'",
		fmt.Sprintf("%d seconds", int(idle.Seconds())), id)
	if err != nil {
		return fmt.Errorf("bump passive session %s expiry: %w", id, err)
	}
	return nil
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

// GetByConversationID 按绑定的对话流 id 反查 passive 会话（阶段2 插话判别用）。
// 不存在时返回 (Session{}, false, nil)——调用方据此判定该对话非 passive（走 active 分支）。
func (s *Store) GetByConversationID(ctx context.Context, convID string) (Session, bool, error) {
	if convID == "" {
		return Session{}, false, nil
	}
	row := s.pool.QueryRow(ctx, "SELECT "+colsSelect+" FROM passive_session WHERE conversation_id=$1 LIMIT 1", convID)
	var sess Session
	err := scan(row, &sess)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, fmt.Errorf("get passive session by conversation %s: %w", convID, err)
	}
	return sess, true, nil
}

// Abort 把 session 置为 aborted（释放 active host 唯一约束位），同时写入
// Abort 把 active session 推进到 aborted 终态：写 status / ended_at / error_message。
// 0042 后无 *_count 兜底——读路径直接 SELECT count(*) FROM finding/... 即可。
func (s *Store) Abort(ctx context.Context, id, errMsg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE passive_session SET
			status='aborted',
			ended_at=now(),
			error_message=$1
		WHERE id=$2`, errMsg, id)
	if err != nil {
		return fmt.Errorf("abort passive session %s: %w", id, err)
	}
	return nil
}

// Sweep 关闭所有 expires_at 已过期的 active session（status → aborted）。
// 与"懒轮换"（流量进来时 LookupOrCreate 检查 host 已有 active）互补——无流量场景下
// 也能保证「TTL 一到必关」，避免 PG 堆积陈旧 active 行 + viewer 看僵尸 session。
//
// 返回本次扫到的过期 session 数（已 abort）。
func (s *Store) Sweep(ctx context.Context) (int, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE passive_session SET
			status='aborted',
			ended_at=now(),
			error_message='expired'
		WHERE status='active' AND expires_at < now()`)
	if err != nil {
		return 0, fmt.Errorf("sweep passive_session: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scan 是 colsSelect 列序的统一反序列化点。
func scan(r scanner, s *Session) error {
	return r.Scan(&s.ID, &s.Host, &s.Status,
		&s.CreatedAt, &s.ExpiresAt,
		&s.EndedAt, &s.ErrorMessage, &s.ConversationID)
}
