package flow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 http_flow 表的所有持久化操作。
// maxReqBody / maxRespBody <= 0 表示不截断。
type Store struct {
	pool        *pgxpool.Pool
	maxReqBody  int
	maxRespBody int
}

// NewStore 构造 Store。建议 maxReqBody=maxRespBody=32*1024（spec 32 KiB 截断阈值）。
func NewStore(pool *pgxpool.Pool, maxReqBody, maxRespBody int) *Store {
	return &Store{pool: pool, maxReqBody: maxReqBody, maxRespBody: maxRespBody}
}

// flowSelectCols 是 GetByID 的统一列序，与 scanFlow() 字段一一对应（0060 新字段已纳入）。
const flowSelectCols = "id, owner_type, owner_id::text, source, " +
	"COALESCE(hunter_id::text, ''), host, created_at, method, url, path, " +
	"request_headers, request_body, " +
	"status_code, response_headers, response_body, duration_ms"

// summaryCols 是 ListByOwner 的瘦列序，刻意不含 body / headers，避免大 payload。
const summaryCols = "id, owner_type, owner_id::text, source, " +
	"COALESCE(hunter_id::text, ''), host, created_at, method, url, path, " +
	"status_code, duration_ms"

// copyFromCols 是 CopyFrom 写入的列名顺序，必须与每行 []any 的元素顺序严格对齐。
var copyFromCols = []string{
	"owner_type", "owner_id", "source", "hunter_id",
	"host", "method", "url", "path",
	"request_headers", "request_body",
	"status_code", "response_headers", "response_body", "duration_ms",
}

// hunterIDArg 把空字符串 HunterID 转 nil（用于 pgx 写 NULL），非空原样返回。
// pgx INSERT 用 nil 写 NULL；CopyFrom 同理。
func hunterIDArg(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Append 单条插入（带截断），返回 bigserial id。
func (s *Store) Append(ctx context.Context, f Flow) (int64, error) {
	reqBody, _ := truncate(f.RequestBody, s.maxReqBody)
	respBody, _ := truncate(f.ResponseBody, s.maxRespBody)
	reqH := normalizeHeaders(f.RequestHeaders)
	respH := normalizeHeaders(f.ResponseHeaders)

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
		INSERT INTO http_flow
			(owner_type, owner_id, source, hunter_id,
			 host, method, url, path,
			 request_headers, request_body,
			 status_code, response_headers, response_body, duration_ms)
		VALUES ($1, $2::uuid, $3, $4::uuid,
		        $5, $6, $7, $8,
		        $9, $10,
		        $11, $12, $13, $14)
		RETURNING id`,
		f.OwnerType, f.OwnerID, f.Source, hunterIDArg(f.HunterID),
		host, f.Method, f.URL, path,
		reqH, reqBody,
		f.StatusCode, respH, respBody, f.DurationMs).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("append flow: %w", err)
	}
	return id, nil
}

// AppendBatch 用 pgx.CopyFrom 批量插入，性能远高于逐行 INSERT。
// 空切片是合法的 no-op。注意：CopyFrom 不返回 RETURNING id。
func (s *Store) AppendBatch(ctx context.Context, flows []Flow) error {
	if len(flows) == 0 {
		return nil
	}
	rows := make([][]any, len(flows))
	for i, f := range flows {
		reqBody, _ := truncate(f.RequestBody, s.maxReqBody)
		respBody, _ := truncate(f.ResponseBody, s.maxRespBody)
		host := f.Host
		if host == "" {
			host = extractHost(f.URL)
		}
		path := f.Path
		if path == "" {
			path = extractPath(f.URL)
		}
		rows[i] = []any{
			f.OwnerType, f.OwnerID, f.Source, hunterIDArg(f.HunterID),
			host, f.Method, f.URL, path,
			normalizeHeaders(f.RequestHeaders), reqBody,
			f.StatusCode, normalizeHeaders(f.ResponseHeaders), respBody, f.DurationMs,
		}
	}
	_, err := s.pool.CopyFrom(ctx,
		pgx.Identifier{"http_flow"},
		copyFromCols,
		pgx.CopyFromRows(rows))
	if err != nil {
		return fmt.Errorf("copy from http_flow: %w", err)
	}
	return nil
}

// GetByID 读单行（含 body bytea）。
func (s *Store) GetByID(ctx context.Context, id int64) (Flow, error) {
	row := s.pool.QueryRow(ctx,
		"SELECT "+flowSelectCols+" FROM http_flow WHERE id=$1", id)
	var f Flow
	if err := scanFlow(row, &f); err != nil {
		return Flow{}, fmt.Errorf("get flow %d: %w", id, err)
	}
	return f, nil
}

// ListByOwner 按 (ts, id) 升序分页列出 owner 的瘦摘要（不含 body / headers）。
func (s *Store) ListByOwner(ctx context.Context, ownerID string, limit, offset int) ([]FlowSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+summaryCols+`
		FROM http_flow
		WHERE owner_id=$1::uuid
		ORDER BY created_at ASC, id ASC
		LIMIT $2 OFFSET $3`, ownerID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list flows: %w", err)
	}
	defer rows.Close()

	var out []FlowSummary
	for rows.Next() {
		var sum FlowSummary
		if err := rows.Scan(&sum.ID, &sum.OwnerType, &sum.OwnerID, &sum.Source,
			&sum.HunterID, &sum.Host, &sum.CreatedAt, &sum.Method, &sum.URL, &sum.Path,
			&sum.StatusCode, &sum.DurationMs); err != nil {
			return nil, fmt.Errorf("scan flow summary: %w", err)
		}
		out = append(out, sum)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate flow summaries: %w", err)
	}
	return out, nil
}

// ListFilter 是 ListByOwnerFiltered 的可选过滤条件；零值不过滤。
// Path 支持 glob：自动把 "*" 替换为 SQL '%'（caller 不需要预转义）。
// 时间倒序（最新优先），limit 上限由 caller 控制（典型 50-200）。
type ListFilter struct {
	Host      string    // 等值
	Method    string    // 等值，自动 upper-case
	Path      string    // glob，支持 '*'；空则不过滤
	Source    string    // 'external' / 'internal'；空则不过滤
	StatusMin int       // 状态码下界（如 400 = 仅 4xx/5xx）
	StatusMax int       // 状态码上界
	Since     time.Time // 仅看此时间后；零值不过滤
	Limit     int       // 必填，调用方控制 ≤ 200
	Offset    int       // 分页
}

// ListByOwnerFiltered 在 owner 范围内按多维过滤查 flow 摘要（list_flows 工具用）。
// 按 created_at DESC 排（最新优先）方便 LLM 看最近活动。
func (s *Store) ListByOwnerFiltered(ctx context.Context, ownerID string, f ListFilter) ([]FlowSummary, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	// 动态拼 WHERE：owner_id 必传，其余按非零值追加
	q := "SELECT " + summaryCols + " FROM http_flow WHERE owner_id=$1::uuid"
	args := []any{ownerID}
	add := func(cond string, val any) {
		args = append(args, val)
		q += " AND " + fmt.Sprintf(cond, len(args))
	}
	if f.Host != "" {
		add("host=$%d", f.Host)
	}
	if f.Method != "" {
		add("method=$%d", f.Method)
	}
	if f.Path != "" {
		// glob '*' → SQL '%'，转义已有 '%' '_' 避免 SQL 通配冲突
		pattern := f.Path
		if pattern != "" {
			pattern = sqlEscapeLike(pattern)
			pattern = strings.ReplaceAll(pattern, "*", "%")
		}
		add("path LIKE $%d", pattern)
	}
	if f.Source != "" {
		add("source=$%d", f.Source)
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
		return nil, fmt.Errorf("list filtered: %w", err)
	}
	defer rows.Close()

	var out []FlowSummary
	for rows.Next() {
		var sum FlowSummary
		if err := rows.Scan(&sum.ID, &sum.OwnerType, &sum.OwnerID, &sum.Source,
			&sum.HunterID, &sum.Host, &sum.CreatedAt, &sum.Method, &sum.URL, &sum.Path,
			&sum.StatusCode, &sum.DurationMs); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, sum)
	}
	return out, rows.Err()
}

// sqlEscapeLike 转义 LIKE 模式里的 % 和 _ —— 但保留 * （上层转换为 %）。
func sqlEscapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scanFlow 是 flowSelectCols 列序的统一反序列化点。
func scanFlow(r scanner, f *Flow) error {
	var reqH, respH []byte
	if err := r.Scan(&f.ID, &f.OwnerType, &f.OwnerID, &f.Source, &f.HunterID,
		&f.Host, &f.CreatedAt, &f.Method, &f.URL, &f.Path,
		&reqH, &f.RequestBody,
		&f.StatusCode, &respH, &f.ResponseBody, &f.DurationMs); err != nil {
		return err
	}
	f.RequestHeaders = json.RawMessage(reqH)
	f.ResponseHeaders = json.RawMessage(respH)
	return nil
}

// truncate 把超出 max 的 byte 切片截到 max，并返回是否截断；max<=0 表示禁用截断。
// 返回的切片是 b 前 max 字节的副本，避免持有 caller 大切片的底层数组。
func truncate(b []byte, max int) ([]byte, bool) {
	if max <= 0 || len(b) <= max {
		return b, false
	}
	out := make([]byte, max)
	copy(out, b[:max])
	return out, true
}

// normalizeHeaders 把空 RawMessage 折成 jsonb 空对象，避免列默认值与显式 NULL 的歧义。
func normalizeHeaders(h json.RawMessage) []byte {
	if len(h) == 0 {
		return []byte("{}")
	}
	return []byte(h)
}

// extractHost 从 URL 提取 host 部分（不含 port / path / query）。
// 与 0045 PG migration 的 substring regex 等价：scheme 可选，遇到 :/?# 中止。
func extractHost(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	s := rawURL
	for _, prefix := range []string{"https://", "http://"} {
		if len(s) > len(prefix) && s[:len(prefix)] == prefix {
			s = s[len(prefix):]
			break
		}
	}
	for i, r := range s {
		if r == '/' || r == ':' || r == '?' || r == '#' {
			return s[:i]
		}
	}
	return s
}

// extractPath 从 URL 抽 path（不含 query）。空或异常 fallback "/"。
// 与 0060 migration 回填 SQL 的 substring 正则等价。
func extractPath(rawURL string) string {
	if rawURL == "" {
		return "/"
	}
	s := rawURL
	for _, prefix := range []string{"https://", "http://"} {
		if len(s) > len(prefix) && s[:len(prefix)] == prefix {
			s = s[len(prefix):]
			break
		}
	}
	// 跳过 host[:port]
	slash := -1
	for i, r := range s {
		if r == '/' {
			slash = i
			break
		}
	}
	if slash < 0 {
		return "/"
	}
	s = s[slash:]
	// 去 query
	for i, r := range s {
		if r == '?' || r == '#' {
			return s[:i]
		}
	}
	return s
}
