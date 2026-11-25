//go:build integration

package toolinvocation

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

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

	// tool_invocation 的两列 uuid 都是外键：落一条真实任务与 run，供 Append 落库
	taskID := uuid.NewString()
	dbtest.SeedTask(t, pool, taskID)
	var runID string
	if err := pool.QueryRow(ctx, "INSERT INTO agent_run (task_id) VALUES ($1::uuid) RETURNING id", taskID).Scan(&runID); err != nil {
		t.Fatalf("seed agent_run: %v", err)
	}

	base := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	appendAt := func(at time.Time) {
		id, err := store.Append(ctx, Invocation{AgentRunID: runID,
			TaskID: taskID, ToolName: "command_run", DurationMs: 10})
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

// 工具超时或被取消时，调用方传进来的 ctx 已经结束；这条记录——「这次工具调用为什么
// 失败」——恰恰要在这种时候留下（此前库里带超时字样的工具记录为 0 条）。
func TestAppendSurvivesEndedContextPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	defer pool.Close()
	store := NewStore(pool)

	taskID := uuid.NewString()
	dbtest.SeedTask(t, pool, taskID)
	var runID string
	if err := pool.QueryRow(ctx, "INSERT INTO agent_run (task_id) VALUES ($1::uuid) RETURNING id", taskID).Scan(&runID); err != nil {
		t.Fatalf("seed agent_run: %v", err)
	}

	ended, cancel := context.WithTimeout(ctx, -time.Second)
	defer cancel()
	const timeoutMsg = "工具 command_run 执行超时（30s）"
	id, err := store.Append(ended, Invocation{AgentRunID: runID, TaskID: taskID,
		ToolName: "command_run", DurationMs: 30000, ErrorMessage: timeoutMsg})
	if err != nil {
		t.Fatalf("ctx 已结束时记录仍应写下: %v", err)
	}
	var got string
	if err := pool.QueryRow(ctx, "SELECT error_message FROM tool_invocation WHERE id=$1", id).Scan(&got); err != nil {
		t.Fatalf("读回记录: %v", err)
	}
	if got != timeoutMsg {
		t.Fatalf("失败原因应原样留存，实际 %q", got)
	}
}

// 按调用方角色分组的工具用量（任务详情的「智能体调用情况」）：调用多的排前，各分组之和等于全量
// 合计；没有调用时是空表。
func TestAggregateByTaskRolePostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	defer pool.Close()
	store := NewStore(pool)
	taskID := uuid.NewString()
	dbtest.SeedTask(t, pool, taskID)
	var runID string
	if err := pool.QueryRow(ctx, "INSERT INTO agent_run (task_id) VALUES ($1::uuid) RETURNING id", taskID).Scan(&runID); err != nil {
		t.Fatalf("seed agent_run: %v", err)
	}

	got, err := store.AggregateByTaskRole(ctx, taskID)
	if err != nil {
		t.Fatalf("aggregate by role on empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("没有调用时应为空表，得 %+v", got)
	}

	add := func(role string, ms int) {
		t.Helper()
		if _, err := store.Append(ctx, Invocation{AgentRunID: runID, TaskID: taskID, Role: role, ToolName: "command_run", DurationMs: ms}); err != nil {
			t.Fatalf("写入: %v", err)
		}
	}
	add("planner", 10)
	add("executor", 100)
	add("executor", 200)

	got, err = store.AggregateByTaskRole(ctx, taskID)
	if err != nil {
		t.Fatalf("aggregate by role: %v", err)
	}
	want := []RoleAggregate{
		{Role: "executor", Aggregate: Aggregate{Calls: 2, DurationMs: 300}},
		{Role: "planner", Aggregate: Aggregate{Calls: 1, DurationMs: 10}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("按调用方分组不符：\n得   %+v\n期望 %+v", got, want)
	}

	// 各分组之和等于全量合计：同一批记录的两种切法
	total, err := store.AggregateByTask(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	var sum Aggregate
	for _, r := range got {
		sum.Calls += r.Calls
		sum.DurationMs += r.DurationMs
	}
	if sum != total {
		t.Errorf("各分组之和 %+v 应等于全量合计 %+v", sum, total)
	}
}
