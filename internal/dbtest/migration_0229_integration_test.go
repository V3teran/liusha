//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0229 uint = 229

// 迁移 0229：LLM 调用记下所在对话（对话内直接发起的调用）；对话删除时置空，调用记录保留。
func TestMigration0229_InvocationConversation(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0229)
	MigrateTo(t, pool, migration0229)
	var conv string
	if err := pool.QueryRow(ctx, `INSERT INTO conversation (title) VALUES ('闲聊') RETURNING id`).Scan(&conv); err != nil {
		t.Fatal(err)
	}
	var inv int64
	if err := pool.QueryRow(ctx, `INSERT INTO llm_invocation (conversation_id, provider, model, role)
		VALUES ($1, 'p', 'm', 'inspector') RETURNING id`, conv).Scan(&inv); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM conversation WHERE id = $1`, conv); err != nil {
		t.Fatal(err)
	}
	var got *string
	if err := pool.QueryRow(ctx, `SELECT conversation_id::text FROM llm_invocation WHERE id = $1`, inv).Scan(&got); err != nil {
		t.Fatalf("删除对话后调用记录应保留: %v", err)
	}
	if got != nil {
		t.Errorf("对话删除后应置空，得 %s", *got)
	}
}
