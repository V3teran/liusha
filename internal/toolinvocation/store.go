package toolinvocation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
)

// previewMax 是 output_preview 列的字节上限（截到字符边界）。截断只在这里做一次。
const previewMax = 4096

// Store 封装 tool_invocation 表的所有持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Append 单条插入。args 为 nil 时落空 jsonb；Output 传完整输出，由本函数生成预览。
func (s *Store) Append(ctx context.Context, v Invocation) (int64, error) {
	if v.AgentRunID == "" {
		return 0, fmt.Errorf("tool_invocation: AgentRunID 必填")
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

	outputPreview := preview(v.Output)

	var errMsg any
	if v.ErrorMessage != "" {
		errMsg = sanitizeUTF8(v.ErrorMessage)
	}

	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO tool_invocation
			(agent_run_id, task_id, role, tool_name, args,
			 output_size, output_preview, duration_ms, error_message)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id`,
		v.AgentRunID, v.TaskID, v.Role, v.ToolName, args,
		v.OutputSize, outputPreview, v.DurationMs, errMsg,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("append tool_invocation: %w", err)
	}
	return id, nil
}

// Aggregate 是某 task 下所有工具调用的合计（会话工具耗时总览）。
type Aggregate struct {
	Calls      int   // 工具调用次数
	DurationMs int64 // 工具执行耗时合计（ms）
}

// AggregateByTask 合计某 task 的全部 tool_invocation 用量。task 无记录时返回零值。
func (s *Store) AggregateByTask(ctx context.Context, taskID string) (Aggregate, error) {
	var a Aggregate
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(SUM(duration_ms),0)
		FROM tool_invocation WHERE task_id=$1::uuid`, taskID).
		Scan(&a.Calls, &a.DurationMs)
	if err != nil {
		return Aggregate{}, fmt.Errorf("aggregate tool_invocation by task %s: %w", taskID, err)
	}
	return a, nil
}

// preview 生成落库的输出预览：先把非法字节替换为替换字符，再按字节上限截到字符边界。
// 不猜测其它编码：截断不当或混入二进制都会让串非法，把它按某种编码重新解码只会造出乱码。
func preview(s string) string {
	// 先截再清洗：输出可能有数 MB，没必要整段清洗；替换字符比坏字节长，清洗后再截一次。
	return truncateUTF8(sanitizeUTF8(truncateUTF8(s, previewMax)), previewMax)
}

// sanitizeUTF8 把非法 UTF-8 字节序列替换为替换字符（PG text 列要求合法 UTF-8）。
func sanitizeUTF8(s string) string {
	return strings.ToValidUTF8(s, "\uFFFD")
}

// truncateUTF8 按字节上限截断，并退回到字符边界：截在多字节字符中间会留下非法字节。
func truncateUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	end := max(limit, 0)
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}
