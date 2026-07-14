package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestSemaphore 起 miniredis + 构造 HostSemaphore，测试结束自动清理。
func newTestSemaphore(t *testing.T, limit int, ttl time.Duration) (*HostSemaphore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return NewHostSemaphore(rdb, "test", limit, ttl), mr
}

// 限额内的 Acquire 都应成功，超限的第 N+1 次应被拒绝。
func TestHostSemaphore_LimitEnforced(t *testing.T) {
	sem, _ := newTestSemaphore(t, 2, time.Minute)
	ctx := context.Background()

	rel1, ok1, err := sem.Acquire(ctx, "target.com")
	if err != nil || !ok1 {
		t.Fatalf("第 1 次 acquire 应成功: ok=%v err=%v", ok1, err)
	}
	rel2, ok2, err := sem.Acquire(ctx, "target.com")
	if err != nil || !ok2 {
		t.Fatalf("第 2 次 acquire 应成功: ok=%v err=%v", ok2, err)
	}
	_, ok3, err := sem.Acquire(ctx, "target.com")
	if err != nil {
		t.Fatalf("第 3 次 acquire 不应报错: %v", err)
	}
	if ok3 {
		t.Fatal("第 3 次 acquire 已超限，应返回 false")
	}

	rel1()
	rel4, ok4, err := sem.Acquire(ctx, "target.com")
	if err != nil || !ok4 {
		t.Fatalf("释放一个额度后应能再次 acquire: ok=%v err=%v", ok4, err)
	}
	rel2()
	rel4()
}

// 不同 host 的额度互相独立，互不挤占。
func TestHostSemaphore_IndependentPerHost(t *testing.T) {
	sem, _ := newTestSemaphore(t, 1, time.Minute)
	ctx := context.Background()

	relA, okA, err := sem.Acquire(ctx, "a.com")
	if err != nil || !okA {
		t.Fatalf("a.com acquire 应成功: ok=%v err=%v", okA, err)
	}
	relB, okB, err := sem.Acquire(ctx, "b.com")
	if err != nil || !okB {
		t.Fatalf("b.com acquire 应成功（与 a.com 额度独立）: ok=%v err=%v", okB, err)
	}
	relA()
	relB()
}

// limit ≤ 0 视为不限速，恒放行。
func TestHostSemaphore_UnlimitedWhenLimitNonPositive(t *testing.T) {
	sem, _ := newTestSemaphore(t, 0, time.Minute)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, ok, err := sem.Acquire(ctx, "target.com")
		if err != nil || !ok {
			t.Fatalf("limit=0 应恒放行，第 %d 次: ok=%v err=%v", i, ok, err)
		}
	}
}

// host 为空时恒放行（active orchestrator 无 target_host 的场景）。
func TestHostSemaphore_EmptyHostPassthrough(t *testing.T) {
	sem, _ := newTestSemaphore(t, 1, time.Minute)
	ctx := context.Background()

	_, ok1, err := sem.Acquire(ctx, "")
	if err != nil || !ok1 {
		t.Fatalf("空 host 第 1 次应放行: ok=%v err=%v", ok1, err)
	}
	_, ok2, err := sem.Acquire(ctx, "")
	if err != nil || !ok2 {
		t.Fatalf("空 host 第 2 次也应放行（不占额度）: ok=%v err=%v", ok2, err)
	}
}

// release 后计数应清零回到初始额度，可再次占满 limit 次。
func TestHostSemaphore_ReleaseRestoresCapacity(t *testing.T) {
	sem, _ := newTestSemaphore(t, 1, time.Minute)
	ctx := context.Background()

	rel, ok, err := sem.Acquire(ctx, "target.com")
	if err != nil || !ok {
		t.Fatalf("acquire 应成功: ok=%v err=%v", ok, err)
	}
	rel()

	// 释放后应可再次 acquire 到 limit 次
	rel2, ok2, err := sem.Acquire(ctx, "target.com")
	if err != nil || !ok2 {
		t.Fatalf("release 后应恢复额度: ok=%v err=%v", ok2, err)
	}
	rel2()
}
