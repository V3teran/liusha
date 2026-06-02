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
