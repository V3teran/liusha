//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0224 uint = 224

// 迁移 0224：删除没有生产者的控制事件队列 task_control_event。
func TestMigration0224_DropTaskControlEvent(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0224)

	MigrateTo(t, pool, migration0224)

	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.task_control_event') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("task_control_event 应已删除")
	}
}
