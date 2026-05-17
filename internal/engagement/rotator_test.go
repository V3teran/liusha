package engagement

import (
	"context"
	"errors"
	"testing"
	"time"
)

// stubStore 是 engStore 接口的内存实现，用于 Rotator 单测。
type stubStore struct {
	// LookupActivePassive 返回值
	activeEng Engagement
	hasActive bool
	lookupErr error

	// CreatePassiveSession 行为
	createdEng Engagement
	createErr  error
	createTTLs []time.Duration

	// Abort 行为
	abortIDs []string
	abortErr error
}

func (s *stubStore) LookupActivePassive(_ context.Context) (Engagement, bool, error) {
	return s.activeEng, s.hasActive, s.lookupErr
}

func (s *stubStore) CreatePassiveSession(_ context.Context, ttl time.Duration) (Engagement, error) {
	s.createTTLs = append(s.createTTLs, ttl)
	if s.createErr != nil {
		return Engagement{}, s.createErr
	}
	return s.createdEng, nil
}

func (s *stubStore) Abort(_ context.Context, id, _ string) error {
	s.abortIDs = append(s.abortIDs, id)
	return s.abortErr
}

// fixedNow 固定时间钩，避免依赖墙钟。
func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

// ----- EnsurePassiveSession -----

func TestEnsurePassiveSession_NoActive_CreatesNew(t *testing.T) {
	s := &stubStore{
		hasActive:  false,
		createdEng: Engagement{ID: "new-1"},
	}
	r := NewRotator(s, RotateLimits{MaxAge: 2 * time.Hour})
	id, err := r.EnsurePassiveSession(context.Background())
	if err != nil || id != "new-1" {
		t.Fatalf("want new-1/no-err, got %q err=%v", id, err)
	}
	if len(s.abortIDs) != 0 {
		t.Fatalf("不该 abort，实际 %v", s.abortIDs)
	}
	if len(s.createTTLs) != 1 || s.createTTLs[0] != 2*time.Hour {
		t.Fatalf("应建一次新 session 用 limits.MaxAge=2h，实际 %v", s.createTTLs)
	}
}

func TestEnsurePassiveSession_ActiveNotExpired_ReturnsExisting(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	futureExp := now.Add(10 * time.Hour)
	s := &stubStore{
		hasActive: true,
		activeEng: Engagement{ID: "old-1", ExpiresAt: &futureExp},
	}
	r := NewRotator(s, RotateLimits{MaxAge: 24 * time.Hour})
	r.now = fixedNow(now)
	id, err := r.EnsurePassiveSession(context.Background())
	if err != nil || id != "old-1" {
		t.Fatalf("未过期应返旧 id old-1，实际 %q err=%v", id, err)
	}
	if len(s.abortIDs) != 0 || len(s.createTTLs) != 0 {
		t.Fatalf("未过期不应 abort/create，实际 abort=%v create=%v", s.abortIDs, s.createTTLs)
	}
}

func TestEnsurePassiveSession_ActiveExpired_Rotates(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	pastExp := now.Add(-1 * time.Hour) // 已过期 1h
	s := &stubStore{
		hasActive:  true,
		activeEng:  Engagement{ID: "old-2", ExpiresAt: &pastExp},
		createdEng: Engagement{ID: "new-2"},
	}
	r := NewRotator(s, RotateLimits{MaxAge: 24 * time.Hour})
	r.now = fixedNow(now)
	id, err := r.EnsurePassiveSession(context.Background())
	if err != nil || id != "new-2" {
		t.Fatalf("过期应轮转返 new-2，实际 %q err=%v", id, err)
	}
	if len(s.abortIDs) != 1 || s.abortIDs[0] != "old-2" {
		t.Fatalf("应 abort old-2，实际 %v", s.abortIDs)
	}
	if len(s.createTTLs) != 1 {
		t.Fatalf("应建 1 次新 session，实际 %d", len(s.createTTLs))
	}
}

// ----- Sweep -----

func TestSweep_NoActive_Noop(t *testing.T) {
	s := &stubStore{hasActive: false}
	r := NewRotator(s, RotateLimits{MaxAge: 24 * time.Hour})
	n, err := r.Sweep(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("无 active 应返 (0,nil)，实际 (%d, %v)", n, err)
	}
	if len(s.abortIDs) != 0 {
		t.Fatalf("不该 abort，实际 %v", s.abortIDs)
	}
}

func TestSweep_ActiveNotExpired_Noop(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	future := now.Add(5 * time.Hour)
	s := &stubStore{
		hasActive: true,
		activeEng: Engagement{ID: "live", ExpiresAt: &future},
	}
	r := NewRotator(s, RotateLimits{MaxAge: 24 * time.Hour})
	r.now = fixedNow(now)
	n, err := r.Sweep(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("未过期应返 (0,nil)，实际 (%d, %v)", n, err)
	}
	if len(s.abortIDs) != 0 {
		t.Fatalf("未过期不该 abort，实际 %v", s.abortIDs)
	}
}

func TestSweep_ActiveExpired_Aborts(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	past := now.Add(-2 * time.Hour)
	s := &stubStore{
		hasActive: true,
		activeEng: Engagement{ID: "stale", ExpiresAt: &past},
	}
	r := NewRotator(s, RotateLimits{MaxAge: 24 * time.Hour})
	r.now = fixedNow(now)
	n, err := r.Sweep(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("过期应 abort 1 个，实际 (%d, %v)", n, err)
	}
	if len(s.abortIDs) != 1 || s.abortIDs[0] != "stale" {
		t.Fatalf("应 abort stale，实际 %v", s.abortIDs)
	}
	// Sweep 不主动建新——留给下条流量懒触发
	if len(s.createTTLs) != 0 {
		t.Fatalf("Sweep 不应主动 create，实际 %v", s.createTTLs)
	}
}

func TestSweep_LookupErr_PropagatesErr(t *testing.T) {
	s := &stubStore{lookupErr: errors.New("db boom")}
	r := NewRotator(s, RotateLimits{MaxAge: 24 * time.Hour})
	n, err := r.Sweep(context.Background())
	if err == nil || n != 0 {
		t.Fatalf("lookup 失败应返 (0,err)，实际 (%d, %v)", n, err)
	}
}

func TestSweep_AbortErr_PropagatesErr(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	past := now.Add(-1 * time.Hour)
	s := &stubStore{
		hasActive: true,
		activeEng: Engagement{ID: "x", ExpiresAt: &past},
		abortErr:  errors.New("update conflict"),
	}
	r := NewRotator(s, RotateLimits{MaxAge: 24 * time.Hour})
	r.now = fixedNow(now)
	n, err := r.Sweep(context.Background())
	if err == nil || n != 0 {
		t.Fatalf("abort 失败应返 (0,err)，实际 (%d, %v)", n, err)
	}
}
