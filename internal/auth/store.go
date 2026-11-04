package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 会话生命周期：7 天滑动有效期（活跃不登出）+ 自签发起 30 天绝对上限
// （滑动会话不得永生，到期必须重新认证）。两者都在 SessionByToken 的 SQL
// 谓词中判定，不依赖后台清理任务的及时性。
const (
	SessionTTL        = 7 * 24 * time.Hour
	SessionRenewAfter = SessionTTL / 2 // 剩余不足半程时中间件触发续期，写频至多每天一次
	SessionMaxAge     = 30 * 24 * time.Hour
)

// 哨兵错误：handler 据此映射 401/403，其余一律 500。
var (
	ErrAccountNotFound    = errors.New("auth: 账号不存在")
	ErrAlreadyInitialized = errors.New("auth: 管理员已初始化")
	ErrSessionInvalid     = errors.New("auth: 会话无效或已过期")
)

// Account 是控制面登录账号。
type Account struct {
	ID           int64
	Username     string
	PasswordHash string
}

// Session 是一次登录会话。TokenHash 是 Cookie 明文 token 的 SHA-256；
// 明文只在 CreateSession 返回值中存在一次，此后任何路径不再出现。
// Username 经 JOIN 带出，供中间件注入 gin context（/me 与审计 actor 用）。
type Session struct {
	TokenHash string
	AccountID int64
	Username  string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// Store 封装 account / account_session 表的持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// CountAccounts 返回账号数；0 表示尚未初始化（首启向导的判据）。
func (s *Store) CountAccounts(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM account`).Scan(&n); err != nil {
		return 0, fmt.Errorf("auth: count account: %w", err)
	}
	return n, nil
}

// CreateAccountIfAbsent 建立初始管理员：仅当账号表为空时写入（WHERE NOT EXISTS
// 单语句原子判定，并发首启也只有一个成功）。已有账号返回 ErrAlreadyInitialized。
func (s *Store) CreateAccountIfAbsent(ctx context.Context, username, passwordHash string) (Account, error) {
	var a Account
	err := s.pool.QueryRow(ctx, `
		INSERT INTO account (username, password_hash)
		SELECT $1, $2
		WHERE NOT EXISTS (SELECT 1 FROM account)
		RETURNING id, username, password_hash`,
		username, passwordHash,
	).Scan(&a.ID, &a.Username, &a.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrAlreadyInitialized
	}
	if err != nil {
		return Account{}, fmt.Errorf("auth: create account: %w", err)
	}
	return a, nil
}

// AccountByUsername 按用户名取账号；不存在返回 ErrAccountNotFound。
func (s *Store) AccountByUsername(ctx context.Context, username string) (Account, error) {
	var a Account
	err := s.pool.QueryRow(ctx, `
		SELECT id, username, password_hash FROM account WHERE username = $1`,
		username,
	).Scan(&a.ID, &a.Username, &a.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrAccountNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("auth: account by username: %w", err)
	}
	return a, nil
}

// ReplacePassword 原子地改哈希并吊销该账号全部会话：一次凭据变更是一个操作，
// 不允许"哈希已换、旧会话仍活"的中间态（进程在两条语句间崩溃即出现）。
func (s *Store) ReplacePassword(ctx context.Context, accountID int64, passwordHash string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("auth: replace password begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE account SET password_hash = $1, updated_at = now() WHERE id = $2`,
		passwordHash, accountID,
	); err != nil {
		return fmt.Errorf("auth: replace password: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM account_session WHERE account_id = $1`, accountID,
	); err != nil {
		return fmt.Errorf("auth: revoke sessions: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("auth: replace password commit: %w", err)
	}
	return nil
}

// CreateSession 为账号签发新会话，返回值 token 是仅此刻可见的明文（写入 Cookie）。
// 顺带清理已死会话行（滑动过期或超绝对上限）——这是唯一无界增长的来源（登录即新增行），
// 登出/改密只删当时的活跃行；best-effort，清理失败不阻断登录。
func (s *Store) CreateSession(ctx context.Context, accountID int64) (token string, exp time.Time, err error) {
	_, _ = s.pool.Exec(ctx, `
		DELETE FROM account_session
		WHERE expires_at < now() OR created_at < now() - make_interval(secs => $1)`,
		int64(SessionMaxAge.Seconds()),
	)
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("auth: 生成会话 token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	exp = time.Now().Add(SessionTTL)
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO account_session (token_hash, account_id, expires_at)
		VALUES ($1, $2, $3)`,
		hex.EncodeToString(sum[:]), accountID, exp,
	); err != nil {
		return "", time.Time{}, fmt.Errorf("auth: create session: %w", err)
	}
	return token, exp, nil
}

// SessionByToken 校验并返回会话：token 哈希命中、滑动未过期、绝对上限未过，
// 三者同一谓词判定；任一不满足返回 ErrSessionInvalid。
func (s *Store) SessionByToken(ctx context.Context, token string) (Session, error) {
	sum := sha256.Sum256([]byte(token))
	var ss Session
	err := s.pool.QueryRow(ctx, `
		SELECT s.token_hash, s.account_id, a.username, s.expires_at, s.created_at
		FROM account_session s
		JOIN account a ON a.id = s.account_id
		WHERE s.token_hash = $1
		  AND s.expires_at > now()
		  AND s.created_at > now() - make_interval(secs => $2)`,
		hex.EncodeToString(sum[:]), int64(SessionMaxAge.Seconds()),
	).Scan(&ss.TokenHash, &ss.AccountID, &ss.Username, &ss.ExpiresAt, &ss.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrSessionInvalid
	}
	if err != nil {
		return Session{}, fmt.Errorf("auth: session by token: %w", err)
	}
	return ss, nil
}

// SlideSession 把会话续到 now+SessionTTL，且不越过 created_at 起的绝对上限；
// 返回续期后的准确过期时间（Cookie MaxAge 与之同步）。
func (s *Store) SlideSession(ctx context.Context, tokenHash string) (time.Time, error) {
	var exp time.Time
	err := s.pool.QueryRow(ctx, `
		UPDATE account_session
		SET expires_at = LEAST(now() + make_interval(secs => $1),
		                       created_at + make_interval(secs => $2)),
		    renewed_at = now()
		WHERE token_hash = $3
		RETURNING expires_at`,
		int64(SessionTTL.Seconds()), int64(SessionMaxAge.Seconds()), tokenHash,
	).Scan(&exp)
	if err != nil {
		return time.Time{}, fmt.Errorf("auth: slide session: %w", err)
	}
	return exp, nil
}

// DeleteSession 登出：删除当前会话行（幂等，不存在也返回 nil）。
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	sum := sha256.Sum256([]byte(token))
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM account_session WHERE token_hash = $1`,
		hex.EncodeToString(sum[:]),
	); err != nil {
		return fmt.Errorf("auth: delete session: %w", err)
	}
	return nil
}
