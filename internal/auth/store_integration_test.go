//go:build integration

package auth

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(dbtest.NewPgPool(t))
}

// mustAccount 建立初始管理员（哈希用真实 bcrypt，走生产同路径）。
func mustAccount(t *testing.T, s *Store) Account {
	t.Helper()
	hash, err := HashPassword("s3cret-password")
	require.NoError(t, err)
	a, err := s.CreateAccountIfAbsent(context.Background(), "admin", hash)
	require.NoError(t, err)
	return a
}

// TestStore_CreateAccountIfAbsent 验证：账号表为空才可建号，第二次返回 ErrAlreadyInitialized（原子防并发首启）。
func TestStore_CreateAccountIfAbsent(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)

	n, err := s.CountAccounts(ctx)
	require.NoError(t, err)
	assert.Zero(t, n, "初始应为零账号")

	a := mustAccount(t, s)

	_, err = s.CreateAccountIfAbsent(ctx, "other", "hash-would-be-invalid")
	assert.ErrorIs(t, err, ErrAlreadyInitialized)

	got, err := s.AccountByUsername(ctx, a.Username)
	require.NoError(t, err)
	assert.Equal(t, a.ID, got.ID)

	_, err = s.AccountByUsername(ctx, "nobody")
	assert.ErrorIs(t, err, ErrAccountNotFound)
}

// TestStore_SessionLifecycle 验证：签发 → 校验通过 → 登出删除 → 再校验失败；库中不落明文 token。
func TestStore_SessionLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	a := mustAccount(t, s)

	token, exp, err := s.CreateSession(ctx, a.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.True(t, time.Until(exp) > SessionTTL-time.Minute)

	ss, err := s.SessionByToken(ctx, token)
	require.NoError(t, err)
	assert.Equal(t, a.ID, ss.AccountID)
	assert.Equal(t, a.Username, ss.Username)
	assert.NotContains(t, ss.TokenHash, token, "库中只存哈希，不得出现明文 token")

	require.NoError(t, s.DeleteSession(ctx, token))
	_, err = s.SessionByToken(ctx, token)
	assert.ErrorIs(t, err, ErrSessionInvalid, "登出后会话立即失效")
}

// TestStore_SessionExpiry 验证：过期与超绝对上限的会话都被谓词拒绝。
func TestStore_SessionExpiry(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	a := mustAccount(t, s)

	token, _, err := s.CreateSession(ctx, a.ID)
	require.NoError(t, err)
	ss, err := s.SessionByToken(ctx, token)
	require.NoError(t, err)

	// 手工把 expires_at 拨到过去 → 滑动过期拒绝。
	_, err = s.pool.Exec(ctx, `UPDATE account_session SET expires_at = now() - interval '1 min' WHERE token_hash = $1`, ss.TokenHash)
	require.NoError(t, err)
	_, err = s.SessionByToken(ctx, token)
	assert.ErrorIs(t, err, ErrSessionInvalid)

	// 手工把 created_at 拨回 31 天前 → 绝对上限拒绝（expires_at 仍有效）。
	_, err = s.pool.Exec(ctx, `UPDATE account_session SET created_at = now() - interval '31 days', expires_at = now() + interval '1 hour' WHERE token_hash = $1`, ss.TokenHash)
	require.NoError(t, err)
	_, err = s.SessionByToken(ctx, token)
	assert.ErrorIs(t, err, ErrSessionInvalid)
}

// TestStore_SlideSession 验证：续期写 expires_at=now+TTL 且不越过绝对上限。
func TestStore_SlideSession(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	a := mustAccount(t, s)

	token, _, err := s.CreateSession(ctx, a.ID)
	require.NoError(t, err)
	ss, _ := s.SessionByToken(ctx, token)

	exp, err := s.SlideSession(ctx, ss.TokenHash)
	require.NoError(t, err)
	assert.True(t, time.Until(exp) > SessionTTL-time.Minute, "常规续期应得满 TTL")

	// 把 created_at 拨回 29 天前：续期只能到 created_at+30d，不足满 TTL。
	_, err = s.pool.Exec(ctx, `UPDATE account_session SET created_at = now() - interval '29 days' WHERE token_hash = $1`, ss.TokenHash)
	require.NoError(t, err)
	exp, err = s.SlideSession(ctx, ss.TokenHash)
	require.NoError(t, err)
	assert.Less(t, time.Until(exp), time.Duration(24*time.Hour), "绝对上限邻近时不得续满 TTL")
}

// TestStore_ReplacePassword 验证：单事务改哈希 + 吊销全部会话（改密全员下线路径）。
func TestStore_ReplacePassword(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	a := mustAccount(t, s)

	t1, _, err := s.CreateSession(ctx, a.ID)
	require.NoError(t, err)
	t2, _, err := s.CreateSession(ctx, a.ID)
	require.NoError(t, err)

	newHash, err := HashPassword("new-s3cret-password")
	require.NoError(t, err)
	require.NoError(t, s.ReplacePassword(ctx, a.ID, newHash))

	_, err = s.SessionByToken(ctx, t1)
	assert.ErrorIs(t, err, ErrSessionInvalid, "改密后全部会话应被吊销")
	_, err = s.SessionByToken(ctx, t2)
	assert.ErrorIs(t, err, ErrSessionInvalid)

	got, err := s.AccountByUsername(ctx, a.Username)
	require.NoError(t, err)
	assert.NoError(t, CheckPassword(got.PasswordHash, "new-s3cret-password"))
	assert.Error(t, CheckPassword(got.PasswordHash, "s3cret-password"))
}
