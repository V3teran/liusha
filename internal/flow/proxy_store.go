package flow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ProxyStore 封装 proxy_traffic 表——代理捕获流量，按 host 归属，先于 task。
type ProxyStore struct {
	pool        *pgxpool.Pool
	maxReqBody  int
	maxRespBody int
}

// NewProxyStore 构造 ProxyStore（body 截断阈值默认 32 KiB）。
func NewProxyStore(pool *pgxpool.Pool) *ProxyStore {
	return &ProxyStore{pool: pool, maxReqBody: defaultMaxBody, maxRespBody: defaultMaxBody}
}

const proxyCols = "id, host, method, scheme, url, path, " +
	"COALESCE(consumed_by_task_id::text, ''), status_code, " +
	"request_headers, request_body, response_headers, response_body, duration_ms, captured_at"

// Append 单条落库（带截断），返回 bigserial id。host / path 缺省时从 url 抽取。
func (s *ProxyStore) Append(ctx context.Context, f ProxyTraffic) (int64, error) {
	host := f.Host
	if host == "" {
		host = extractHost(f.URL)
	}
	path := f.Path
	if path == "" {
		path = extractPath(f.URL)
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO proxy_traffic
			(host, method, scheme, url, path, status_code,
			 request_headers, request_body, response_headers, response_body, duration_ms)
		VALUES ($1,$2,$3,$4,$5,$6, $7,$8,$9,$10,$11)
		RETURNING id`,
		host, f.Method, f.Scheme, f.URL, path, f.StatusCode,
		normalizeHeaders(f.RequestHeaders), truncate(f.RequestBody, s.maxReqBody),
		normalizeHeaders(f.ResponseHeaders), truncate(f.ResponseBody, s.maxRespBody), f.DurationMs,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("append proxy_traffic: %w", err)
	}
	return id, nil
}

// GetByID 读单行（含 body）。
func (s *ProxyStore) GetByID(ctx context.Context, id int64) (ProxyTraffic, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+proxyCols+" FROM proxy_traffic WHERE id=$1", id)
	var f ProxyTraffic
	if err := scanProxy(row, &f); err != nil {
		return ProxyTraffic{}, fmt.Errorf("get proxy_traffic %d: %w", id, err)
	}
	return f, nil
}

// ClaimUnconsumedByHost 原子领取某 host 最早的至多 limit 条未消费流量：把它们的
// consumed_by_task_id 从 NULL 置为 taskID，返回实际领到的条数（见 spec §13.2）。
//
// 用子查询 + FOR UPDATE SKIP LOCKED 锁定候选行再更新，条件 consumed_by_task_id IS NULL
// 保证跨实例不重复领取（抢到 0 行说明被别的实例先占，调用方据此放弃本批，幂等）。
func (s *ProxyStore) ClaimUnconsumedByHost(ctx context.Context, taskID, host string, limit int) (int64, error) {
	if limit <= 0 {
		limit = 20
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE proxy_traffic SET consumed_by_task_id=$1::uuid
		WHERE id IN (
			SELECT id FROM proxy_traffic
			WHERE host=$2 AND consumed_by_task_id IS NULL
			ORDER BY captured_at ASC, id ASC
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		)`, taskID, host, limit)
	if err != nil {
		return 0, fmt.Errorf("claim proxy_traffic for task %s host %s: %w", taskID, host, err)
	}
	return tag.RowsAffected(), nil
}

// ProxySummary 是 proxy_traffic 的瘦行（list_flows 用）：不含 body / headers。
type ProxySummary struct {
	ID         int64
	Host       string
	Method     string
	Path       string
	StatusCode int
	DurationMs int
	CapturedAt time.Time
}

// ListByTaskFiltered 在某 passive task 消费的流量范围内按多维过滤查摘要（list_flows 工具用）。
// 按 captured_at DESC 排（最新优先）。
func (s *ProxyStore) ListByTaskFiltered(ctx context.Context, taskID string, f ProxyListFilter) ([]ProxySummary, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	q := "SELECT id, host, method, path, status_code, duration_ms, captured_at " +
		"FROM proxy_traffic WHERE consumed_by_task_id=$1::uuid"
	args := []any{taskID}
	add := func(cond string, val any) {
		args = append(args, val)
		q += " AND " + fmt.Sprintf(cond, len(args))
	}
	if f.Host != "" {
		add("host=$%d", stripHostPort(f.Host))
	}
	if f.Method != "" {
		add("method=$%d", strings.ToUpper(f.Method))
	}
	if f.Path != "" {
		add("path LIKE $%d", strings.ReplaceAll(sqlEscapeLike(f.Path), "*", "%"))
	}
	if f.StatusMin > 0 {
		add("status_code >= $%d", f.StatusMin)
	}
	if f.StatusMax > 0 {
		add("status_code <= $%d", f.StatusMax)
	}
	args = append(args, f.Limit, f.Offset)
	q += fmt.Sprintf(" ORDER BY captured_at DESC, id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list proxy_traffic filtered: %w", err)
	}
	defer rows.Close()

	var out []ProxySummary
	for rows.Next() {
		var sum ProxySummary
		if err := rows.Scan(&sum.ID, &sum.Host, &sum.Method, &sum.Path,
			&sum.StatusCode, &sum.DurationMs, &sum.CapturedAt); err != nil {
			return nil, fmt.Errorf("scan proxy_traffic summary: %w", err)
		}
		out = append(out, sum)
	}
	return out, rows.Err()
}

// ListByTask 按 captured_at 升序列出某 passive task 消费的流量（含 body），供 traffic-analysis 读这批。
func (s *ProxyStore) ListByTask(ctx context.Context, taskID string) ([]ProxyTraffic, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT "+proxyCols+" FROM proxy_traffic WHERE consumed_by_task_id=$1::uuid ORDER BY captured_at ASC, id ASC",
		taskID)
	if err != nil {
		return nil, fmt.Errorf("list proxy_traffic by task: %w", err)
	}
	defer rows.Close()

	var out []ProxyTraffic
	for rows.Next() {
		var f ProxyTraffic
		if err := scanProxy(rows, &f); err != nil {
			return nil, fmt.Errorf("scan proxy_traffic: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// MonitoredHost 是「监控中 host 列表」的一行（从 proxy_traffic 派生，见 spec §6.4）。
type MonitoredHost struct {
	Host       string
	LastSeenAt string
	FlowCount  int
}

// ListMonitoredHosts 派生「最近 N 秒内有流量」的 host（前端监控列表），不落标志表。
func (s *ProxyStore) ListMonitoredHosts(ctx context.Context, withinSeconds int) ([]MonitoredHost, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT host, max(captured_at)::text, count(*)
		FROM proxy_traffic
		WHERE captured_at > now() - ($1::text)::interval
		GROUP BY host
		ORDER BY max(captured_at) DESC`,
		fmt.Sprintf("%d seconds", withinSeconds))
	if err != nil {
		return nil, fmt.Errorf("list monitored hosts: %w", err)
	}
	defer rows.Close()

	var out []MonitoredHost
	for rows.Next() {
		var m MonitoredHost
		if err := rows.Scan(&m.Host, &m.LastSeenAt, &m.FlowCount); err != nil {
			return nil, fmt.Errorf("scan monitored host: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanProxy(r scanner, f *ProxyTraffic) error {
	var reqH, respH []byte
	if err := r.Scan(&f.ID, &f.Host, &f.Method, &f.Scheme, &f.URL, &f.Path,
		&f.ConsumedByTaskID, &f.StatusCode,
		&reqH, &f.RequestBody, &respH, &f.ResponseBody, &f.DurationMs, &f.CapturedAt); err != nil {
		return err
	}
	f.RequestHeaders = reqH
	f.ResponseHeaders = respH
	return nil
}
