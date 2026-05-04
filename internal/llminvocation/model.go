// Package llmcall 实现 llm_call 表持久化层：每一次外部 LLM 调用（含失败）
// 落一行用于成本核算 + 路由审计。Role 字段为黑客松借鉴的 T21 RouteKey
// （react.main / observer / lesson_extract / compaction / vision），便于按维度聚合成本。
package llminvocation

import "time"

// Invocation 是 llm_call 表行的 Go 表示。TaskID / EngagementID 都可空（外键 SET NULL）。
//
// MessagesJSON / ResultJSON 是完整的输入/输出 payload，用于审计与回放。
// 始终落库（不脱敏、不开关），cookie 等敏感头会原样保留。
// 类型为 []byte，由调用方 json.Marshal 后写入；nil 时落 default '[]' / '{}'。
type Invocation struct {
	ID           int64
	TaskID       *string
	EngagementID *string
	Provider     string
	Model        string
	InTokens     int
	OutTokens    int
	CachedTokens int
	CostUSD      float64 // 对应 numeric(12,6)
	LatencyMs    int
	FinishReason string
	Error        string
	Role         string // 黑客松借鉴：T21 RouteKey；空字符串表示未分类
	MessagesJSON []byte // jsonb：输入消息数组（[]llm.Message 序列化）
	ResultJSON   []byte // jsonb：LLM 返回（llm.Result 序列化）
	CreatedAt    time.Time
}
