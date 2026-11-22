package overview

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/finding"
)

// Store 读概览快照。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Snapshot 读一次概览快照。q.Location 必须是 IANA 时区（PostgreSQL 按名字解析），由调用方校验。
// 各项在同一个只读的可重复读事务里读取：页面上的各块互相印证（待处理总数与各目标的待处理之和、
// 按天新增与台账），分开读会在并发写入时彼此对不上。
func (s *Store) Snapshot(ctx context.Context, q Query) (Snapshot, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Snapshot{}, fmt.Errorf("概览：开启只读事务: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // 只读事务，结束即回滚

	loc := q.Location
	since := windowStart(q.Now, loc)
	snap := Snapshot{
		GeneratedAt: q.Now,
		Window:      Window{Days: WindowDays, Since: since},
	}
	if snap.Tasks, err = countTasks(ctx, tx); err != nil {
		return Snapshot{}, err
	}
	if snap.Findings.Open, err = openFindings(ctx, tx); err != nil {
		return Snapshot{}, err
	}
	if snap.Findings.Daily, err = dailyFindings(ctx, tx, since, loc); err != nil {
		return Snapshot{}, err
	}
	if snap.Findings.Classes, err = findingClasses(ctx, tx); err != nil {
		return Snapshot{}, err
	}
	if snap.Usage.Daily, err = dailyUsage(ctx, tx, since, loc); err != nil {
		return Snapshot{}, err
	}
	if snap.Usage.Last24h, err = usageSince(ctx, tx, q.Now.Add(-24*time.Hour)); err != nil {
		return Snapshot{}, err
	}
	if snap.Targets, err = targets(ctx, tx); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

// windowStart 是时间窗起点：查看者时区里「今天往前数 WindowDays-1 天」的零点。
func windowStart(now time.Time, loc *time.Location) time.Time {
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return today.AddDate(0, 0, -(WindowDays - 1))
}

// days 返回时间窗内每一天的日期串（由旧到新），按天序列据此补齐没有数据的日子。
func days(since time.Time) []string {
	out := make([]string, WindowDays)
	for i := range out {
		out[i] = since.AddDate(0, 0, i).Format(time.DateOnly)
	}
	return out
}

func emptyCounts() SeverityCounts {
	c := make(SeverityCounts, len(Severities))
	for _, sev := range Severities {
		c[sev] = 0
	}
	return c
}

// addCount 把一个严重级计数并入（不在固定序列里的级别归入 info，不丢数）。
func addCount(c SeverityCounts, severity string, n int) {
	sev := strings.ToLower(strings.TrimSpace(severity))
	if _, ok := c[sev]; !ok {
		sev = "info"
	}
	c[sev] += n
}

func countTasks(ctx context.Context, tx pgx.Tx) (Tasks, error) {
	var t Tasks
	err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE NOT paused), count(*) FILTER (WHERE paused)
		FROM task WHERE status = 'active'`).Scan(&t.Running, &t.Paused)
	if err != nil {
		return Tasks{}, fmt.Errorf("概览：任务计数: %w", err)
	}
	return t, nil
}

func openFindings(ctx context.Context, tx pgx.Tx) (SeverityCounts, error) {
	rows, err := tx.Query(ctx, `SELECT severity, count(*) FROM finding WHERE status = 'open' GROUP BY severity`)
	if err != nil {
		return nil, fmt.Errorf("概览：待处理发现: %w", err)
	}
	defer rows.Close()
	out := emptyCounts()
	for rows.Next() {
		var sev string
		var n int
		if err := rows.Scan(&sev, &n); err != nil {
			return nil, fmt.Errorf("概览：待处理发现: %w", err)
		}
		addCount(out, sev, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("概览：待处理发现: %w", err)
	}
	return out, nil
}

func dailyFindings(ctx context.Context, tx pgx.Tx, since time.Time, loc *time.Location) ([]DailySeverity, error) {
	rows, err := tx.Query(ctx, `
		SELECT to_char(created_at AT TIME ZONE $2, 'YYYY-MM-DD'), severity, count(*)
		FROM finding WHERE created_at >= $1
		GROUP BY 1, 2`, since, loc.String())
	if err != nil {
		return nil, fmt.Errorf("概览：按天发现: %w", err)
	}
	defer rows.Close()
	byDay := map[string]SeverityCounts{}
	for rows.Next() {
		var day, sev string
		var n int
		if err := rows.Scan(&day, &sev, &n); err != nil {
			return nil, fmt.Errorf("概览：按天发现: %w", err)
		}
		if byDay[day] == nil {
			byDay[day] = emptyCounts()
		}
		addCount(byDay[day], sev, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("概览：按天发现: %w", err)
	}
	out := make([]DailySeverity, 0, WindowDays)
	for _, day := range days(since) {
		c := byDay[day]
		if c == nil {
			c = emptyCounts()
		}
		out = append(out, DailySeverity{Date: day, Counts: c})
	}
	return out, nil
}

func findingClasses(ctx context.Context, tx pgx.Tx) ([]ClassCount, error) {
	rows, err := tx.Query(ctx, `
		SELECT class, count(*), count(*) FILTER (WHERE status = 'open')
		FROM finding
		GROUP BY class
		ORDER BY 2 DESC, class`)
	if err != nil {
		return nil, fmt.Errorf("概览：发现类型: %w", err)
	}
	defer rows.Close()
	out := []ClassCount{}
	for rows.Next() {
		var c ClassCount
		if err := rows.Scan(&c.Class, &c.Total, &c.Open); err != nil {
			return nil, fmt.Errorf("概览：发现类型: %w", err)
		}
		c.Name = finding.Class(c.Class).Name
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("概览：发现类型: %w", err)
	}
	return out, nil
}

const usageColumns = `count(*), count(*) FILTER (WHERE error_message <> ''),
	coalesce(sum(in_tokens), 0), coalesce(sum(out_tokens), 0), coalesce(sum(cached_tokens), 0)`

func dailyUsage(ctx context.Context, tx pgx.Tx, since time.Time, loc *time.Location) ([]DailyUsage, error) {
	rows, err := tx.Query(ctx, `
		SELECT to_char(created_at AT TIME ZONE $2, 'YYYY-MM-DD'), `+usageColumns+`
		FROM llm_invocation WHERE created_at >= $1
		GROUP BY 1`, since, loc.String())
	if err != nil {
		return nil, fmt.Errorf("概览：按天消耗: %w", err)
	}
	defer rows.Close()
	byDay := map[string]UsageTotals{}
	for rows.Next() {
		var day string
		var u UsageTotals
		if err := rows.Scan(&day, &u.Calls, &u.Errors, &u.InTokens, &u.OutTokens, &u.CachedTokens); err != nil {
			return nil, fmt.Errorf("概览：按天消耗: %w", err)
		}
		byDay[day] = u
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("概览：按天消耗: %w", err)
	}
	out := make([]DailyUsage, 0, WindowDays)
	for _, day := range days(since) {
		out = append(out, DailyUsage{Date: day, UsageTotals: byDay[day]})
	}
	return out, nil
}

func usageSince(ctx context.Context, tx pgx.Tx, since time.Time) (UsageTotals, error) {
	var u UsageTotals
	err := tx.QueryRow(ctx, `SELECT `+usageColumns+` FROM llm_invocation WHERE created_at >= $1`, since).
		Scan(&u.Calls, &u.Errors, &u.InTokens, &u.OutTokens, &u.CachedTokens)
	if err != nil {
		return UsageTotals{}, fmt.Errorf("概览：24 小时消耗: %w", err)
	}
	return u, nil
}

// targets 按目标主机汇总：先按待处理发现由重到轻排（严重最多的在前），再按最近活动排。
// 目标取任务的目标主机（用户下发时瞄准的对象），发现经所属任务归到目标上——发现自己的
// host 是漏洞所在的具体地址，同一目标下可能有多个。
func targets(ctx context.Context, tx pgx.Tx) ([]Target, error) {
	rows, err := tx.Query(ctx, `
		WITH t AS (
			SELECT target_host, count(*) AS tasks,
			       count(*) FILTER (WHERE status = 'active') AS active_tasks,
			       max(created_at) AS last_task_at
			FROM task WHERE target_host <> ''
			GROUP BY target_host
		), f AS (
			SELECT tk.target_host,
			       count(*) FILTER (WHERE f.status = 'open' AND f.severity = 'critical') AS critical,
			       count(*) FILTER (WHERE f.status = 'open' AND f.severity = 'high') AS high,
			       count(*) FILTER (WHERE f.status = 'open' AND f.severity = 'medium') AS medium,
			       count(*) FILTER (WHERE f.status = 'open' AND f.severity = 'low') AS low,
			       count(*) FILTER (WHERE f.status = 'open' AND f.severity = 'info') AS info,
			       count(*) AS findings,
			       max(f.created_at) AS last_finding_at
			FROM finding f JOIN task tk ON tk.id = f.task_id
			WHERE tk.target_host <> ''
			GROUP BY tk.target_host
		)
		SELECT t.target_host, t.tasks, t.active_tasks,
		       coalesce(f.critical, 0), coalesce(f.high, 0), coalesce(f.medium, 0), coalesce(f.low, 0), coalesce(f.info, 0),
		       coalesce(f.findings, 0),
		       greatest(t.last_task_at, coalesce(f.last_finding_at, t.last_task_at)) AS last_activity_at
		FROM t LEFT JOIN f ON f.target_host = t.target_host
		ORDER BY 4 DESC, 5 DESC, 6 DESC, 7 DESC, 8 DESC, last_activity_at DESC, t.target_host
		LIMIT $1`, TargetLimit)
	if err != nil {
		return nil, fmt.Errorf("概览：目标排行: %w", err)
	}
	defer rows.Close()
	out := []Target{}
	for rows.Next() {
		var t Target
		var crit, high, med, low, info int
		if err := rows.Scan(&t.Host, &t.Tasks, &t.ActiveTasks, &crit, &high, &med, &low, &info, &t.Findings, &t.LastActivityAt); err != nil {
			return nil, fmt.Errorf("概览：目标排行: %w", err)
		}
		t.Open = SeverityCounts{"critical": crit, "high": high, "medium": med, "low": low, "info": info}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("概览：目标排行: %w", err)
	}
	return out, nil
}
