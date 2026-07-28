// Package llminvocation 实现 llm_invocation 表持久化层：每一次外部 LLM 调用
// （含失败）落一行用于 token 用量 + 路由审计。
//
// 列命名约定：
//   - role 是"调用者角色"（trafficAnalysis/orchestrator/exploitation/inspector/react_main），与 OpenAI message.role 区分
//   - messages / result 是 jsonb 列（完整输入/输出 payload，审计回放用）
package llminvocation

import "time"

// Invocation 是 llm_invocation 表行的 Go 表示。HunterID 可空（外键 SET NULL）。
//
// Messages / Result 是完整的输入/输出 payload，用于审计与回放。
// 始终落库（不脱敏、不开关），cookie 等敏感头会原样保留。
// 类型为 []byte，由调用方 json.Marshal 后写入；nil 时落 default '[]' / '{}'。
type Invocation struct {
	ID           int64
	RequestID    string // 跨系统关联键，db 侧 gen_random_uuid() 生成（写路径不传，见 store.go copyFromBatch）
	HunterID     *string
	TaskID       *string // 所属 task.id（可空：SET NULL 外键）
	Provider     string
	Model        string
	InTokens     int
	OutTokens    int
	CachedTokens int
	LatencyMs    int
	// TTFTMs 是首 token 延迟（ms）：区分「模型思考慢」与「输出长导致总时长长」。
	// 仅流式可测（非流式一次性返回，首 token 即末 token）；0 = 未测得 / 非流式。
	TTFTMs int
	// IsStream 标记该次调用是否走流式；决定 TTFTMs 是否有意义，也决定前端如何解读延迟。
	IsStream     bool
	FinishReason string
	Error        string
	// ToolNames / TextPreview 是「这次调用产出了什么」的派生摘要，仅列表查询填充
	// （由 SQL 从 result 里算出，不把 result 大字段传出库）。审计表据此一眼看出
	// 该次调用是派活、跑命令还是写漏洞，而不必逐行点开详情。
	ToolNames   []string
	TextPreview string
	Role        string // 调用者角色：trafficAnalysis / orchestrator / exploitation / inspector 等；空 = 未分类
	Messages    []byte // jsonb：输入消息数组（[]llm.Message 序列化）
	Result      []byte // jsonb：LLM 返回（llm.Result 序列化）
	CreatedAt   time.Time
}
