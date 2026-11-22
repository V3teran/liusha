//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0245 uint = 245

// 迁移 0245：严重度收进五档（大小写规整，词表外的归 medium）后加约束；first_seen_at 删除。
func TestMigration0245_FindingSeverity(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0245)
	var taskID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, goal) VALUES ($1, 'x') RETURNING id::text`,
		SeedAssignment(t, pool)).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	newFinding := func(severity string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO finding (task_id, host, severity, summary)
			VALUES ($1, 't.local', $2, 's') RETURNING id::text`, taskID, severity).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	upper, unknown, ok := newFinding("HIGH"), newFinding("严重"), newFinding("low")

	MigrateTo(t, pool, migration0245)

	for id, want := range map[string]string{upper: "high", unknown: "medium", ok: "low"} {
		var got string
		if err := pool.QueryRow(ctx, `SELECT severity FROM finding WHERE id = $1`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("严重度应规整为 %s，得 %s", want, got)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO finding (task_id, host, severity, summary) VALUES ($1, 't', 'urgent', 's')`, taskID); err == nil {
		t.Error("词表外的严重度应被约束拒绝")
	}
}
