//go:build integration

package finding_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
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
	_, err := pool.Exec(ctx, `INSERT INTO assignment (id, source, title) VALUES ($1, 'direct', 'itest')`, taskID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx,
		`INSERT INTO task (id, assignment_id, goal, target_host) VALUES ($1, $1, 'itest', 'target.local')`, taskID)
	require.NoError(t, err)

	// Save：必填校验
	_, err = store.Save(ctx, finding.Finding{TaskID: taskID, Summary: "s"})
	require.ErrorContains(t, err, "Host 必填")

	// Save：正常写入，默认 severity=medium，分配 ID/Seq/Status
	saved, err := store.Save(ctx, finding.Finding{
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
	again, err := store.Save(ctx, finding.Finding{
		TaskID:  taskID,
		Host:    "target.local",
		Summary: "SQL 注入可枚举 users 表",
		Repro:   json.RawMessage(`{"domain":"web","recipe":{},"assert":{}}`),
	})
	require.NoError(t, err)
	assert.Equal(t, saved.ID, again.ID, "同 dedup_key 应返回 existing 行")

	// ListAll + Host 过滤：按 host 筛（与台账页同口径）
	_, err = store.Save(ctx, finding.Finding{TaskID: taskID, Host: "other.local", Summary: "另一台主机的漏洞"})
	require.NoError(t, err)
	byHost, err := store.ListAll(ctx, finding.LedgerFilter{Host: "target.local"})
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

// TestListAll_CountAll 锁定全局台账的列表 / 计数 / 过滤路径（SQL 引用不存在的列时
// GET /findings 会 100% 运行时报错）。
func TestListAll_CountAll(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	store := finding.NewStore(pool)
	assignmentID := dbtest.SeedAssignment(t, pool)

	taskID := seedTask(t, pool, assignmentID)
	for _, tc := range []struct{ host, summary, severity string }{
		{"ledger.local", "SQL 注入", "critical"},
		{"ledger.local", "XSS", "low"},
		{"other.local", "SSRF", "medium"},
	} {
		_, err := store.Save(ctx, finding.Finding{
			TaskID: taskID, Host: tc.host, Summary: tc.summary, Severity: tc.severity,
		})
		require.NoError(t, err)
	}

	rows, err := store.ListAll(ctx, finding.LedgerFilter{})
	require.NoError(t, err)
	assert.Len(t, rows, 3)
	for _, r := range rows {
		assert.Equal(t, "direct", r.Source, "台账行应带 assignment.source")
	}

	n, err := store.CountAll(ctx, finding.LedgerFilter{})
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	byHost, err := store.ListAll(ctx, finding.LedgerFilter{Host: "other.local"})
	require.NoError(t, err)
	assert.Len(t, byHost, 1)
	assert.Equal(t, "SSRF", byHost[0].Summary)

	bySeverity, err := store.CountAll(ctx, finding.LedgerFilter{Severity: "critical"})
	require.NoError(t, err)
	assert.Equal(t, 1, bySeverity)

	// distinct host 集合：从全量台账行派生
	rows, err = store.ListAll(ctx, finding.LedgerFilter{})
	require.NoError(t, err)
	hosts := make(map[string]bool)
	for _, r := range rows {
		hosts[r.Host] = true
	}
	assert.ElementsMatch(t, []string{"ledger.local", "other.local"}, mapKeys(hosts))
}

// mapKeys 提取集合键（去序）。
func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// seedTask 建最小 task 行返回 id（台账 JOIN task/assignment 用）。
func seedTask(t *testing.T, pool *pgxpool.Pool, assignmentID string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO task (assignment_id, goal) VALUES ($1, 'test') RETURNING id`,
		assignmentID).Scan(&id)
	require.NoError(t, err)
	return id
}

// 按任务筛选：任务详情与对话跳到「这个任务的漏洞」，列表、计数、统计同一口径。
func TestLedger_FilterByTask(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	store := finding.NewStore(pool)
	assignmentID := dbtest.SeedAssignment(t, pool)
	mine, other := seedTask(t, pool, assignmentID), seedTask(t, pool, assignmentID)
	for _, tc := range []struct{ task, summary string }{{mine, "XSS"}, {mine, "SQL 注入"}, {other, "SSRF"}} {
		_, err := store.Save(ctx, finding.Finding{TaskID: tc.task, Host: "t.local", Summary: tc.summary, Severity: "high"})
		require.NoError(t, err)
	}

	f := finding.LedgerFilter{TaskID: mine}
	rows, err := store.ListAll(ctx, f)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, r := range rows {
		assert.Equal(t, mine, r.TaskID)
	}
	n, err := store.CountAll(ctx, f)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	st, err := store.StatFiltered(ctx, f)
	require.NoError(t, err)
	assert.Equal(t, 2, st.Total)
}

// 任务分面：只列有漏洞的任务（带目标与漏洞数），按最近一次发现倒序；其它筛选照常生效，
// 任务筛选不作用于自身（选中一个任务时仍能看到其余可选任务）。
func TestLedger_TaskFacets(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	store := finding.NewStore(pool)
	assignmentID := dbtest.SeedAssignment(t, pool)
	older, newer := seedTask(t, pool, assignmentID), seedTask(t, pool, assignmentID)
	_ = seedTask(t, pool, assignmentID) // 没有漏洞的任务不出现
	for _, tc := range []struct{ task, summary, severity string }{
		{older, "XSS", "high"}, {older, "SQL 注入", "critical"}, {newer, "SSRF", "high"},
	} {
		_, err := store.Save(ctx, finding.Finding{TaskID: tc.task, Host: "t.local", Summary: tc.summary, Severity: tc.severity})
		require.NoError(t, err)
	}

	facets, more, err := store.TaskFacets(ctx, finding.LedgerFilter{TaskID: older}, "", 10)
	require.NoError(t, err)
	require.Len(t, facets, 2, "任务筛选不作用于任务分面自身")
	assert.False(t, more)
	assert.Equal(t, newer, facets[0].TaskID, "最近有发现的任务在前")
	assert.Equal(t, 1, facets[0].Count)
	assert.Equal(t, older, facets[1].TaskID)
	assert.Equal(t, 2, facets[1].Count)
	assert.Equal(t, "test", facets[1].Goal)
	assert.False(t, facets[1].CreatedAt.IsZero(), "目标相同的任务靠创建时间区分")

	critical, _, err := store.TaskFacets(ctx, finding.LedgerFilter{Severity: "critical"}, "", 10)
	require.NoError(t, err)
	require.Len(t, critical, 1, "其它筛选照常生效")
	assert.Equal(t, older, critical[0].TaskID)
	assert.Equal(t, 1, critical[0].Count)
}

// 台账关键词搜索在后端执行，覆盖全部页：匹配漏洞摘要、主机、路径与所属任务的目标；
// 搜索词按字面匹配。任务选项可按目标搜索并限量，超出时报告还有更多。
func TestLedger_SearchAndFacetSearch(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	store := finding.NewStore(pool)
	assignmentID := dbtest.SeedAssignment(t, pool)
	var shop, blog string
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO task (assignment_id, goal) VALUES ($1, '测试商城 shop.local') RETURNING id`, assignmentID).Scan(&shop))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO task (assignment_id, goal) VALUES ($1, '测试博客 blog.local') RETURNING id`, assignmentID).Scan(&blog))
	for _, f := range []finding.Finding{
		{TaskID: shop, Host: "shop.local", Summary: "订单接口 user_id 越权", Severity: "high", Target: []byte(`{"path":"/api/order"}`)},
		{TaskID: shop, Host: "shop.local", Summary: "反射型 XSS", Severity: "medium", Target: []byte(`{"path":"/search"}`)},
		{TaskID: blog, Host: "blog.local", Summary: "评论区存储型 XSS", Severity: "high", Target: []byte(`{"path":"/comment"}`)},
	} {
		_, err := store.Save(ctx, f)
		require.NoError(t, err)
	}
	count := func(q string) int {
		n, err := store.CountAll(ctx, finding.LedgerFilter{Search: q})
		require.NoError(t, err)
		return n
	}
	assert.Equal(t, 2, count("xss"), "摘要，大小写不敏感")
	assert.Equal(t, 1, count("/api/order"), "路径")
	assert.Equal(t, 1, count("blog.local"), "主机")
	assert.Equal(t, 2, count("商城"), "所属任务的目标")
	assert.Equal(t, 1, count("user_id"), "下划线按字面")
	assert.Equal(t, 0, count("userXid"))
	st, err := store.StatFiltered(ctx, finding.LedgerFilter{Search: "xss"})
	require.NoError(t, err)
	assert.Equal(t, 2, st.Total, "统计与列表同一口径")

	facets, more, err := store.TaskFacets(ctx, finding.LedgerFilter{}, "博客", 10)
	require.NoError(t, err)
	require.Len(t, facets, 1)
	assert.Equal(t, blog, facets[0].TaskID)
	assert.False(t, more)
	facets, more, err = store.TaskFacets(ctx, finding.LedgerFilter{}, "", 1)
	require.NoError(t, err)
	assert.Len(t, facets, 1)
	assert.True(t, more, "超出上限时报告还有更多")
}
