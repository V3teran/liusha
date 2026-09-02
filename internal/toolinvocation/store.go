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
	if v.ExecutorID == "" {
		return 0, fmt.Errorf("tool_invocation: ExecutorID 必填")
	}
	if v.TaskID == "" {
		return 0, fmt.Errorf("tool_invocation: TaskID 必填")
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
			(agent_id, task_id, tool_name, args,
			 output_size, output_preview, duration_ms, error_message, done)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id`,
		v.ExecutorID, v.TaskID, v.ToolName, args,
		v.OutputSize, preview, v.DurationMs, errMsg, v.Done,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("append tool_invocation: %w", err)
	}
	return id, nil
}

// Aggregate 是某 owner 下所有工具调用的合计（会话工具耗时总览）。
type Aggregate struct {
	Calls      int   // 工具调用次数
	DurationMs int64 // 工具执行耗时合计（ms）
}

// AggregateByTask 合计某 task 的全部 tool_invocation 用量（仅叶子工具）。task 无记录时返回零值。
//
// 排除 tool_name='task'：task 是"派发子代理"的工具，其 duration_ms 是子代理整段运行的墙钟，
// 已包含该子代理自身的 run_command 等叶子工具耗时（同 task 另有明细行）+ 子代理 LLM 耗时
// （单独计入 llm_invocation.latency_ms）。计入 task 会与这两者重叠，导致总耗时翻倍。
// 与 SSE 侧 scanagent.ScanEvent 的 task 排除同源。
func (s *Store) AggregateByTask(ctx context.Context, taskID string) (Aggregate, error) {
	var a Aggregate
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(SUM(duration_ms),0)
		FROM tool_invocation WHERE task_id=$1::uuid AND tool_name <> 'task'`, taskID).
		Scan(&a.Calls, &a.DurationMs)
	if err != nil {
		return Aggregate{}, fmt.Errorf("aggregate tool_invocation by task %s: %w", taskID, err)
	}
	return a, nil
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
