package traffic

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

const proxyCols = "id, host, method, scheme, url, path, status_code, " +
	"request_raw, response_raw, content_type, http_version, resp_len, captured_at"

// proxyColsP 是 proxyCols 的 p. 前缀版（JOIN traffic_task 时消歧义列名）。
const proxyColsP = "p.id, p.host, p.method, p.scheme, p.url, p.path, p.status_code, " +
	"p.request_raw, p.response_raw, p.content_type, p.http_version, p.resp_len, p.captured_at"

// Append 单条落库（body 已在拼 raw 前截断），返回 bigserial id。host / path 缺省时从 url 抽取。
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
			 request_raw, response_raw, content_type, http_version, resp_len)
		VALUES ($1,$2,$3,$4,$5,$6, $7,$8,$9,$10,$11)
		RETURNING id`,
		host, f.Method, f.Scheme, f.URL, path, f.StatusCode,
		f.RequestRaw, f.ResponseRaw, f.ContentType, f.HTTPVersion, f.RespLen,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("append proxy_traffic: %w", err)
	}
	return id, nil
}

// GetByID 读单行（含 raw 报文），并 JOIN 装填消费本条的 passive task 列表（M:N，前端 chip 展示）。
func (s *ProxyStore) GetByID(ctx context.Context, id int64) (ProxyTraffic, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+proxyCols+" FROM proxy_traffic WHERE id=$1", id)
	var f ProxyTraffic
	if err := scanProxy(row, &f); err != nil {
		return ProxyTraffic{}, fmt.Errorf("get proxy_traffic %d: %w", id, err)
	}
	consumers, err := s.listConsumers(ctx, id)
	if err != nil {
		return ProxyTraffic{}, fmt.Errorf("get proxy_traffic %d consumers: %w", id, err)
	}
	f.ConsumedBy = consumers
	return f, nil
}

// listConsumers 查消费某条流量的全部 passive task（traffic_task JOIN task），供 chip 展示。
func (s *ProxyStore) listConsumers(ctx context.Context, trafficID int64) ([]ConsumerTask, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id::text, t._id, t.target_host, t.status
		FROM traffic_task tt
		JOIN task t ON t.id = tt.task_id
		WHERE tt.traffic_id = $1
		ORDER BY tt.created_at ASC`, trafficID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ConsumerTask
	for rows.Next() {
		var c ConsumerTask
		if err := rows.Scan(&c.TaskID, &c.ScenarioID, &c.Host, &c.Status); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ClaimUnconsumedByHost 原子领取某 host 最早的至多 limit 条「尚未被本 task 消费」的流量：
// 为它们在 traffic_task 建 (traffic_id, task_id) 关联，返回实际领到的条数（见 spec §13.2）。
//
// M:N 语义（v0100+）：一条流量可被多个 task 复用，故不再是「未消费才领」的排他更新，而是
// 「本 task 尚未关联过就领」。用 NOT EXISTS 排掉本 task 已关联的，FOR UPDATE SKIP LOCKED
// 锁定候选行防跨实例重复领取；ON CONFLICT DO NOTHING 兜底并发下的主键冲突（幂等）。
func (s *ProxyStore) ClaimUnconsumedByHost(ctx context.Context, taskID, host string, limit int) (int64, error) {
	if limit <= 0 {
		limit = 20
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO traffic_task (traffic_id, task_id)
		SELECT id, $1::uuid FROM proxy_traffic p
		WHERE p.host=$2
		  AND NOT EXISTS (
		      SELECT 1 FROM traffic_task tt
		      WHERE tt.traffic_id = p.id AND tt.task_id = $1::uuid)
		ORDER BY p.captured_at ASC, p.id ASC
		LIMIT $3
		FOR UPDATE OF p SKIP LOCKED
		ON CONFLICT DO NOTHING`, taskID, host, limit)
	if err != nil {
		return 0, fmt.Errorf("claim proxy_traffic for task %s host %s: %w", taskID, host, err)
	}
	return tag.RowsAffected(), nil
}

// ClaimByIDs 为某 task 显式领取指定 id 集合的流量（manual 下发带 TrafficIDs 的 M:N 路径）：
// 仅领确实存在的行，为每条建 traffic_task 关联，返回实际领到的条数。ids 为空返回 0。
// ON CONFLICT DO NOTHING 使重复下发幂等（已关联的不重复计入）。
func (s *ProxyStore) ClaimByIDs(ctx context.Context, taskID string, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO traffic_task (traffic_id, task_id)
		SELECT id, $1::uuid FROM proxy_traffic
		WHERE id = ANY($2::bigint[])
		ON CONFLICT DO NOTHING`, taskID, ids)
	if err != nil {
		return 0, fmt.Errorf("claim proxy_traffic by ids for task %s: %w", taskID, err)
	}
	return tag.RowsAffected(), nil
}

// ProxySummary 是 proxy_traffic 的瘦行（list_traffic 用）：不含 body / headers。
// URL 是完整请求 URL（前端列表 url 列直显，比裸 path 更可读）；
// ContentType 是响应主类型（列表 content-type 列 + 前端筛选 facet 命中值）。
type ProxySummary struct {
	ID          int64
	Host        string
	Method      string
	Path        string
	URL         string
	ContentType string
	StatusCode  int
	RespLen     int64 // 响应体字节数（前端「长度」列）
	CapturedAt  time.Time
}

// ListByTaskFiltered 在某 passive task 消费的流量范围内按多维过滤查摘要（list_traffic 工具用）。
// 按 captured_at DESC 排（最新优先）。
func (s *ProxyStore) ListByTaskFiltered(ctx context.Context, taskID string, f ProxyListFilter) ([]ProxySummary, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	q := "SELECT p.id, p.host, p.method, p.path, p.status_code, p.resp_len, p.captured_at " +
		"FROM proxy_traffic p JOIN traffic_task tt ON tt.traffic_id = p.id WHERE tt.task_id=$1::uuid"
	args := []any{taskID}
	add := func(cond string, val any) {
		args = append(args, val)
		q += " AND " + fmt.Sprintf(cond, len(args))
	}
	if f.Host != "" {
		// host 列存的是 host:port（与 task.target_host / credentials key 同格式），
		// 这里不能再剥端口去比——剥了就和存储值对不上，过滤永远零命中（2026-07-14 e2e 实测：
		// passive list_traffic 对已认领的 15 条流量返回空，根因就是这处误剥端口）。
		add("host=$%d", f.Host)
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
			&sum.StatusCode, &sum.RespLen, &sum.CapturedAt); err != nil {
			return nil, fmt.Errorf("scan proxy_traffic summary: %w", err)
		}
		out = append(out, sum)
	}
	return out, rows.Err()
}

// proxyWhere 把 ProxyListFilter 编译成 WHERE 片段 + 参数（全局浏览用，不含 task 归属约束）。
// startArg 是首个占位符序号（调用方已用掉的参数个数 + 1），返回拼好的条件串与追加的参数。
// 与 ListByTaskFiltered 同口径：host 等值不剥端口、method 自动 upper、path glob（'*'→'%'）。
func proxyWhere(f ProxyListFilter, startArg int) (string, []any) {
	var conds []string
	var args []any
	add := func(tmpl string, val any) {
		args = append(args, val)
		conds = append(conds, fmt.Sprintf(tmpl, startArg+len(args)-1))
	}
	if f.Host != "" {
		add("host=$%d", f.Host)
	}
	if f.Method != "" {
		add("method=$%d", strings.ToUpper(f.Method))
	}
	if f.Path != "" {
		add("path LIKE $%d", strings.ReplaceAll(sqlEscapeLike(f.Path), "*", "%"))
	}
	if f.ContentType != "" {
		add("content_type=$%d", f.ContentType)
	}
	if f.Search != "" {
		// 一个搜索框跨 host + url 两列：任一命中即可（OR），大小写不敏感（ILIKE），
		// '*' 视作通配（→'%'）；未含 '*' 时两端补 '%' 退化为子串包含，符合搜索框直觉。
		pat := strings.ReplaceAll(sqlEscapeLike(f.Search), "*", "%")
		if !strings.Contains(f.Search, "*") {
			pat = "%" + pat + "%"
		}
		args = append(args, pat)
		n := startArg + len(args) - 1
		conds = append(conds, fmt.Sprintf("(host ILIKE $%d OR url ILIKE $%d)", n, n))
	}
	if f.StatusMin > 0 {
		add("status_code >= $%d", f.StatusMin)
	}
	if f.StatusMax > 0 {
		add("status_code <= $%d", f.StatusMax)
	}
	if !f.Since.IsZero() {
		add("captured_at >= $%d", f.Since)
	}
	if !f.Until.IsZero() {
		add("captured_at <= $%d", f.Until)
	}
	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// ListPagedGlobal 跨全部 host 分页浏览代理捕获流量摘要（前端流量模块用），不限 task 归属。
// captured_at DESC（最新优先）。Limit<=0 兜底 50。offset 分页由调用方 clamp。
func (s *ProxyStore) ListPagedGlobal(ctx context.Context, f ProxyListFilter) ([]ProxySummary, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	where, args := proxyWhere(f, 1)
	args = append(args, f.Limit, f.Offset)
	q := "SELECT id, host, method, path, url, content_type, status_code, resp_len, captured_at FROM proxy_traffic" +
		where + fmt.Sprintf(" ORDER BY captured_at DESC, id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list proxy_traffic paged: %w", err)
	}
	defer rows.Close()

	var out []ProxySummary
	for rows.Next() {
		var sum ProxySummary
		if err := rows.Scan(&sum.ID, &sum.Host, &sum.Method, &sum.Path, &sum.URL,
			&sum.ContentType, &sum.StatusCode, &sum.RespLen, &sum.CapturedAt); err != nil {
			return nil, fmt.Errorf("scan proxy_traffic summary: %w", err)
		}
		out = append(out, sum)
	}
	return out, rows.Err()
}

// CountGlobal 返回同筛选口径下的全局总行数（分页 total）。
func (s *ProxyStore) CountGlobal(ctx context.Context, f ProxyListFilter) (int, error) {
	where, args := proxyWhere(f, 1)
	var n int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM proxy_traffic"+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count proxy_traffic: %w", err)
	}
	return n, nil
}

// DistinctHosts 返回 proxy_traffic 里出现过的全部 host（前端筛选下拉候选），按名排序。
// 全表 distinct，不吃筛选——候选恒为全集，否则筛完下拉自锁死（对齐 llm facets 口径）。
func (s *ProxyStore) DistinctHosts(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, "SELECT DISTINCT host FROM proxy_traffic ORDER BY host")
	if err != nil {
		return nil, fmt.Errorf("distinct proxy_traffic hosts: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("scan host: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// DistinctContentTypes 返回 proxy_traffic 里出现过的全部非空 content_type（前端筛选下拉候选），按名排序。
// 与 DistinctHosts 同口径：全表 distinct 不吃筛选（否则筛完下拉自锁死）；空串排除（无 Content-Type 的行不进候选）。
func (s *ProxyStore) DistinctContentTypes(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT DISTINCT content_type FROM proxy_traffic WHERE content_type <> '' ORDER BY content_type")
	if err != nil {
		return nil, fmt.Errorf("distinct proxy_traffic content_types: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var ct string
		if err := rows.Scan(&ct); err != nil {
			return nil, fmt.Errorf("scan content_type: %w", err)
		}
		out = append(out, ct)
	}
	return out, rows.Err()
}

// ListByTask 按 captured_at 升序列出某 passive task 消费的流量（含 raw 报文），供 traffic-analysis 读这批。
func (s *ProxyStore) ListByTask(ctx context.Context, taskID string) ([]ProxyTraffic, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT "+proxyColsP+" FROM proxy_traffic p JOIN traffic_task tt ON tt.traffic_id = p.id "+
			"WHERE tt.task_id=$1::uuid ORDER BY p.captured_at ASC, p.id ASC",
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
	TrafficCount  int
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
		if err := rows.Scan(&m.Host, &m.LastSeenAt, &m.TrafficCount); err != nil {
			return nil, fmt.Errorf("scan monitored host: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanProxy(r scanner, f *ProxyTraffic) error {
	return r.Scan(&f.ID, &f.Host, &f.Method, &f.Scheme, &f.URL, &f.Path, &f.StatusCode,
		&f.RequestRaw, &f.ResponseRaw, &f.ContentType, &f.HTTPVersion, &f.RespLen, &f.CapturedAt)
}
