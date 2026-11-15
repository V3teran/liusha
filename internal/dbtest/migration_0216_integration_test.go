//go:build integration

package dbtest

import (
	"context"
	"testing"
	"time"
)

const migration0216 uint = 216

// 迁移 0216：每个任务只有一个根目标。多余的根目标（旧逻辑按主机建、恢复重投时重复
// 建）删除，挂在它们上的边改挂到保留的最早根目标；目标内容去掉 target_host；唯一
// 索引改为「每个任务一个根目标」。
func TestMigration0216_SingleRootObjective(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0216)
	assignment := SeedAssignment(t, pool)
	var taskID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, brief) VALUES ($1, 'test') RETURNING id`,
		assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	insert := func(kind, state, source, content string, at time.Time) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO exploration_node (id, task_id, kind, state, content, metadata, created_at)
			VALUES (gen_random_uuid(), $1::uuid, $2, $3, $4::jsonb, jsonb_build_object('task_id', $1::text, 'source_type', $5::text), $6)
			RETURNING id`, taskID, kind, state, content, source, at).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	t0 := time.Now().Add(-time.Hour)
	keep := insert("objective", "open", "user", `{"description":"d","target_host":"a:1"}`, t0)
	extra := insert("objective", "open", "user", `{"description":"d","target_host":"b:2"}`, t0.Add(time.Second))
	derived := insert("objective", "open", "planner", `{"description":"派生"}`, t0.Add(2*time.Second))
	action := insert("action", "open", "planner", `{"instruction":"i"}`, t0.Add(3*time.Second))
	if _, err := pool.Exec(ctx, `INSERT INTO exploration_edge (task_id, src_id, rel, dst_id) VALUES ($1, $2, 'generates', $3)`,
		taskID, extra, action); err != nil {
		t.Fatal(err)
	}

	MigrateTo(t, pool, migration0216)

	var roots int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM exploration_node WHERE task_id = $1 AND kind = 'objective'
		AND coalesce(metadata->>'source_type', '') <> 'planner'`, taskID).Scan(&roots); err != nil {
		t.Fatal(err)
	}
	if roots != 1 {
		t.Fatalf("应只剩 1 个根目标，得 %d", roots)
	}
	var content string
	if err := pool.QueryRow(ctx, `SELECT content::text FROM exploration_node WHERE id = $1`, keep).Scan(&content); err != nil {
		t.Fatalf("应保留最早的根目标: %v", err)
	}
	if content != `{"description": "d"}` {
		t.Errorf("根目标内容 = %s", content)
	}
	var src string
	if err := pool.QueryRow(ctx, `SELECT src_id::text FROM exploration_edge WHERE dst_id = $1 AND rel = 'generates'`, action).Scan(&src); err != nil || src != keep {
		t.Errorf("动作的归属边应改挂到保留的根目标: src=%s err=%v", src, err)
	}
	var derivedLeft int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM exploration_node WHERE id = $1`, derived).Scan(&derivedLeft); err != nil || derivedLeft != 1 {
		t.Errorf("派生目标不受影响: %d %v", derivedLeft, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO exploration_node (id, task_id, kind, state, content, metadata)
		VALUES (gen_random_uuid(), $1::uuid, 'objective', 'open', '{}', jsonb_build_object('task_id', $1::text, 'source_type', 'user'))`,
		taskID); err == nil {
		t.Error("同一任务的第二个根目标应被唯一索引拒绝")
	}
}
