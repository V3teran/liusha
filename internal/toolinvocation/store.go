package toolinvocation

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// previewMax 是 output_preview 列的最大字符数；超过截断。
const previewMax = 4096

// Store 封装 tool_invocation 表的所有持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT 路径的统一列序，与 collect() 字段一一对应。
const colsSelect = "id, hunter_id::text, owner_type, owner_id::text, " +
	"tool_name, args, output_size, output_preview, duration_ms, " +
	"COALESCE(error_message, ''), done, created_at"

// Append 单条插入。args 为 nil 时落空 jsonb；output 超 previewMax 自动截断 preview。
func (s *Store) Append(ctx context.Context, v Invocation) (int64, error) {
	if v.HunterID == "" {
		return 0, fmt.Errorf("tool_invocation: HunterID 必填")
	}
	if v.OwnerType == "" || v.OwnerID == "" {
		return 0, fmt.Errorf("tool_invocation: OwnerType + OwnerID 必填")
	}
	if v.ToolName == "" {
		return 0, fmt.Errorf("tool_invocation: ToolName 必填")
	}
	args := v.Args
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	preview := truncateUTF8(v.OutputPreview, previewMax)
	var errMsg any
	if v.ErrorMessage != "" {
		errMsg = v.ErrorMessage
	}

	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO tool_invocation
			(hunter_id, owner_type, owner_id, tool_name, args,
			 output_size, output_preview, duration_ms, error_message, done)
		VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id`,
		v.HunterID, v.OwnerType, v.OwnerID, v.ToolName, args,
		v.OutputSize, preview, v.DurationMs, errMsg, v.Done,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("append tool_invocation: %w", err)
	}
	return id, nil
}

// ListByTask 按 created_at ASC 列出指定 hunter run 的全部工具调用。
func (s *Store) ListByTask(ctx context.Context, hunterID string) ([]Invocation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+colsSelect+`
		FROM tool_invocation
		WHERE hunter_id=$1::uuid
		ORDER BY created_at ASC, id ASC`, hunterID)
	if err != nil {
		return nil, fmt.Errorf("list tool_invocation by task: %w", err)
	}
	defer rows.Close()
	return collect(rows)
}

// ListByOwnerID 按 created_at ASC 列出指定 owner 的全部工具调用。
func (s *Store) ListByOwnerID(ctx context.Context, ownerID string) ([]Invocation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+colsSelect+`
		FROM tool_invocation
		WHERE owner_id=$1::uuid
		ORDER BY created_at ASC, id ASC`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list tool_invocation by owner: %w", err)
	}
	defer rows.Close()
	return collect(rows)
}

// CountByName 按 tool_name 聚合统计 owner 范围内每个工具的调用次数。
// 用于 telemetry "本次扫描跑了多少次 sqlmap / curl / write_finding"。
func (s *Store) CountByName(ctx context.Context, ownerID string) (map[string]int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT tool_name, count(*)
		FROM tool_invocation
		WHERE owner_id=$1::uuid
		GROUP BY tool_name`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("count tool_invocation by name: %w", err)
	}
	defer rows.Close()
	out := make(map[string]int)
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			return nil, fmt.Errorf("scan tool count: %w", err)
		}
		out[name] = n
	}
	return out, rows.Err()
}

// truncateUTF8 按字节上限截断，但保证不切到 multi-byte rune 中间——
// PG text 列要求合法 UTF-8，原始 string(bytes)[:max] 若切到 0xe6 0x97 (3-byte rune 中间)
// 会触发 SQLSTATE 22021。本函数从 max 处向前回退到上一个完整 rune 边界。
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// 向前回退到 ASCII 字节或 UTF-8 leading byte（0xxxxxxx 或 11xxxxxx）。
	// continuation byte 是 10xxxxxx，必须跳过。
	for end := max; end > 0; end-- {
		b := s[end-1]
		if b < 0x80 || b >= 0xC0 {
			// 该位置是 ASCII 或 leading byte——但 leading byte 自己也要看它能否容下完整 rune。
			// 简化处理：如果 end-1 是 leading byte，再退一位（不含本 rune）。
			if b >= 0xC0 {
				return s[:end-1]
			}
			return s[:end]
		}
	}
	return ""
}

// collect 通用列表收集器。
func collect(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]Invocation, error) {
	var out []Invocation
	for rows.Next() {
		var v Invocation
		if err := rows.Scan(
			&v.ID, &v.HunterID, &v.OwnerType, &v.OwnerID,
			&v.ToolName, &v.Args, &v.OutputSize, &v.OutputPreview, &v.DurationMs,
			&v.ErrorMessage, &v.Done, &v.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan tool_invocation: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
