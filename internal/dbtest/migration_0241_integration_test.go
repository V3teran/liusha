//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0241 uint = 241

// 迁移 0241：发现类型收敛为封闭词表——词表之外的类型与空值归入 other，词表内的不动；
// 此后列的默认值是 other。
func TestMigration0241_FindingClassVocabulary(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0241)
	assignment := SeedAssignment(t, pool)
	var taskID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, goal) VALUES ($1, 'x') RETURNING id`, assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	newFinding := func(class string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO finding (task_id, class, host, severity, summary)
			VALUES ($1, $2, 't.local', 'high', 's') RETURNING id::text`, taskID, class).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	kept := newFinding("sql_injection")
	flag := newFinding("flag")
	madeUp := newFinding("hidden_directory_exposure")
	empty := newFinding("")

	MigrateTo(t, pool, migration0241)

	for id, want := range map[string]string{kept: "sql_injection", flag: "flag", madeUp: "other", empty: "other"} {
		var got string
		if err := pool.QueryRow(ctx, `SELECT class FROM finding WHERE id = $1`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("类型应为 %q，得 %q", want, got)
		}
	}
	var def string
	if err := pool.QueryRow(ctx, `INSERT INTO finding (task_id, host, severity, summary)
		VALUES ($1, 't.local', 'high', 's') RETURNING class`, taskID).Scan(&def); err != nil {
		t.Fatal(err)
	}
	if def != "other" {
		t.Errorf("列默认值应为 other，得 %q", def)
	}
}
