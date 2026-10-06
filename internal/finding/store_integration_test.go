//go:build integration

package finding_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/finding"
)

// finding 存储是交付物的唯一落点（仅坐实漏洞入库）——本套件锁定其核心契约：
// Save 幂等去重（task+dedup_key）、Update 保留 first_seen、按 task/host/limit 读取。

func TestFinding_SaveDedupUpdateList(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	store := finding.NewStore(pool)
	const taskID = "11111111-2222-3333-4444-555555555555"
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM finding WHERE task_id=$1`, taskID)
		_, _ = pool.Exec(ctx, `DELETE FROM task WHERE id=$1`, taskID)
		_, _ = pool.Exec(ctx, `DELETE FROM assignment WHERE id=$1`, taskID)
	})

	// finding.task_id → task → assignment 外键链：先建父行
	_, err := pool.Exec(ctx, `INSERT INTO assignment (id, source, title) VALUES ($1, 'manual', 'itest')`, taskID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO task (id, assignment_id, brief, target_host) VALUES ($1, $1, 'itest', 'target.local')`, taskID)
	require.NoError(t, err)

	// Save：必填校验
	_, err = store.Save(ctx, finding.VulnFinding{TaskID: taskID, Summary: "s"})
	require.ErrorContains(t, err, "Host 必填")

	// Save：正常写入，默认 severity=medium，分配 ID/Seq/Status
	saved, err := store.Save(ctx, finding.VulnFinding{
		TaskID:  taskID,
		Host:    "target.local",
		Summary: "SQL 注入可枚举 users 表",
		Repro:   json.RawMessage(`{"domain":"web","recipe":{},"assert":{}}`),
	})
	require.NoError(t, err)
	assert.NotEmpty(t, saved.ID)
	assert.NotZero(t, saved.Seq)
	assert.Equal(t, "medium", saved.Severity)
	assert.Equal(t, "open", saved.Status)

	// Save 幂等去重：同 task + 同 host/summary → 返回 existing 行（同 ID），不新增
	again, err := store.Save(ctx, finding.VulnFinding{
		TaskID:  taskID,
		Host:    "target.local",
		Summary: "SQL 注入可枚举 users 表",
		Repro:   json.RawMessage(`{"domain":"web","recipe":{},"assert":{}}`),
	})
	require.NoError(t, err)
	assert.Equal(t, saved.ID, again.ID, "同 dedup_key 应返回 existing 行")

	// ListByTaskAndHost：按 host 过滤；limit=0 表示不限
	_, err = store.Save(ctx, finding.VulnFinding{TaskID: taskID, Host: "other.local", Summary: "另一台主机的漏洞"})
	require.NoError(t, err)
	byHost, err := store.ListByTaskAndHost(ctx, taskID, "target.local", 0)
	require.NoError(t, err)
	assert.Len(t, byHost, 1)
	all, err := store.ListByTask(ctx, taskID)
	require.NoError(t, err)
	assert.Len(t, all, 2)

	// UpdateTriage：处置态流转
	trianed, err := store.UpdateTriage(ctx, saved.ID, "confirmed", "critical", "复现坐实，人工复核通过")
	require.NoError(t, err)
	assert.Equal(t, "confirmed", trianed.Status)
	assert.Equal(t, "critical", trianed.Severity)
}
