//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0240 uint = 240

// 迁移 0240：漏洞记下发现类型。
//
// 主张类别此前只存在结果节点的去重规范键「类别|对象|作用点」里：存量结果节点把类别单独记下，
// 漏洞的类型从它的结果节点回填；没有规范键的（评估者没给出指纹）类型为空。没有写入方的
// cwe_id / owasp_category 两列删除。
func TestMigration0240_FindingClass(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0240)
	assignment := SeedAssignment(t, pool)
	var taskID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, goal) VALUES ($1, 'x') RETURNING id`, assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	newResult := func(metadata string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO exploration_node (id, task_id, kind, content, metadata)
			VALUES (gen_random_uuid(), $1, 'result', '{}', $2::jsonb) RETURNING id::text`, taskID, metadata).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	newFinding := func(nodeID string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO finding (task_id, result_node_id, host, severity, summary)
			VALUES ($1, $2::uuid, 't.local', 'high', 's') RETURNING id::text`, taskID, nodeID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	sqli := newResult(`{"claim_fingerprint":"sql_injection|get http://t.local/item|id"}`)
	noFingerprint := newResult(`{}`)
	fSQLi := newFinding(sqli)
	fNone := newFinding(noFingerprint)

	MigrateTo(t, pool, migration0240)

	classOf := func(id string) string {
		var c string
		if err := pool.QueryRow(ctx, `SELECT class FROM finding WHERE id = $1`, id).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	if got := classOf(fSQLi); got != "sql_injection" {
		t.Errorf("应从结果节点回填类型，得 %q", got)
	}
	if got := classOf(fNone); got != "" {
		t.Errorf("没有规范键的类型应为空，得 %q", got)
	}
	var nodeClass *string
	if err := pool.QueryRow(ctx, `SELECT metadata->>'claim_class' FROM exploration_node WHERE id = $1`, sqli).Scan(&nodeClass); err != nil {
		t.Fatal(err)
	}
	if nodeClass == nil || *nodeClass != "sql_injection" {
		t.Errorf("结果节点应单独记下主张类别，得 %v", nodeClass)
	}
	var dropped int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'finding' AND column_name IN ('cwe_id', 'owasp_category')`).Scan(&dropped); err != nil {
		t.Fatal(err)
	}
	if dropped != 0 {
		t.Errorf("没有写入方的 cwe_id / owasp_category 应删除，仍有 %d 列", dropped)
	}
}
