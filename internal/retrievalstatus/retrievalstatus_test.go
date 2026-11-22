package retrievalstatus

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return New(rdb)
}

// TestFailGetRecover 锁定降级记录的语义：Fail 写入带原因与时间（读取可见），
// Recover 清除（幂等），无记录时 ok=false。
func TestFailGetRecover(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)

	if _, ok, err := st.Get(ctx); err != nil || ok {
		t.Fatalf("无记录时 ok 应为 false: ok=%v err=%v", ok, err)
	}

	if err := st.Fail(ctx, "402 上游欠费"); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := st.Get(ctx)
	if err != nil || !ok {
		t.Fatalf("失败后应可读回: ok=%v err=%v", ok, err)
	}
	if rec.Reason != "402 上游欠费" || time.Since(rec.At) > time.Minute {
		t.Fatalf("记录内容错误: %+v", rec)
	}

	if err := st.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.Get(ctx); ok {
		t.Fatal("清除后应无记录")
	}
	// 幂等：再清一次也不报错
	if err := st.Recover(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestFailRefreshesTTL 锁定「持续失败期间记录不过期」：两次 Fail 后 TTL 被刷新到满额。
func TestFailRefreshesTTL(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	st := New(rdb)

	if err := st.Fail(ctx, "第一次"); err != nil {
		t.Fatal(err)
	}
	mr.FastForward(TTL - time.Minute) // 快进到即将过期
	if _, ok, _ := st.Get(ctx); !ok {
		t.Fatal("记录应仍在")
	}
	if err := st.Fail(ctx, "第二次"); err != nil {
		t.Fatal(err)
	}
	mr.FastForward(TTL - time.Minute) // 再快进同等时长：若 TTL 被刷新应仍在
	rec, ok, _ := st.Get(ctx)
	if !ok || rec.Reason != "第二次" {
		t.Fatalf("持续失败应刷新 TTL: ok=%v rec=%+v", ok, rec)
	}
}
