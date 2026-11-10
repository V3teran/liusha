// Package llm 实现 LLM 抽象层：Router 分档路由、Provider 适配、重试与降级。
// 框架层自包含，不 import 业务配置包。
// spec.go — Router 的配置契约（框架层自包含，不 import 业务配置包）。
//
// 解耦口径：框架只认「已解析」的连接参数（ProviderSpec.APIKey 为明文密钥）；
// 密钥解密（EncryptedAPIKey/ENV 回退）与 DB schema 是业务侧（internal/llmconfig）
// 的职责，经 RouterStore 适配层注入。框架层因此不感知 crypto 与存储。
package llm

import "context"

// ProviderSpec 是构造一个 Generator 所需的全部连接参数（密钥已解析）。
type ProviderSpec struct {
	Code    string // provider 标识（日志/审计用；列名 llm_provider.code）
	Type    string // 协议类型：openai_compat | anthropic
	BaseURL string
	Model   string
	APIKey  string // 明文密钥（业务侧解析后传入；框架不落盘不记录）
	// AuthScheme 是密钥的携带方式；空 = 协议标准方式（见 DefaultAuthScheme）。
	AuthScheme AuthScheme
	// OutputLimitParam 是 OpenAI 兼容协议声明输出上限的字段；空 = max_tokens。
	// 上游明确拒绝时生成器会自适应切换（见 requestCompat）。anthropic 协议不消费。
	OutputLimitParam OutputLimitParam
	MaxTokens        int
	SupportsVision   bool
	// ThinkingBudget 是扩展思考预算（anthropic thinking.budget_tokens）。
	// >0 = 开启扩展思考（模型先流式产出内部 CoT 再产正文）；0 = 不开启。
	// openai_compat 协议当前不消费此参数。
	ThinkingBudget int64
	// ContextWindow 是模型的上下文窗口（token 数）。上下文压缩预算的基准：
	// 历史预算 = 比例 × ContextWindow；0 = 未知（消费方回退内置兜底）。
	ContextWindow int
}

// 协议类型常量（与业务侧 llmconfig 的字符串值一致——wire 契约）。
const (
	ProviderTypeOpenAICompat = "openai_compat"
	ProviderTypeAnthropic    = "anthropic"
)

// AuthScheme 是出站请求携带密钥的方式。它与协议正交：协议决定报文格式与端点路径，
// 鉴权方式只决定凭据头——同一协议的不同服务（官方、各家兼容端点、中转网关）可能
// 只认其中一种，由连接诊断实测后记录在 provider 上。
type AuthScheme string

// 鉴权方式（与业务侧 llmconfig 的字符串值一致——wire 契约）。
const (
	AuthBearer  AuthScheme = "bearer"    // Authorization: Bearer <key>（OpenAI 兼容协议的标准方式）
	AuthXAPIKey AuthScheme = "x-api-key" // x-api-key: <key>（Anthropic 协议的标准方式）
)

// DefaultAuthScheme 返回协议的标准鉴权方式。
func DefaultAuthScheme(protocol string) AuthScheme {
	if protocol == ProviderTypeAnthropic {
		return AuthXAPIKey
	}
	return AuthBearer
}

// OutputLimitParam 是 OpenAI 兼容协议声明单次输出上限所用的请求字段：多数服务认
// max_tokens，部分推理模型只认 max_completion_tokens。
type OutputLimitParam string

// 输出上限字段（与业务侧 llmconfig 的字符串值一致——wire 契约）。
const (
	OutputLimitMaxTokens           OutputLimitParam = "max_tokens"
	OutputLimitMaxCompletionTokens OutputLimitParam = "max_completion_tokens"
)

// RoutingSpec 是复杂度档位 → provider key 的路由表。
type RoutingSpec struct {
	Roles map[string]string
}

// RouterStore 是 Router 依赖的配置查询子集（业务侧适配实现）。
type RouterStore interface {
	GetRouting(ctx context.Context) (RoutingSpec, error)
	GetProviderSpec(ctx context.Context, key string) (ProviderSpec, error)
}
