package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/flow"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres 封装 pgxpool，提供 http_flow 冷层批量写入。
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres 构造 Postgres 存储；连接池由调用方管理生命周期。
func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

// BatchInsert 将一批 flow 写入 http_flow 分区表（当前写入默认分区）。
// 使用 pgx CopyFrom 协议减少往返次数；空批次直接返回。
func (s *Postgres) BatchInsert(ctx context.Context, flows []flow.Flow) error {
	if len(flows) == 0 {
		return nil
	}

	cols := []string{
		"id", "engagement_id", "ts",
		"method", "url",
		"req_content_type", "req_headers", "req_body",
		"resp_status",
		"resp_content_type", "resp_headers", "resp_body",
		"body_truncated",
	}

	rows := make([][]any, 0, len(flows))
	for _, f := range flows {
		reqH, err := marshalHeaders(f.ReqHeaders)
		if err != nil {
			return fmt.Errorf("marshal req_headers: %w", err)
		}
		respH, err := marshalHeaders(f.RespHeaders)
		if err != nil {
			return fmt.Errorf("marshal resp_headers: %w", err)
		}
		rows = append(rows, []any{
			f.ID, f.EngagementID, f.Timestamp,
			nilIfEmpty(f.Method), nilIfEmpty(f.URL),
			nilIfEmpty(f.ReqContentType), reqH, nilBytesIfEmpty(f.ReqBody),
			f.RespStatus,
			nilIfEmpty(f.RespContentType), respH, nilBytesIfEmpty(f.RespBody),
			f.BodyTruncated,
		})
	}

	n, err := s.pool.CopyFrom(ctx,
		pgx.Identifier{"http_flow"},
		cols,
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return fmt.Errorf("copy http_flow: %w", err)
	}
	if n != int64(len(flows)) {
		return fmt.Errorf("期望插入 %d 行，实际 %d 行", len(flows), n)
	}
	return nil
}

func marshalHeaders(h map[string]string) ([]byte, error) {
	if len(h) == 0 {
		return nil, nil
	}
	return json.Marshal(h)
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nilBytesIfEmpty(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
