//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0222 uint = 222

// 迁移 0222：验证审计链的复现配方列与全系统同名（repro，原 primitives），数据原样保留。
func TestMigration0222_VerificationReproColumn(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0222)
	assignment := SeedAssignment(t, pool)
	var taskID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, brief) VALUES ($1, 'test') RETURNING id`,
		assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO exploration_verification (id, task_id, node_id, primitives, outcome, evidence)
		VALUES ('v1', $1, gen_random_uuid(), '{"domain":"web"}', 'confirmed', '{}')`, taskID); err != nil {
		t.Fatal(err)
	}

	MigrateTo(t, pool, migration0222)

	var repro string
	if err := pool.QueryRow(ctx, `SELECT repro::text FROM exploration_verification WHERE id = 'v1'`).Scan(&repro); err != nil {
		t.Fatal(err)
	}
	if repro != `{"domain": "web"}` {
		t.Errorf("repro = %s", repro)
	}
}
