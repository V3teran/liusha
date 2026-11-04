package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/auth"
)

// fakeAuthStore 是 AuthStore 的内存实现（路由级单测用）。
//
// 各业务路由测试不关心登录全链路，只关心"带着有效会话的请求能过认证"——
// newTestServer 默认注入本 fake 并预置固定会话 testSessionToken（等价于
// 昔日固定 API key "k" 的角色）；登录/改密/登出全链路由 auth_handler_test
// 走真实 handler + 真实 bcrypt 覆盖。
type fakeAuthStore struct {
	mu       sync.Mutex
	account  *auth.Account
	sessions map[string]auth.Session // 明文 token -> 会话（内存 fake，无需哈希）
	seq      int
	slides   int // SlideSession 调用计数（中间件续期断言用）
}

// 测试账号与预置会话：bcrypt 哈希进程内只算一次（cost 12 约百毫秒级）。
const (
	testAccountName  = "tester"
	testAccountPass  = "test-pass-123"
	testSessionToken = "unit-test-session-token"
)

var testPasswordHashOnce sync.Once
var testPasswordHashValue string

func testPasswordHash() string {
	testPasswordHashOnce.Do(func() {
		h, err := auth.HashPassword(testAccountPass)
		if err != nil {
			panic(err)
		}
		testPasswordHashValue = h
	})
	return testPasswordHashValue
}

func newFakeAuthStore() *fakeAuthStore {
	f := &fakeAuthStore{sessions: map[string]auth.Session{}}
	f.account = &auth.Account{ID: 1, Username: testAccountName, PasswordHash: testPasswordHash()}
	f.seedSession(testSessionToken)
	return f
}


// seedSession 预置一条满期会话（不过期、不触发续期）。
func (f *fakeAuthStore) seedSession(token string) {
	f.sessions[token] = auth.Session{
		TokenHash: token,
		AccountID: f.account.ID,
		Username:  f.account.Username,
		ExpiresAt: time.Now().Add(auth.SessionTTL),
		CreatedAt: time.Now(),
	}
}

// testSessionCookie 返回预置会话的请求 Cookie。
func testSessionCookie() *http.Cookie {
	return &http.Cookie{Name: sessionCookieName, Value: testSessionToken}
}

// AccountByUsername ...
func (f *fakeAuthStore) AccountByUsername(_ context.Context, username string) (auth.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.account == nil || f.account.Username != username {
		return auth.Account{}, auth.ErrAccountNotFound
	}
	return *f.account, nil
}

// CreateSession 签发自增 token。
func (f *fakeAuthStore) CreateSession(_ context.Context, accountID int64) (string, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	token := "fake-token-" + strconv.Itoa(f.seq)
	f.sessions[token] = auth.Session{
		TokenHash: token,
		AccountID: accountID,
		Username:  f.account.Username,
		ExpiresAt: time.Now().Add(auth.SessionTTL),
		CreatedAt: time.Now(),
	}
	return token, time.Now().Add(auth.SessionTTL), nil
}

// SessionByToken 命中且未过期才有效。
func (f *fakeAuthStore) SessionByToken(_ context.Context, token string) (auth.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ss, ok := f.sessions[token]
	if !ok || !time.Now().Before(ss.ExpiresAt) {
		return auth.Session{}, auth.ErrSessionInvalid
	}
	return ss, nil
}

// SlideSession 把会话续到满 TTL。
func (f *fakeAuthStore) SlideSession(_ context.Context, tokenHash string) (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.slides++
	ss, ok := f.sessions[tokenHash]
	if !ok {
		return time.Time{}, auth.ErrSessionInvalid
	}
	ss.ExpiresAt = time.Now().Add(auth.SessionTTL)
	f.sessions[tokenHash] = ss
	return ss.ExpiresAt, nil
}

// DeleteSession 登出。
func (f *fakeAuthStore) DeleteSession(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, token)
	return nil
}

// ReplacePassword 原子改哈希并吊销全部会话（与真实 store 同语义）。
func (f *fakeAuthStore) ReplacePassword(_ context.Context, accountID int64, passwordHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.account == nil || f.account.ID != accountID {
		return auth.ErrAccountNotFound
	}
	f.account.PasswordHash = passwordHash
	for k, ss := range f.sessions {
		if ss.AccountID == accountID {
			delete(f.sessions, k)
		}
	}
	return nil
}
