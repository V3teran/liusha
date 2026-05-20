package flow

import (
	"context"
	"encoding/json"
	"fmt"

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

// flowSelectCols 是 GetByID 的统一列序，与 scanFlow() 字段一一对应。
const flowSelectCols = "id, passive_session_id::text, " +
	"created_at, method, url, request_headers, request_body, " +
	"status_code, response_headers, response_body"

// summaryCols 是 ListByOwner 的瘦列序，刻意不含 body / headers，避免大 payload。
// FlowSummary.PassiveSessionID 字段直接对应 passive_session.id（前端 viewer 通过 JSON tag 取值）。
const summaryCols = "id, passive_session_id::text, created_at, method, url, status_code"

// copyFromCols 是 CopyFrom 写入的列名顺序，必须与每行 []any 的元素顺序严格对齐。
var copyFromCols = []string{
	"passive_session_id", "method", "url",
	"request_headers", "request_body",
	"status_code", "response_headers", "response_body",
}

// Append 单条插入（带截断），返回 bigserial id。
func (s *Store) Append(ctx context.Context, f Flow) (int64, error) {
	reqBody, _ := truncate(f.RequestBody, s.maxReqBody)
	respBody, _ := truncate(f.ResponseBody, s.maxRespBody)
	reqH := normalizeHeaders(f.RequestHeaders)
	respH := normalizeHeaders(f.ResponseHeaders)

	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO http_flow
			(passive_session_id, method, url, request_headers, request_body,
			 status_code, response_headers, response_body)
		VALUES ($1::uuid, $2,$3,$4,$5,$6,$7,$8)
		RETURNING id`,
		f.PassiveSessionID, f.Method, f.URL,
		reqH, reqBody,
		f.StatusCode, respH, respBody).Scan(&id)
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
		rows[i] = []any{
			f.PassiveSessionID, f.Method, f.URL,
			normalizeHeaders(f.RequestHeaders), reqBody,
			f.StatusCode, normalizeHeaders(f.ResponseHeaders), respBody,
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

// ListByOwner 按 (ts, id) 升序分页列出  owner / passive_session 的瘦摘要（不含 body / headers）。
//
// 双轨切读：参数 ID 可以是旧 owner.id 或新 passive_session.id。SQL OR 让 viewer 传新 ID
// 时也命中。commit B5+ 完成数据回填后可改名为 ListByOwner。
func (s *Store) ListByOwner(ctx context.Context, ownerID string, limit, offset int) ([]FlowSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+summaryCols+`
		FROM http_flow
		WHERE passive_session_id=$1::uuid
		ORDER BY created_at ASC, id ASC
		LIMIT $2 OFFSET $3`, ownerID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list flows: %w", err)
	}
	defer rows.Close()

	var out []FlowSummary
	for rows.Next() {
		var sum FlowSummary
		if err := rows.Scan(&sum.ID, &sum.PassiveSessionID, &sum.CreatedAt, &sum.Method, &sum.URL,
			&sum.StatusCode); err != nil {
			return nil, fmt.Errorf("scan flow summary: %w", err)
		}
		out = append(out, sum)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate flow summaries: %w", err)
	}
	return out, nil
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scanFlow 是 flowSelectCols 列序的统一反序列化点。
func scanFlow(r scanner, f *Flow) error {
	var reqH, respH []byte
	if err := r.Scan(&f.ID, &f.PassiveSessionID, &f.CreatedAt, &f.Method, &f.URL,
		&reqH, &f.RequestBody,
		&f.StatusCode, &respH, &f.ResponseBody); err != nil {
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
