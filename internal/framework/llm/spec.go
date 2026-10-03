// spec.go — Router 的配置契约（框架层自包含，不 import 业务配置包）。
//
// 解耦口径：框架只认「已解析」的连接参数（ProviderSpec.APIKey 为明文密钥）；
// 密钥解密（EncryptedAPIKey/ENV 回退）与 DB schema 是业务侧（internal/config/llm）
// 的职责，经 RouterStore 适配层注入。框架层因此不感知 crypto 与存储。
package llm

import "context"

// ProviderSpec 是构造一个 Generator 所需的全部连接参数（密钥已解析）。
type ProviderSpec struct {
	Key      string // provider 标识（日志/审计用）
	Type     string // 协议类型：openai_compat | anthropic
	BaseURL  string
	Model    string
	APIKey   string // 明文密钥（业务侧解析后传入；框架不落盘不记录）
	MaxTokens int
	SupportsVision bool
}

// 协议类型常量（与业务侧 config/llm 的字符串值一致——wire 契约）。
const (
	ProviderTypeOpenAICompat = "openai_compat"
	ProviderTypeAnthropic    = "anthropic"
)

// RoutingSpec 是复杂度档位（含 __fallback__）→ provider key 的路由表。
type RoutingSpec struct {
	Roles map[string]string
}

// RouterStore 是 Router 依赖的配置查询子集（业务侧适配实现）。
type RouterStore interface {
	GetRouting(ctx context.Context) (RoutingSpec, error)
	GetProviderSpec(ctx context.Context, key string) (ProviderSpec, error)
}
