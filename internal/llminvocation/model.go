// Package llminvocation 实现 llm_invocation 表持久化层：每一次外部 LLM 调用
// （含失败）落一行用于 token 用量 + 路由审计。
//
// 列命名约定：
//   - role 是"调用者角色"（认知循环 planner/executor/evaluator/monitor + api 轻量路由 inspector 等），与 OpenAI message.role 区分
//   - messages / result 是 jsonb 列（完整输入/输出 payload，审计回放用）
package llminvocation

import "time"

// Invocation 是 llm_invocation 表行的 Go 表示。AgentRunID 可空（外键 SET NULL）。
//
// Messages / Result 是完整的输入/输出 payload，用于审计与回放。
// 始终落库（不脱敏、不开关），cookie 等敏感头会原样保留。
// 类型为 []byte，由调用方 json.Marshal 后写入；nil 时落 default '[]' / '{}'。
//
// ConversationID / AssignmentID 是派生字段（JOIN 查询填充），不存储在 llm_invocation 表。
type Invocation struct {
	ID             int64
	RequestID      string  // 跨系统关联键,db 侧 gen_random_uuid() 生成（写路径不传，见 store.go copyFromBatch）
	AgentRunID     *string // 所属 agent_run.id（可空：SET NULL 外键；列名 agent_run_id）
	TaskID         *string // 所属 task.id（可空：SET NULL 外键）
	ConversationID *string // 所属 conversation.id（派生字段，JOIN 查询填充）
	AssignmentID   *string // 所属 assignment.id（派生字段，JOIN 查询填充）
	Provider       string
	Model          string
	InTokens       int
	OutTokens      int
	CachedTokens   int
	LatencyMs      int
	FinishReason   string
	Error          string
	// ToolNames / TextPreview 是「这次调用产出了什么」的派生摘要，仅列表查询填充
	// （由 SQL 从 result 里算出，不把 result 大字段传出库）。审计表据此一眼看出
	// 该次调用是派活、跑命令还是写漏洞，而不必逐行点开详情。
	ToolNames   []string
	TextPreview string
	Role        string // 调用者角色：planner / executor / evaluator / monitor / inspector 等；空 = 未分类
	Messages    []byte // jsonb：输入消息数组（[]llm.Message 序列化）
	Result      []byte // jsonb：LLM 返回（llm.Result 序列化）
	CreatedAt   time.Time
}
