package ingestor

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newTestAggregator 起 miniredis + 构造 aggregator，测试结束自动清理。
func newTestAggregator(t *testing.T, batchSize int, window time.Duration) (*aggregator, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return newAggregator(rdb, "test", batchSize, window), mr
}

// 阈值触发：攒够 batchSize 条即 Triggered，不足则否。
func TestAggregator_ThresholdTrigger(t *testing.T) {
	agg, _ := newTestAggregator(t, 3, 10*time.Second)
	ctx := context.Background()

	for i := 1; i <= 2; i++ {
		w, err := agg.observe(ctx, "example.com")
		if err != nil {
			t.Fatalf("observe #%d: %v", i, err)
		}
		if w.Triggered {
			t.Fatalf("observe #%d 不应触发（未达阈值 3）", i)
		}
	}
	// 第 3 条达阈值
	w, err := agg.observe(ctx, "example.com")
	if err != nil {
		t.Fatalf("observe #3: %v", err)
	}
	if !w.Triggered {
		t.Fatal("observe #3 应触发（达阈值 3）")
	}
}

// 超时补偿：不足阈值但窗口超时后，sweepExpired 找出该 host。
func TestAggregator_SweepExpired(t *testing.T) {
	window := 10 * time.Second
	agg, mr := newTestAggregator(t, 100, window) // 阈值取高，逼走超时路径
	ctx := context.Background()

	// 打 2 条（远不足 100），窗口开启。
	for i := 0; i < 2; i++ {
		if _, err := agg.observe(ctx, "silent.com"); err != nil {
			t.Fatalf("observe: %v", err)
		}
	}

	// 尚未超时：sweep 应为空。
	expired, err := agg.sweepExpired(ctx)
	if err != nil {
		t.Fatalf("sweepExpired: %v", err)
	}
	if len(expired) != 0 {
		t.Fatalf("未超时不应返回 host，得 %v", expired)
	}

	// firstKey 存的是绝对 unix ms，不受 miniredis 时钟影响；直接把它改老以模拟窗口超时。
	oldMs := time.Now().Add(-2 * window).UnixMilli()
	if err := mr.Set(agg.firstKey("silent.com"), strconv.FormatInt(oldMs, 10)); err != nil {
		t.Fatalf("set firstKey old: %v", err)
	}

	expired, err = agg.sweepExpired(ctx)
	if err != nil {
		t.Fatalf("sweepExpired 2: %v", err)
	}
	if len(expired) != 1 || expired[0] != "silent.com" {
		t.Fatalf("超时后应返回 [silent.com]，得 %v", expired)
	}
}

// 抢锁幂等：claimAndReset 首次成功清窗口，二次失败（锁 TTL 内挡住并发重复建）。
func TestAggregator_ClaimAndResetIdempotent(t *testing.T) {
	agg, _ := newTestAggregator(t, 3, 10*time.Second)
	ctx := context.Background()

	if _, err := agg.observe(ctx, "h.com"); err != nil {
		t.Fatalf("observe: %v", err)
	}

	won, err := agg.claimAndReset(ctx, "h.com")
	if err != nil {
		t.Fatalf("claim #1: %v", err)
	}
	if !won {
		t.Fatal("首次 claim 应成功")
	}
	// 二次抢同一 host：锁未过期，应失败。
	won, err = agg.claimAndReset(ctx, "h.com")
	if err != nil {
		t.Fatalf("claim #2: %v", err)
	}
	if won {
		t.Fatal("二次 claim 应失败（锁 TTL 内幂等挡住）")
	}
}

// sweepExpired 清孤儿：host 在活跃集但 firstKey 已不存在 → 从集里剔除。
func TestAggregator_SweepCleansOrphan(t *testing.T) {
	agg, mr := newTestAggregator(t, 100, 10*time.Second)
	ctx := context.Background()

	if _, err := agg.observe(ctx, "orphan.com"); err != nil {
		t.Fatalf("observe: %v", err)
	}
	// 手动删 firstKey 模拟"别的实例已处理"，但 host 仍在活跃集。
	mr.Del(agg.firstKey("orphan.com"))

	expired, err := agg.sweepExpired(ctx)
	if err != nil {
		t.Fatalf("sweepExpired: %v", err)
	}
	if len(expired) != 0 {
		t.Fatalf("孤儿 host 不应触发，得 %v", expired)
	}
	// 应已从活跃集剔除。
	members, _ := mr.SMembers(agg.hostsKey())
	if len(members) != 0 {
		t.Fatalf("孤儿 host 应被 SREM，活跃集仍有 %v", members)
	}
}
