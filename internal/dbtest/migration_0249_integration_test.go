//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0249 uint = 249

// 迁移 0249：消息角色只留 user / assistant，存量的 system / tool 归入 assistant。
func TestMigration0249_MessageRole(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0249)
	var convID string
	if err := pool.QueryRow(ctx, `INSERT INTO conversation (title) VALUES ('c') RETURNING id::text`).Scan(&convID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO message (conversation_id, role, kind, content) VALUES
		($1, 'user', 'message', 'u'), ($1, 'tool', 'event', 't'), ($1, 'system', 'event', 's')`, convID); err != nil {
		t.Fatal(err)
	}

	MigrateTo(t, pool, migration0249)

	var users, assistants int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE role = 'user'), count(*) FILTER (WHERE role = 'assistant')
		FROM message WHERE conversation_id = $1`, convID).Scan(&users, &assistants); err != nil {
		t.Fatal(err)
	}
	if users != 1 || assistants != 2 {
		t.Errorf("system / tool 应归入 assistant：user %d，assistant %d", users, assistants)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO message (conversation_id, role, content) VALUES ($1, 'tool', 'x')`, convID); err == nil {
		t.Error("收紧后 tool 角色应被约束拒绝")
	}
}
