// Package llm 实现 LLM 抽象层：Router 分档路由、Provider 适配、重试与降级。
// 框架层自包含，不 import 业务配置包。
// spec.go — Router 的配置契约（框架层自包含，不 import 业务配置包）。
//
// 解耦口径：框架只认「已解析」的连接参数（ProviderSpec.APIKey 为明文密钥）；
// 密钥解密（EncryptedAPIKey/ENV 回退）与 DB schema 是业务侧（internal/llmconfig）
// 的职责，经 RouterStore 适配层注入。框架层因此不感知 crypto 与存储。
package llm

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// ProviderSpec 是构造一个 Generator 所需的全部连接参数（密钥已解析）。
type ProviderSpec struct {
	Code    string // provider 标识（日志/审计用；列名 llm_provider.code）
	Type    string // 协议类型：openai_chat_completions | anthropic
	BaseURL string
	Model   string
	APIKey  string // 明文密钥（业务侧解析后传入；框架不落盘不记录）
	// AuthScheme 是密钥的携带方式；空 = 协议标准方式（见 DefaultAuthScheme）。
	AuthScheme AuthScheme
	// OutputLimitParam 是 OpenAI Chat Completions 协议声明输出上限的字段；空 = max_tokens。
	// 上游明确拒绝时生成器会自适应切换（见 requestCompat）。Anthropic Messages 协议不消费。
	OutputLimitParam OutputLimitParam
	MaxTokens        int
	SupportsVision   bool
	// ReasoningEffort 是推理模型的思考深度；空 = 没设置（不下发这个参数，按模型自己的默认）。
	ReasoningEffort ReasoningEffort
	// ContextWindow 是模型的上下文窗口（token 数）。上下文压缩预算的基准：
	// 历史预算 = 比例 × ContextWindow；0 = 未知（消费方回退内置兜底）。
	ContextWindow int
}

// 协议类型常量——取值是「厂商 + 接口名」：同一家有多种协议时只写厂商分不清。这是 wire 契约的
// 唯一出处：库表 CHECK、业务侧（llmconfig）与前端的取值都以它为准，不在别处再抄一份。
const (
	ProviderTypeOpenAIChatCompletions = "openai_chat_completions"
	ProviderTypeAnthropicMessages     = "anthropic_messages"
)

// ProviderTypes 是全部协议取值（校验与提示共用这一个出处）。
var ProviderTypes = []string{ProviderTypeOpenAIChatCompletions, ProviderTypeAnthropicMessages}

// ReasoningEffort 是推理模型的思考深度：模型在回答前投入多少推理。它是档位而不是 token 预算——
// 各家都已从「给思考分配多少 token」转向「给一个档位，由模型自己决定想多少」。按 token 的旧参数
// （budget_tokens）在新模型上已被弃用或直接拒绝，别家也从来没有过。
//
// 四档由低到高：低 / 中 / 高 / 最高。各家叫法不同，适配器按协议映射——OpenAI 两种协议与
// Anthropic、GLM 都认 low / medium / high，最高一档叫 max（个别模型叫 xhigh，没有 max）。
// 「无」不是一个档位：liusha 的三档调用都受益于思考，不思考的模型留空即可，由它自己的默认决定。
type ReasoningEffort string

const (
	ReasoningEffortLow    ReasoningEffort = "low"
	ReasoningEffortMedium ReasoningEffort = "medium"
	ReasoningEffortHigh   ReasoningEffort = "high"
	ReasoningEffortMax    ReasoningEffort = "max"
)

// ReasoningEfforts 是全部档位，由低到高（校验与界面共用这一个出处）。
var ReasoningEfforts = []ReasoningEffort{ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh, ReasoningEffortMax}

// Valid 报告 e 是不是合法取值：空（没设置）或档位表里的一个。
func (e ReasoningEffort) Valid() bool {
	return e == "" || slices.Contains(ReasoningEfforts, e)
}

// ReasoningEffortNames 是全部档位的取值名，由低到高；校验提示列出可取的值时用。
func ReasoningEffortNames() []string {
	names := make([]string, 0, len(ReasoningEfforts))
	for _, e := range ReasoningEfforts {
		names = append(names, string(e))
	}
	return names
}

// invalidErr 是取值不在档位表里时的报错（调用方冠以适配器名）。
func (e ReasoningEffort) invalidErr(adapter string) error {
	return fmt.Errorf("%s: 非法思考深度 %q（应为 %s 之一或不设置）", adapter, e, strings.Join(ReasoningEffortNames(), " / "))
}

// AuthScheme 是出站请求携带密钥的方式。它与协议正交：协议决定报文格式与端点路径，
// 鉴权方式只决定凭据头——同一协议的不同服务（官方、各家兼容端点、中转网关）可能
// 只认其中一种，由测试连接实测后记录在 provider 上。
type AuthScheme string

// 鉴权方式（wire 契约的唯一出处，业务侧经 llmconfig 取用）。
const (
	AuthBearer  AuthScheme = "bearer"    // Authorization: Bearer <key>（OpenAI 协议的标准方式）
	AuthXAPIKey AuthScheme = "x-api-key" // x-api-key: <key>（Anthropic 协议的标准方式）
)

// DefaultAuthScheme 返回协议的标准鉴权方式。
func DefaultAuthScheme(protocol string) AuthScheme {
	if protocol == ProviderTypeAnthropicMessages {
		return AuthXAPIKey
	}
	return AuthBearer
}

// OutputLimitParam 是 OpenAI Chat Completions 协议声明单次输出上限所用的请求字段：多数服务认
// max_tokens，部分推理模型只认 max_completion_tokens。
type OutputLimitParam string

// 输出上限字段（wire 契约的唯一出处，业务侧经 llmconfig 取用）。
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
	GetProviderSpec(ctx context.Context, code string) (ProviderSpec, error)
}
