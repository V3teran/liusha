// Package einollm 是 liusha 在 eino 上的 ChatModel 工厂（取代手写的 internal/llm provider 适配）。
//
// 设计（eino 全面迁移 P1，见 docs/superpowers/specs/2026-06-07-eino-full-migration.md）：
//   - 按 role 解析到 provider key（沿用 liusha config 的 Agents/Utilities + default/light/vision/fallback 语义）
//   - 从 config.Providers[key] 的 base_url/default_model/api_key_env 构造**原生** eino ChatModel
//   - 返回 eino 的 model.ToolCallingChatModel——消费方（ChatModelAgent/deep）直接用 eino 类型，不再包一层
//
// ★ per-hunter 独立实例铁律（spike/eino-deep 实测）：For 每次返回**新** ChatModel 实例。
// eino BindTools 会改 model 内部状态，commander+striker 共享一个实例会被串行化，
// 失去并发。装配多 agent 时务必每个 agent 调一次 For 拿独立实例。
package einollm

import (
	"context"
	"fmt"
	"os"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"

	"github.com/V3teran/liusha/internal/config"
)

// Factory 按 role 产出 eino ChatModel；无状态，可全局共享一份。
type Factory struct {
	cfg config.Config
}

// New 构造 Factory。
func New(cfg config.Config) *Factory { return &Factory{cfg: cfg} }

// For 按 role 返回一个**新建**的 eino ChatModel（独立实例，见包注释铁律）。
//
// role 解析：Agents[role] → Utilities[role] → default_provider，field 再映射到 provider key。
func (f *Factory) For(ctx context.Context, role string) (model.ToolCallingChatModel, error) {
	key := f.resolveProviderKey(role)
	if key == "" {
		return nil, fmt.Errorf("einollm.For(%q): default_provider 未配置", role)
	}
	return f.build(ctx, key)
}

// build 从 config.Providers[key] 构造原生 eino ChatModel。
func (f *Factory) build(ctx context.Context, providerKey string) (model.ToolCallingChatModel, error) {
	pc, ok := f.cfg.Providers[providerKey]
	if !ok {
		return nil, fmt.Errorf("einollm: provider %q 未在 config.providers 配置", providerKey)
	}
	apiKey := os.Getenv(pc.APIKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("einollm: env %s 为空（provider=%s）", pc.APIKeyEnv, providerKey)
	}

	switch pc.Type {
	case "openai_compat", "":
		// 国产 provider（小米/DeepSeek/通义/GLM/Moonshot/豆包-Ark）全走 OpenAI 兼容协议。
		// LIUSHA_EINO_DEBUG_HTTP=1 时注入 dump transport 打请求/响应体（定位 provider 4xx）。
		cm, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
			APIKey:     apiKey,
			BaseURL:    pc.BaseURL,
			Model:      pc.DefaultModel,
			HTTPClient: newDebugHTTPClient(),
		})
		if err != nil {
			return nil, fmt.Errorf("einollm: 构造 openai-compat ChatModel(%s): %w", providerKey, err)
		}
		return cm, nil
	case "anthropic":
		// TODO(P1)：接 eino-ext claude adapter（或 OpenAI 兼容层）。
		// 当前 liusha 激活的是国产 openai_compat 三角色，anthropic 暂不阻塞迁移。
		return nil, fmt.Errorf("einollm: provider type=anthropic 尚未接入（P1 待办，provider=%s）", providerKey)
	default:
		return nil, fmt.Errorf("einollm: 未知 provider type=%q（provider=%s）", pc.Type, providerKey)
	}
}

// resolveProviderKey 沿用 internal/llm 的 role→provider key 解析语义。
func (f *Factory) resolveProviderKey(role string) string {
	field, ok := f.cfg.LLM.Agents[role]
	if !ok {
		field, ok = f.cfg.LLM.Utilities[role]
	}
	if !ok {
		return f.cfg.LLM.DefaultProvider
	}
	if key := lookupLLMField(f.cfg.LLM, field); key != "" {
		return key
	}
	return f.cfg.LLM.DefaultProvider
}

func lookupLLMField(c config.LLMConfig, field string) string {
	switch field {
	case "default_provider":
		return c.DefaultProvider
	case "light_provider":
		return c.LightProvider
	case "vision_provider":
		return c.VisionProvider
	case "fallback_provider":
		return c.FallbackProvider
	}
	return ""
}
