//go:build integration

package task

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/dbtest"
)

// graphFixture 在任务图上铺一组典型节点：派生目标（open 与已达成）、open / running /
// completed 动作，连同任务创建时就有的根目标。返回名称 → 节点 ID。
func graphFixture(t *testing.T, pool *pgxpool.Pool, taskID string) map[string]string {
	t.Helper()
	ids := map[string]string{}
	var root string
	if err := pool.QueryRow(context.Background(), `
		SELECT id FROM exploration_node WHERE task_id = $1 AND kind = 'objective' AND metadata->>'source_type' = 'user'`,
		taskID).Scan(&root); err != nil {
		t.Fatalf("read root: %v", err)
	}
	ids["root"] = root
	nodes := []struct{ name, kind, state, source string }{
		{"derived-open", "objective", "open", "planner"},
		{"derived-done", "objective", "completed", "planner"},
		{"action-open", "action", "open", "planner"},
		{"action-running", "action", "running", "planner"},
		{"action-done", "action", "completed", "planner"},
	}
	for _, n := range nodes {
		var id string
		if err := pool.QueryRow(context.Background(), `
			INSERT INTO exploration_node (id, task_id, kind, state, metadata)
			VALUES (gen_random_uuid(), $1::uuid, $2, $3, jsonb_build_object('task_id', $1::text, 'source_type', $4::text))
			RETURNING id`, taskID, n.kind, n.state, n.source).Scan(&id); err != nil {
			t.Fatalf("seed %s: %v", n.name, err)
		}
		ids[n.name] = id
	}
	return ids
}

func nodeStates(t *testing.T, pool *pgxpool.Pool, ids map[string]string) map[string]string {
	t.Helper()
	states := map[string]string{}
	for name, id := range ids {
		var state string
		if err := pool.QueryRow(context.Background(), `SELECT state FROM exploration_node WHERE id = $1`, id).Scan(&state); err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		states[name] = state
	}
	return states
}

func assertStates(t *testing.T, got, want map[string]string) {
	t.Helper()
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s = %s，期望 %s", name, got[name], w)
		}
	}
}

// 任务被终止：根目标与仍 open 的派生目标落 aborted，未结束的动作落 aborted；
// 已结束的节点不动。
func TestGraphFollowsTask_Abort(t *testing.T) {
	s := NewStore(dbtest.NewPgPool(t))
	id := newActiveTask(t, s)
	ids := graphFixture(t, s.pool, id)

	if _, err := s.Abort(context.Background(), id, "用户手动停止"); err != nil {
		t.Fatal(err)
	}
	assertStates(t, nodeStates(t, s.pool, ids), map[string]string{
		"root": "aborted", "derived-open": "aborted", "derived-done": "completed",
		"action-open": "aborted", "action-running": "aborted", "action-done": "completed",
	})
}

// 任务完成：根目标 completed；仍 open 的派生目标是未达成的方向，落 exhausted。
func TestGraphFollowsTask_Complete(t *testing.T) {
	s := NewStore(dbtest.NewPgPool(t))
	id := newActiveTask(t, s)
	ids := graphFixture(t, s.pool, id)

	if err := s.Complete(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	assertStates(t, nodeStates(t, s.pool, ids), map[string]string{
		"root": "completed", "derived-open": "exhausted", "derived-done": "completed",
		"action-open": "aborted", "action-running": "aborted", "action-done": "completed",
	})
}

// 续接重开：根目标代表任务本身，随任务回到 open；历史上已收口的派生目标与动作不动。
func TestGraphFollowsTask_Reopen(t *testing.T) {
	s := NewStore(dbtest.NewPgPool(t))
	id := newActiveTask(t, s)
	ids := graphFixture(t, s.pool, id)
	if err := s.Complete(context.Background(), id); err != nil {
		t.Fatal(err)
	}

	if err := reopen(s, context.Background(), id); err != nil {
		t.Fatal(err)
	}
	assertStates(t, nodeStates(t, s.pool, ids), map[string]string{
		"root": "open", "derived-open": "exhausted", "action-open": "aborted",
	})
}

// 被拒的迁移（已完成的任务迟到的终止）不得改动图。
func TestGraphFollowsTask_RejectedTransitionLeavesGraph(t *testing.T) {
	s := NewStore(dbtest.NewPgPool(t))
	id := newActiveTask(t, s)
	ids := graphFixture(t, s.pool, id)
	if err := s.Complete(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Abort(context.Background(), id, "迟到的停止"); err == nil {
		t.Fatal("completed 后 Abort 应被拒")
	}
	assertStates(t, nodeStates(t, s.pool, ids), map[string]string{"root": "completed", "derived-open": "exhausted"})
}
