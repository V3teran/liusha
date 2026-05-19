// Package llminvocation 实现 llm_invocation 表持久化层：每一次外部 LLM 调用
// （含失败）落一行用于成本核算 + 路由审计。
//
// 列命名约定：
//   - call_purpose 是"调用目的"（如 hunter / reviewer），与 OpenAI message.role 区分
//   - messages / result 是 jsonb 列（完整输入/输出 payload，审计回放用）
package llminvocation

import "time"

// Invocation 是 llm_invocation 表行的 Go 表示。TaskID / EngagementID 都可空（外键 SET NULL）。
//
// 双轨期：EngagementID（旧）与 OwnerType+OwnerID（新 polymorphic）并存。commit B5 之后 DROP engagement_id。
//
// Messages / Result 是完整的输入/输出 payload，用于审计与回放。
// 始终落库（不脱敏、不开关），cookie 等敏感头会原样保留。
// 类型为 []byte，由调用方 json.Marshal 后写入；nil 时落 default '[]' / '{}'。
type Invocation struct {
	ID           int64
	TaskID       *string
	EngagementID *string // 旧字段，待 DROP
	OwnerType    *string // 'passive_session' / 'active_scan'；nil = 旧路径
	OwnerID      *string // passive_session.id / active_scan.id；nil = 旧路径
	Provider     string
	Model        string
	InTokens     int
	OutTokens    int
	CachedTokens int
	CostUSD      float64 // 对应 numeric(12,6)
	LatencyMs    int
	FinishReason string
	Error        string
	CallPurpose  string // 调用目的：hunter / reviewer 等；空 = 未分类
	Messages     []byte // jsonb：输入消息数组（[]llm.Message 序列化）
	Result       []byte // jsonb：LLM 返回（llm.Result 序列化）
	CreatedAt    time.Time
}
