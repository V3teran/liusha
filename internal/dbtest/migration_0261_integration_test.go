//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0261 uint = 261

// 迁移 0261：探索图的关系收敛为五种。serves（动作或线索「推进」某个派生目标）连同已有的边
// 一起去掉；attaches 更名 covers，存量的边跟着改名，一条不丢；约束不再接受这两个旧取值。
func TestMigration0261_ExplorationRelationsConverge(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0261)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	const task = "33333333-3333-4444-8333-333333333333"
	SeedTask(t, pool, task)
	exec(`INSERT INTO exploration_node (id, task_id, kind, state, content, metadata) VALUES
		('33333333-0000-4000-8000-000000000001', $1, 'objective', 'open', '{}', '{"source_type":"planner"}'),
		('33333333-0000-4000-8000-000000000002', $1, 'action',    'open', '{}', '{"source_type":"planner"}'),
		('33333333-0000-4000-8000-000000000003', $1, 'observation', NULL, '{}', '{"source_type":"executor"}')`, task)
	exec(`INSERT INTO exploration_edge (task_id, src_id, rel, dst_id) VALUES
		($1, '33333333-0000-4000-8000-000000000002', 'attaches', '33333333-0000-4000-8000-000000000003'),
		($1, '33333333-0000-4000-8000-000000000002', 'serves',   '33333333-0000-4000-8000-000000000001'),
		($1, '33333333-0000-4000-8000-000000000003', 'serves',   '33333333-0000-4000-8000-000000000001')`, task)

	MigrateTo(t, pool, migration0261)

	var relations []string
	rows, err := pool.Query(ctx, `SELECT rel FROM exploration_edge WHERE task_id=$1 ORDER BY rel`, task)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var rel string
		if err := rows.Scan(&rel); err != nil {
			t.Fatal(err)
		}
		relations = append(relations, rel)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(relations) != 1 || relations[0] != "covers" {
		t.Fatalf("attaches 应改名 covers、serves 应删除, got %v", relations)
	}
	for _, rel := range []string{"serves", "attaches"} {
		if _, err := pool.Exec(ctx, `INSERT INTO exploration_edge (task_id, src_id, rel, dst_id) VALUES ($1, $2, $3, $4)`,
			task, "33333333-0000-4000-8000-000000000001", rel, "33333333-0000-4000-8000-000000000002"); err == nil {
			t.Fatalf("迁移后不应再接受关系 %q", rel)
		}
	}
}
