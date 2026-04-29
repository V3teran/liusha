// Package llmcall 实现 llm_call 表持久化层：每一次外部 LLM 调用（含失败）
// 落一行用于成本核算 + 路由审计。Role 字段为黑客松借鉴的 T21 RouteKey
// （react.main / observer / distill / compaction / vision），便于按维度聚合成本。
package llmcall

import "time"

// Call 是 llm_call 表行的 Go 表示。TaskID / EngagementID 都可空（外键 SET NULL）。
type Call struct {
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
	CreatedAt    time.Time
}
