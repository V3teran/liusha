package toolinvocation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/V3teran/liusha/internal/constants"
)

// output_preview 是工具输出的预览（字节上限 previewMax，截到字符边界）。工具交出的输出在这里之前
// 不被裁剪（工具结果进模型上下文的窗口在运行时，另算），所以预览的开头就是输出的开头。
const previewMax = 4096

// 预览超过 previewMax 时留开头与结尾：结尾是命令结果里退出码、完整输出文件路径所在——只取开头
// 会把末尾连同 exit code 一起切掉。留位算上中间的说明，预览总长不超过 previewMax。
const (
	previewTailBytes = 1000
	previewHeadBytes = previewMax - previewTailBytes - 96
)

// insertTimeout 是单条写入的时间上限：审计是尽力而为，数据库迟迟不响应时不能把调用方
// （工具执行的收尾）拖住。
const insertTimeout = constants.DBQueryTimeout

// Store 封装 tool_invocation 表的所有持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Append 单条插入。args 为 nil 时落空 jsonb；Output 传完整输出，由本函数生成预览。
//
// 写入不随 ctx 的取消而作废：调用方传进来的常是工具自己的 ctx，工具超时或被取消时它
// 已经结束，拿它写库必然失败——丢掉的偏偏是「这次工具调用为什么失败」。ctx 在这里只
// 用来传递取值，写入本身另有时间上限。
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

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), insertTimeout)
	defer cancel()

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

// RoleAggregate 是某一个调用方角色名下的工具用量合计。
type RoleAggregate struct {
	Role string
	Aggregate
}

// AggregateByTaskRole 按调用方角色分组合计某 task 的工具用量（任务详情的「智能体调用情况」），调用多的排前。
// task 无记录时返回空表；各分组的和等于 AggregateByTask 的结果。
func (s *Store) AggregateByTaskRole(ctx context.Context, taskID string) ([]RoleAggregate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(role, ''), COUNT(*), COALESCE(SUM(duration_ms),0)
		FROM tool_invocation WHERE task_id=$1::uuid
		GROUP BY role
		ORDER BY COUNT(*) DESC, role`, taskID)
	if err != nil {
		return nil, fmt.Errorf("aggregate tool_invocation by task and role %s: %w", taskID, err)
	}
	defer rows.Close()
	var out []RoleAggregate
	for rows.Next() {
		var r RoleAggregate
		if err := rows.Scan(&r.Role, &r.Calls, &r.DurationMs); err != nil {
			return nil, fmt.Errorf("scan tool_invocation aggregate by role: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// preview 生成落库的输出预览：不超过 previewMax 时原样（只清洗非法字节）；超过时留开头与结尾，
// 中间写明省略了多少字节。不猜测其它编码：截断不当或混入二进制都会让串非法，把它按某种编码
// 重新解码只会造出乱码。
func preview(s string) string {
	if len(s) <= previewMax {
		return sanitizeUTF8(s)
	}
	// 先截再清洗：输出可能有数 MB，没必要整段清洗；替换字符比坏字节长，清洗后各自再截一次。
	head := truncateUTF8(sanitizeUTF8(truncateUTF8(s, previewHeadBytes)), previewHeadBytes)
	tail := truncateUTF8(sanitizeUTF8(tailUTF8(s, previewTailBytes)), previewTailBytes)
	return head + fmt.Sprintf("…（中间省略 %d 字节）…", len(s)-len(head)-len(tail)) + tail
}

// tailUTF8 取 s 的末尾至多 limit 字节，开头退回到字符边界。
func tailUTF8(s string, limit int) string {
	start := len(s) - max(limit, 0)
	for start > 0 && start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
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

// RetentionBatch 是留存清理的单批行数（DeleteOlderThan 每次至多删这么多）。
const RetentionBatch = 5000

// DeleteOlderThan 删除 created_at 早于 cutoff 的调用记录，单批至多 RetentionBatch 行，返回
// 删除行数。批删而非一次删完：首次开启留存时存量可能几十万行，一条 DELETE 会长时间持锁；
// 调用方（runner 的统一留存循环）循环调到返回 0。
func (s *Store) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM tool_invocation
		WHERE id IN (SELECT id FROM tool_invocation WHERE created_at < $1 LIMIT $2)`,
		cutoff, RetentionBatch)
	if err != nil {
		return 0, fmt.Errorf("delete tool_invocation older than %s: %w", cutoff.Format(time.RFC3339), err)
	}
	return tag.RowsAffected(), nil
}
