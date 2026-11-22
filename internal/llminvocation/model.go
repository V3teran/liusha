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
// 归属：智能体运行的调用记 TaskID（经任务找对话）；对话里直接发起的调用（问答、意图分类、
// 标题生成）没有任务，记 ConversationID。读出时 ConversationID 是调用所属的对话（自己记下的
// 优先，否则任务的对话），AssignmentID / Source 是归属任务的批次与来源（JOIN 填充）。
type Invocation struct {
	ID             int64
	AgentRunID     *string // 所属 agent_run.id（可空：SET NULL 外键；列名 agent_run_id）
	TaskID         *string // 所属 task.id（可空：SET NULL 外键）
	ConversationID *string // 写入：对话内调用所在的对话；读出：调用所属的对话（可空：SET NULL 外键）
	AssignmentID   *string // 所属 assignment.id（派生字段，JOIN 查询填充）
	Source         string  // 下发来源 direct|proxy（派生字段，详情 JOIN assignment 填充；对话跳转定 tab）
	Provider       string
	Model          string
	InTokens       int
	OutTokens      int
	CachedTokens   int
	LatencyMs      int
	FinishReason   string
	Error          string
	ErrorKind      string // rate_limited|network|server|error（空=成功）；写入时按框架错误分类判定
	Role           string // 调用者角色：planner / executor / evaluator / monitor / inspector 等；空 = 未分类
	Messages       []byte // jsonb：输入消息数组（[]llm.Message 序列化）
	Result         []byte // jsonb：LLM 返回（llm.Result 序列化）
	CreatedAt      time.Time
}
