package traffic

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AgentStore 封装 agent_traffic 表——agent 自产流量，按 task 归属，是 replay/list/view_traffic 弹药。
type AgentStore struct {
	pool        *pgxpool.Pool
	maxReqBody  int
	maxRespBody int
}

// NewAgentStore 构造 AgentStore（body 截断阈值默认 32 KiB）。
func NewAgentStore(pool *pgxpool.Pool) *AgentStore {
	return &AgentStore{pool: pool, maxReqBody: defaultMaxBody, maxRespBody: defaultMaxBody}
}

const agentCols = "id, task_id::text, COALESCE(agent_run_id::text, ''), " +
	"COALESCE(identity, ''), COALESCE(tool, ''), host, method, url, path, " +
	"request_headers, request_body, status_code, response_headers, response_body, duration_ms, created_at"

const agentSummaryCols = "id, task_id::text, COALESCE(agent_run_id::text, ''), " +
	"COALESCE(identity, ''), COALESCE(tool, ''), host, method, url, path, " +
	"status_code, duration_ms, created_at"

// Append 单条落库（带截断），返回 bigserial id。host / path 缺省时从 url 抽取。
func (s *AgentStore) Append(ctx context.Context, f AgentTraffic) (int64, error) {
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
		INSERT INTO agent_traffic
			(task_id, agent_run_id, identity, tool, host, method, url, path, status_code,
			 request_headers, request_body, response_headers, response_body, duration_ms)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id`,
		f.TaskID, nullIfEmpty(f.AgentID), nullIfEmpty(f.Identity), nullIfEmpty(f.Tool),
		host, f.Method, f.URL, path, f.StatusCode,
		normalizeHeaders(f.RequestHeaders), truncate(f.RequestBody, s.maxReqBody),
		normalizeHeaders(f.ResponseHeaders), truncate(f.ResponseBody, s.maxRespBody), f.DurationMs,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("append agent_traffic: %w", err)
	}
	return id, nil
}

// GetByID 读单行（含 body）。
func (s *AgentStore) GetByID(ctx context.Context, id int64) (AgentTraffic, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+agentCols+" FROM agent_traffic WHERE id=$1", id)
	var f AgentTraffic
	if err := scanAgent(row, &f); err != nil {
		return AgentTraffic{}, fmt.Errorf("get agent_traffic %d: %w", id, err)
	}
	return f, nil
}

// ListByTaskFiltered 在 task 范围内按多维过滤查 agent_traffic 摘要（list_traffic 工具用）。
// 按 created_at DESC 排（最新优先）。
func (s *AgentStore) ListByTaskFiltered(ctx context.Context, taskID string, f AgentListFilter) ([]AgentSummary, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	q := "SELECT " + agentSummaryCols + " FROM agent_traffic WHERE task_id=$1::uuid"
	args := []any{taskID}
	add := func(cond string, val any) {
		args = append(args, val)
		q += " AND " + fmt.Sprintf(cond, len(args))
	}
	if f.Host != "" {
		// host 列存的是 host:port，不能剥端口去比——见 proxy_store.go 同处注释。
		add("host=$%d", f.Host)
	}
	if f.Method != "" {
		add("method=$%d", strings.ToUpper(f.Method))
	}
	if f.Path != "" {
		pattern := strings.ReplaceAll(sqlEscapeLike(f.Path), "*", "%")
		add("path LIKE $%d", pattern)
	}
	if f.Identity != "" {
		add("identity=$%d", f.Identity)
	}
	if f.Tool != "" {
		add("tool=$%d", f.Tool)
	}
	if f.StatusMin > 0 {
		add("status_code >= $%d", f.StatusMin)
	}
	if f.StatusMax > 0 {
		add("status_code <= $%d", f.StatusMax)
	}
	if !f.Since.IsZero() {
		add("created_at >= $%d", f.Since)
	}
	args = append(args, f.Limit, f.Offset)
	q += fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list agent_traffic filtered: %w", err)
	}
	defer rows.Close()

	var out []AgentSummary
	for rows.Next() {
		var sum AgentSummary
		if err := rows.Scan(&sum.ID, &sum.TaskID, &sum.AgentID, &sum.Identity, &sum.Tool,
			&sum.Host, &sum.Method, &sum.URL, &sum.Path,
			&sum.StatusCode, &sum.DurationMs, &sum.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan agent_traffic summary: %w", err)
		}
		out = append(out, sum)
	}
	return out, rows.Err()
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法（本包共用）。
type scanner interface {
	Scan(dest ...any) error
}

func scanAgent(r scanner, f *AgentTraffic) error {
	var reqH, respH []byte
	if err := r.Scan(&f.ID, &f.TaskID, &f.AgentID, &f.Identity, &f.Tool,
		&f.Host, &f.Method, &f.URL, &f.Path,
		&reqH, &f.RequestBody, &f.StatusCode, &respH, &f.ResponseBody, &f.DurationMs, &f.CreatedAt); err != nil {
		return err
	}
	f.RequestHeaders = reqH
	f.ResponseHeaders = respH
	return nil
}
