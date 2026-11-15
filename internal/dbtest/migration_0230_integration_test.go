//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0230 uint = 230

// 迁移 0230：漏洞关联它的坐实结果节点，交付按结果节点幂等。
//
// 此前按 dedup_key（host + cwe + 规范化路径 + method + parameter 的生成列）去重，但 cwe 与
// parameter 从无写入方，键退化成「主机 + 路径 + 方法」：同一接口上先交付的低危主张会吞掉后
// 坐实的高危主张；同一结果重放交付时 target 缺席又会让键变化，产生重复记录。
//
// 存量：result_node_id 从 evaluation.node_id 回填（结果节点须存在）；同一结果的重复记录合并为
// 一条——优先保留人工处置过的，否则保留最早的；回填不到的历史记录保留、不关联。
func TestMigration0230_FindingResultNode(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0230)
	assignment := SeedAssignment(t, pool)
	var taskID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, goal) VALUES ($1, 'x') RETURNING id`, assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	newResult := func() string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO exploration_node (id, task_id, kind, content)
			VALUES (gen_random_uuid(), $1, 'result', '{}') RETURNING id::text`, taskID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	newFinding := func(nodeID, summary, path string, triaged bool) string {
		var id string
		eval := `{}`
		if nodeID != "" {
			eval = `{"node_id":"` + nodeID + `"}`
		}
		if err := pool.QueryRow(ctx, `INSERT INTO finding (task_id, host, severity, summary, target, evaluation, triaged_at)
			VALUES ($1, 't.local', 'high', $2, jsonb_build_object('path', $3::text), $4::jsonb, CASE WHEN $5 THEN now() END) RETURNING id::text`,
			taskID, summary, path, eval, triaged).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	single := newResult()
	dup := newResult()
	keep := newFinding(single, "XSS", "/a", false)
	dupFirst := newFinding(dup, "SQL 注入", "/b", false)
	dupTriaged := newFinding(dup, "SQL 注入（重放）", "/b2", true)
	orphan := newFinding("", "历史记录", "/c", false)

	MigrateTo(t, pool, migration0230)

	exists := func(id string) bool {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM finding WHERE id = $1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	linked := func(id string) string {
		var node *string
		if err := pool.QueryRow(ctx, `SELECT result_node_id::text FROM finding WHERE id = $1`, id).Scan(&node); err != nil {
			t.Fatal(err)
		}
		if node == nil {
			return ""
		}
		return *node
	}
	if linked(keep) != single {
		t.Errorf("应回填结果节点 %s，得 %q", single, linked(keep))
	}
	if !exists(dupTriaged) || exists(dupFirst) {
		t.Errorf("同一结果的重复记录应只保留人工处置过的那条（triaged=%v first=%v）", exists(dupTriaged), exists(dupFirst))
	}
	if !exists(orphan) || linked(orphan) != "" {
		t.Errorf("回填不到结果节点的历史记录应保留且不关联")
	}
	// 同一接口上的不同结果各有一条（不再按「主机 + 路径 + 方法」互相吞掉）
	other := newResult()
	if _, err := pool.Exec(ctx, `INSERT INTO finding (task_id, result_node_id, host, severity, summary, target)
		VALUES ($1, $2, 't.local', 'low', '缺少 CSP', '{"path":"/a","method":"GET"}')`, taskID, other); err != nil {
		t.Fatalf("同接口不同结果应能各落一条: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO finding (task_id, result_node_id, host, severity, summary)
		VALUES ($1, $2, 't.local', 'low', '重复交付')`, taskID, other); err == nil {
		t.Error("同一结果节点不应落两条")
	}
	var hasDedup bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name = 'finding' AND column_name = 'dedup_key')`).Scan(&hasDedup); err != nil {
		t.Fatal(err)
	}
	if hasDedup {
		t.Error("dedup_key 应已删除")
	}
}
