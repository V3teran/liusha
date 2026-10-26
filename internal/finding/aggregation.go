// internal/finding/aggregation.go
// 漏洞聚合查询：按语义维度（host+cwe+path）聚合，展示生命周期视图。

package finding

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// AggregatedFinding 代表按语义维度聚合后的漏洞组。
//
// 设计理念：漏洞的语义唯一性由三元组定义：(host, cwe_id, normalized_path)。
// 同一语义漏洞的多次发现（跨 task）聚合为一个视图，追踪其生命周期。
// 每个聚合组保留：最新发现的完整上下文、历史统计、所有关联 finding_id。
type AggregatedFinding struct {
	// 聚合键（语义唯一标识）
	AggregateKey string `json:"aggregate_key"` // host|cwe_id|normalized_path

	// 最新发现（用于展示）
	LatestID        string `json:"latest_id"`
	LatestSummary   string `json:"latest_summary"`
	LatestSeverity  string `json:"latest_severity"`
	LatestStatus    string `json:"latest_status"`
	LatestCWEID     string `json:"latest_cwe_id"`
	LatestHost      string `json:"latest_host"`
	LatestTarget    []byte `json:"latest_target"`    // json.RawMessage
	LatestEval      []byte `json:"latest_evaluation"` // json.RawMessage
	LatestSource    string `json:"latest_source"`     // manual/auto

	// 生命周期统计
	OccurrenceCount int       `json:"occurrence_count"` // 发现次数
	FirstSeenAt     time.Time `json:"first_seen_at"`    // 首次发现时间
	LastSeenAt      time.Time `json:"last_seen_at"`     // 最新发现时间
	DaysSinceFirst  int       `json:"days_since_first"` // 存续天数

	// 状态分布（聚合组内各状态计数）
	OpenCount          int `json:"open_count"`
	ConfirmedCount     int `json:"confirmed_count"`
	FixedCount         int `json:"fixed_count"`
	FalsePositiveCount int `json:"false_positive_count"`
	AcceptedCount      int `json:"accepted_count"`

	// 关联 finding_id 列表（按时间倒序）
	FindingIDs []string `json:"finding_ids"`

	// 最新的 remediation 建议（仅一份，避免重复）
	Remediation string `json:"remediation"`
}

// AggregationFilter 是聚合查询的筛选条件（复用 LedgerFilter 的语义）。
type AggregationFilter struct {
	Host     string
	Severity string // 筛选最新严重度
	Status   string // 筛选最新状态
	Source   string
	Limit    int
	Offset   int
}

// ListAggregated 返回聚合后的漏洞视图，按最新发现时间倒序。
//
// 聚合逻辑：
// 1. 计算聚合键：lower(host) || '|' || lower(cwe_id) || '|' || normalize_path(path)
// 2. 每组取最新一条 finding 作为展示上下文
// 3. 统计组内发现次数、时间跨度、状态分布
// 4. 返回所有 finding_id 供展开查看
//
// 业界对齐：
// - DefectDojo: 类似的 "Finding Groups" 概念，按 hash_key 聚合
// - GitHub Security: Dependabot alerts 按 (package, vulnerability) 聚合
// - Snyk: 按 (package, CVE) 聚合，显示项目数
func (s *Store) ListAggregated(ctx context.Context, f AggregationFilter) ([]AggregatedFinding, error) {
	where, args := aggregationWhere(f)

	// CTE 1: 规范化 path，计算聚合键
	// CTE 2: 按聚合键分组，取每组最新的 finding_id
	// 主查询: JOIN 回 finding 表获取完整上下文 + 聚合统计
	q := `
	WITH normalized AS (
		SELECT
			f.id,
			f.task_id,
			f.host,
			f.cwe_id,
			f.severity,
			f.status,
			f.summary,
			f.target,
			f.evaluation,
			f.remediation,
			f.first_seen_at,
			f.created_at,
			a.source,
			-- 聚合键：host + cwe_id + 规范化 path
			lower(f.host)
			|| '|' || lower(coalesce(f.cwe_id, ''))
			|| '|' || lower(
				regexp_replace(
					rtrim(
						split_part(coalesce(f.target->>'path', ''), '?', 1),
						'/'
					),
					'/+', '/', 'g'
				)
			) AS aggregate_key
		FROM finding f
		JOIN task t ON t.id = f.task_id
		JOIN assignment a ON a.id = t.assignment_id
	),
	groups AS (
		SELECT
			aggregate_key,
			-- 每组最新的 finding_id
			(array_agg(id ORDER BY created_at DESC))[1] AS latest_id,
			-- 统计
			count(*) AS occurrence_count,
			min(first_seen_at) AS first_seen_at,
			max(created_at) AS last_seen_at,
			-- 状态分布
			count(*) FILTER (WHERE status = 'open') AS open_count,
			count(*) FILTER (WHERE status = 'confirmed') AS confirmed_count,
			count(*) FILTER (WHERE status = 'fixed') AS fixed_count,
			count(*) FILTER (WHERE status = 'false_positive') AS false_positive_count,
			count(*) FILTER (WHERE status = 'accepted') AS accepted_count,
			-- 所有 finding_id（倒序）
			array_agg(id ORDER BY created_at DESC) AS finding_ids
		FROM normalized
		GROUP BY aggregate_key
	)
	SELECT
		g.aggregate_key,
		g.latest_id,
		n.summary AS latest_summary,
		n.severity AS latest_severity,
		n.status AS latest_status,
		n.cwe_id AS latest_cwe_id,
		n.host AS latest_host,
		n.target AS latest_target,
		n.evaluation AS latest_evaluation,
		n.source AS latest_source,
		g.occurrence_count,
		g.first_seen_at,
		g.last_seen_at,
		EXTRACT(EPOCH FROM (g.last_seen_at - g.first_seen_at))::int / 86400 AS days_since_first,
		g.open_count,
		g.confirmed_count,
		g.fixed_count,
		g.false_positive_count,
		g.accepted_count,
		g.finding_ids,
		n.remediation
	FROM groups g
	JOIN normalized n ON n.id = g.latest_id
	` + where + `
	ORDER BY g.last_seen_at DESC
	`

	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", f.Limit, f.Offset)
	}

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query aggregated findings: %w", err)
	}
	defer rows.Close()

	var out []AggregatedFinding
	for rows.Next() {
		var a AggregatedFinding
		if err := scanAggregated(rows, &a); err != nil {
			return nil, fmt.Errorf("scan aggregated finding: %w", err)
		}
		out = append(out, a)
	}

	return out, rows.Err()
}

// CountAggregated 返回聚合后的漏洞组总数（用于分页）。
func (s *Store) CountAggregated(ctx context.Context, f AggregationFilter) (int, error) {
	where, args := aggregationWhere(f)

	q := `
	WITH normalized AS (
		SELECT
			lower(f.host)
			|| '|' || lower(coalesce(f.cwe_id, ''))
			|| '|' || lower(
				regexp_replace(
					rtrim(
						split_part(coalesce(f.target->>'path', ''), '?', 1),
						'/'
					),
					'/+', '/', 'g'
				)
			) AS aggregate_key,
			f.severity,
			f.status,
			f.created_at,
			a.source
		FROM finding f
		JOIN task t ON t.id = f.task_id
		JOIN assignment a ON a.id = t.assignment_id
	),
	groups AS (
		SELECT
			aggregate_key,
			(array_agg(severity ORDER BY created_at DESC))[1] AS latest_severity,
			(array_agg(status ORDER BY created_at DESC))[1] AS latest_status,
			(array_agg(source ORDER BY created_at DESC))[1] AS latest_source
		FROM normalized
		GROUP BY aggregate_key
	)
	SELECT count(*) FROM groups g
	` + where

	var total int
	if err := s.pool.QueryRow(ctx, q, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count aggregated findings: %w", err)
	}
	return total, nil
}

// aggregationWhere 构造聚合查询的 WHERE 子句。
// 注意：筛选条件作用于"每组最新的 finding"，而非组内所有 finding。
func aggregationWhere(f AggregationFilter) (string, []any) {
	where := make([]string, 0, 4)
	args := make([]any, 0, 4)
	idx := 1

	add := func(clause string, val any) {
		where = append(where, fmt.Sprintf(clause, idx))
		args = append(args, val)
		idx++
	}

	// 筛选维度作用于"最新"字段
	if f.Host != "" {
		add("g.latest_host = $%d", f.Host)
	}
	if f.Severity != "" {
		add("g.latest_severity = $%d", f.Severity)
	}
	if f.Status != "" {
		add("g.latest_status = $%d", f.Status)
	}
	if f.Source != "" {
		add("g.latest_source = $%d", f.Source)
	}

	if len(where) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(where, " AND "), args
}

func scanAggregated(row pgx.Row, a *AggregatedFinding) error {
	return row.Scan(
		&a.AggregateKey,
		&a.LatestID,
		&a.LatestSummary,
		&a.LatestSeverity,
		&a.LatestStatus,
		&a.LatestCWEID,
		&a.LatestHost,
		&a.LatestTarget,
		&a.LatestEval,
		&a.LatestSource,
		&a.OccurrenceCount,
		&a.FirstSeenAt,
		&a.LastSeenAt,
		&a.DaysSinceFirst,
		&a.OpenCount,
		&a.ConfirmedCount,
		&a.FixedCount,
		&a.FalsePositiveCount,
		&a.AcceptedCount,
		&a.FindingIDs,
		&a.Remediation,
	)
}
