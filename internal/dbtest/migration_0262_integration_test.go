//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0262 uint = 262

// 迁移 0262：探索图的关系收敛为三种。covers 与 supersedes 的边删除，pursues 留下；约束不再
// 接受这两个取值。节点状态的 superseded 去掉：存量改写成 aborted，约束不再接受它。
func TestMigration0262_ExplorationRelationsThree(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0262)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	const task = "33333333-3333-4444-8333-333333333334"
	SeedTask(t, pool, task)
	exec(`INSERT INTO exploration_node (id, task_id, kind, state, content, metadata) VALUES
		('33333333-0000-4000-8000-000000000011', $1, 'objective',   'open',       '{}', '{"source_type":"planner"}'),
		('33333333-0000-4000-8000-000000000012', $1, 'objective',   'superseded', '{}', '{"source_type":"planner"}'),
		('33333333-0000-4000-8000-000000000013', $1, 'action',      'open',       '{}', '{"source_type":"planner"}'),
		('33333333-0000-4000-8000-000000000014', $1, 'observation', NULL,         '{}', '{"source_type":"executor"}')`, task)
	exec(`INSERT INTO exploration_edge (task_id, src_id, rel, dst_id) VALUES
		($1, '33333333-0000-4000-8000-000000000013', 'covers',    '33333333-0000-4000-8000-000000000014'),
		($1, '33333333-0000-4000-8000-000000000011', 'supersedes', '33333333-0000-4000-8000-000000000012'),
		($1, '33333333-0000-4000-8000-000000000013', 'pursues',   '33333333-0000-4000-8000-000000000014')`, task)

	MigrateTo(t, pool, migration0262)

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
	if len(relations) != 1 || relations[0] != "pursues" {
		t.Fatalf("covers 与 supersedes 应删除、pursues 留下, got %v", relations)
	}
	for _, rel := range []string{"covers", "supersedes"} {
		if _, err := pool.Exec(ctx, `INSERT INTO exploration_edge (task_id, src_id, rel, dst_id) VALUES ($1, $2, $3, $4)`,
			task, "33333333-0000-4000-8000-000000000011", rel, "33333333-0000-4000-8000-000000000012"); err == nil {
			t.Fatalf("迁移后不应再接受关系 %q", rel)
		}
	}

	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM exploration_node WHERE id='33333333-0000-4000-8000-000000000012'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "aborted" {
		t.Fatalf("superseded 应改写成 aborted, got %q", state)
	}
	if _, err := pool.Exec(ctx, `UPDATE exploration_node SET state='superseded' WHERE id='33333333-0000-4000-8000-000000000012'`); err == nil {
		t.Fatal("迁移后不应再接受状态 superseded")
	}
}
