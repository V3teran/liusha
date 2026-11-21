//go:build integration

package overview_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/overview"
)

// seedTask 建一个任务（含所属批次），返回任务 ID。
func seedTask(t *testing.T, pool *pgxpool.Pool, host, status string, paused bool, createdAt time.Time, endedAt *time.Time) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `
		WITH a AS (INSERT INTO assignment (id, source, title) VALUES (gen_random_uuid(), 'direct', 'itest') RETURNING id)
		INSERT INTO task (id, assignment_id, goal, target_host, status, paused, created_at, ended_at)
		SELECT gen_random_uuid(), a.id, 'itest', $1, $2, $3, $4, $5 FROM a RETURNING id::text`,
		host, status, paused, createdAt, endedAt).Scan(&id)
	require.NoError(t, err)
	return id
}

func seedFinding(t *testing.T, pool *pgxpool.Pool, taskID, class, severity, status string, createdAt time.Time) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO finding (task_id, class, severity, status, host, summary, created_at) VALUES ($1, $2, $3, $4, 'h', 's', $5)`,
		taskID, class, severity, status, createdAt)
	require.NoError(t, err)
}

func seedCall(t *testing.T, pool *pgxpool.Pool, in, out, cached int, errMsg string, createdAt time.Time) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO llm_invocation (provider, model, in_tokens, out_tokens, cached_tokens, error_message, created_at)
		VALUES ('p', 'm', $1, $2, $3, $4, $5)`, in, out, cached, errMsg, createdAt)
	require.NoError(t, err)
}

// TestSnapshot_CountsWindowAndTimezone 锁定快照口径：进行中分运行 / 暂停（结束的任务不计）；
// 待处理只计 open；按天序列按查看者时区切分并补齐空日；类型统计覆盖全部发现；24 小时消耗与错误数；目标按待处理由重到轻排。
func TestSnapshot_CountsWindowAndTimezone(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	store := overview.NewStore(pool)

	shanghai, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	// 查看者在上海，此刻是当地 2026-11-21 10:00（UTC 02:00）
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, shanghai)
	old := now.AddDate(0, 0, -30)
	recentEnd := now.Add(-2 * time.Hour)
	oldEnd := old.Add(time.Hour)

	a := seedTask(t, pool, "a.example:8080", "active", false, now.Add(-3*time.Hour), nil)
	seedTask(t, pool, "a.example:8080", "active", true, now.Add(-5*time.Hour), nil)
	b := seedTask(t, pool, "b.example", "completed", false, now.Add(-26*time.Hour), &recentEnd)
	seedTask(t, pool, "b.example", "aborted", false, old, &oldEnd) // 窗口外结束：不计
	seedTask(t, pool, "c.example", "aborted", false, now.Add(-4*time.Hour), &recentEnd)

	// 当地 11-21 00:30 = UTC 11-20 16:30：按 UTC 切分会落进前一天
	earlyToday := time.Date(2026, 11, 21, 0, 30, 0, 0, shanghai)
	seedFinding(t, pool, a, "sql_injection", "high", "open", earlyToday)
	seedFinding(t, pool, a, "sql_injection", "HIGH", "open", now.Add(-time.Hour)) // 严重级大小写不敏感
	seedFinding(t, pool, a, "info_disclosure", "low", "confirmed", now.Add(-time.Hour))
	seedFinding(t, pool, b, "sql_injection", "critical", "open", now.Add(-24*time.Hour))
	seedFinding(t, pool, b, "other", "medium", "false_positive", old) // 窗口外：不进按天序列

	seedCall(t, pool, 100, 10, 50, "", now.Add(-time.Hour))
	seedCall(t, pool, 200, 20, 0, "timeout", now.Add(-2*time.Hour))
	seedCall(t, pool, 400, 40, 0, "", now.Add(-48*time.Hour)) // 不在 24 小时内

	snap, err := store.Snapshot(ctx, overview.Query{Now: now, Location: shanghai})
	require.NoError(t, err)

	assert.Equal(t, overview.Tasks{Running: 1, Paused: 1}, snap.Tasks)
	assert.Equal(t, overview.SeverityCounts{"critical": 1, "high": 2, "medium": 0, "low": 0, "info": 0}, snap.Findings.Open)

	require.Len(t, snap.Findings.Daily, overview.WindowDays)
	first, last := snap.Findings.Daily[0], snap.Findings.Daily[overview.WindowDays-1]
	assert.Equal(t, "2026-11-08", first.Date)
	assert.Equal(t, "2026-11-21", last.Date)
	assert.Equal(t, 2, last.Counts["high"], "当地今天的两条高危（含 00:30 那条）")
	assert.Equal(t, 1, last.Counts["low"])
	assert.Equal(t, 1, snap.Findings.Daily[overview.WindowDays-2].Counts["critical"])
	assert.Equal(t, time.Date(2026, 11, 8, 0, 0, 0, 0, shanghai).Unix(), snap.Window.Since.Unix())

	// 类型统计覆盖全部发现（不限时间窗），按数量降序、同数按类型取值，带展示名
	assert.Equal(t, []overview.ClassCount{
		{Class: "sql_injection", Name: "SQL 注入", Total: 3, Open: 3},
		{Class: "info_disclosure", Name: "敏感信息泄露", Total: 1, Open: 0},
		{Class: "other", Name: "其他", Total: 1, Open: 0},
	}, snap.Findings.Classes)

	assert.Equal(t, overview.UsageTotals{Calls: 2, Errors: 1, InTokens: 300, OutTokens: 30, CachedTokens: 50}, snap.Usage.Last24h)
	require.Len(t, snap.Usage.Daily, overview.WindowDays)
	assert.Equal(t, 2, snap.Usage.Daily[overview.WindowDays-1].Calls)
	assert.Equal(t, int64(400), snap.Usage.Daily[overview.WindowDays-3].InTokens)

	require.Len(t, snap.Targets, 3)
	// b 有一条严重待处理，排在只有高危的 a 之前；c 没有发现，排最后
	assert.Equal(t, "b.example", snap.Targets[0].Host)
	assert.Equal(t, 2, snap.Targets[0].Tasks)
	assert.Equal(t, 2, snap.Targets[0].Findings)
	assert.Equal(t, "a.example:8080", snap.Targets[1].Host)
	assert.Equal(t, 2, snap.Targets[1].ActiveTasks)
	assert.Equal(t, 2, snap.Targets[1].Open["high"])
	assert.Equal(t, 3, snap.Targets[1].Findings)
	assert.Equal(t, "c.example", snap.Targets[2].Host)
	assert.Equal(t, 0, snap.Targets[2].Findings)
}

// TestSnapshot_Empty 锁定空库：计数为零、按天序列补齐、目标为空数组（不是 null）。
func TestSnapshot_Empty(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	snap, err := overview.NewStore(pool).Snapshot(context.Background(), overview.Query{Now: time.Now(), Location: time.UTC})
	require.NoError(t, err)
	assert.Equal(t, overview.Tasks{}, snap.Tasks)
	assert.Len(t, snap.Findings.Daily, overview.WindowDays)
	assert.Len(t, snap.Usage.Daily, overview.WindowDays)
	assert.NotNil(t, snap.Targets)
	assert.Empty(t, snap.Targets)
	assert.NotNil(t, snap.Findings.Classes)
	assert.Empty(t, snap.Findings.Classes)
}
