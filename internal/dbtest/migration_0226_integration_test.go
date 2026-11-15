//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0226 uint = 226

// 迁移 0226：一个任务至多一个对话，由部分唯一索引保证。存量里同一任务若关联了多个对话，
// 保留最近更新的那个（与此前按任务反查对话的取法一致），其余解除关联（对话本身保留）。
func TestMigration0226_OneConversationPerTask(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0226)
	assignment := SeedAssignment(t, pool)
	var taskID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, goal, status) VALUES ($1, '扫描 http://a.test', 'completed') RETURNING id`,
		assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	newConv := func(title, updatedAt string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO conversation (title, task_id, updated_at) VALUES ($1, $2, $3::timestamptz) RETURNING id`,
			title, taskID, updatedAt).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	older := newConv("旧", "2026-01-01T00:00:00Z")
	newer := newConv("新", "2026-01-02T00:00:00Z")

	MigrateTo(t, pool, migration0226)

	linked := func(id string) string {
		var tid *string
		if err := pool.QueryRow(ctx, `SELECT task_id::text FROM conversation WHERE id = $1`, id).Scan(&tid); err != nil {
			t.Fatal(err)
		}
		if tid == nil {
			return ""
		}
		return *tid
	}
	if got := linked(newer); got != taskID {
		t.Errorf("最近更新的对话应保留关联，得 %q", got)
	}
	if got := linked(older); got != "" {
		t.Errorf("较旧的对话应解除关联，得 %q", got)
	}
	if _, err := pool.Exec(ctx, `UPDATE conversation SET task_id = $1 WHERE id = $2`, taskID, older); err == nil {
		t.Error("同一任务关联第二个对话应被唯一索引拒绝")
	}
}
