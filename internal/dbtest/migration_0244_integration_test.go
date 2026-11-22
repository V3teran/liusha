//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0244 uint = 244

// 迁移 0244：探索图与审计链挂上任务外键。不可达的孤儿行先清掉；假设节点上没有读取方的四个
// 元数据键清掉，节点版本（规划依据）不因清理而改变；非 UUID 形式的审计链主键换成新 UUID。
func TestMigration0244_ExplorationIntegrity(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0244)
	var taskID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, goal) VALUES ($1, 'x') RETURNING id::text`,
		SeedAssignment(t, pool)).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	const orphanTask = "0000aaaa-0000-4000-8000-000000000001"
	var hyp, orphan string
	if err := pool.QueryRow(ctx, `INSERT INTO exploration_node (id, task_id, kind, content, metadata)
		VALUES (gen_random_uuid(), $1, 'observation', '{}',
		        '{"verification_status":"refuted","refute_reason":"not_reproducible","failure_reason":"f","verify_attempts":3,"verdict_source":"s","duplicate_of":"d"}')
		RETURNING id::text`, taskID).Scan(&hyp); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO exploration_node (id, task_id, kind, content)
		VALUES (gen_random_uuid(), $1, 'observation', '{}') RETURNING id::text`, orphanTask).Scan(&orphan); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO exploration_verification (id, task_id, node_id, outcome) VALUES
		('v-legacy', $1, $2, 'refuted'), ('v-orphan', $3, $4, 'refuted')`, taskID, hyp, orphanTask, orphan); err != nil {
		t.Fatal(err)
	}
	var versionBefore int64
	if err := pool.QueryRow(ctx, `SELECT version FROM exploration_node WHERE id = $1`, hyp).Scan(&versionBefore); err != nil {
		t.Fatal(err)
	}

	MigrateTo(t, pool, migration0244)

	var meta string
	var version int64
	if err := pool.QueryRow(ctx, `SELECT metadata::text, version FROM exploration_node WHERE id = $1`, hyp).Scan(&meta, &version); err != nil {
		t.Fatal(err)
	}
	if meta != `{"refute_reason": "not_reproducible", "verification_status": "refuted"}` {
		t.Errorf("应只留验证结论键，得 %s", meta)
	}
	if version != versionBefore {
		t.Errorf("清理不应改动节点版本：%d → %d", versionBefore, version)
	}
	var orphans, verifications int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM exploration_node WHERE task_id = $1`, orphanTask).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM exploration_verification WHERE task_id = $1`, taskID).Scan(&verifications); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 || verifications != 1 {
		t.Errorf("孤儿节点应清掉、本任务的审计记录应保留：孤儿 %d，审计 %d", orphans, verifications)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO exploration_node (id, task_id, kind, content)
		VALUES (gen_random_uuid(), $1, 'observation', '{}')`, orphanTask); err == nil {
		t.Error("指向不存在任务的节点应被外键拒绝")
	}
}
