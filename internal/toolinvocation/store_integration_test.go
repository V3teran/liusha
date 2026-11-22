//go:build integration

package toolinvocation

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/dbtest"
)

// TestDeleteOlderThanPostgres 锁定留存批删语义：只删 cutoff 之前的行、循环批删到删完、
// cutoff 之后的行为零值（无行可删）。
func TestDeleteOlderThanPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	defer pool.Close()
	store := NewStore(pool)
	if _, err := pool.Exec(ctx, "DELETE FROM tool_invocation"); err != nil {
		t.Fatalf("清理: %v", err)
	}

	base := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	appendAt := func(at time.Time) {
		id, err := store.Append(ctx, Invocation{AgentRunID: "11111111-2222-4333-8444-555555555555",
			TaskID: "11111111-2222-4333-8444-555555555556", ToolName: "run_command", DurationMs: 10})
		if err != nil {
			t.Fatalf("写入: %v", err)
		}
		if _, err := pool.Exec(ctx, "UPDATE tool_invocation SET created_at=$1 WHERE id=$2", at, id); err != nil {
			t.Fatalf("回写时间: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		appendAt(base.Add(time.Duration(i) * time.Minute))
	}
	appendAt(time.Now()) // 留一行在窗口之外

	var deleted int64
	for {
		n, err := store.DeleteOlderThan(ctx, base.Add(5*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		deleted += n
		if n == 0 {
			break
		}
	}
	if deleted != 3 {
		t.Fatalf("应删除 3 行, got %d", deleted)
	}

	var remain int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM tool_invocation").Scan(&remain); err != nil {
		t.Fatal(err)
	}
	if remain != 1 {
		t.Fatalf("窗口之外的行应保留, remain=%d", remain)
	}

	// 再跑一轮：无到期行，返回 0
	n, err := store.DeleteOlderThan(ctx, base.Add(5*time.Minute))
	if err != nil || n != 0 {
		t.Fatalf("应无行可删: n=%d err=%v", n, err)
	}
}
