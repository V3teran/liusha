// Package einollm 是 liusha 在 eino 上的 ChatModel 工厂（取代手写的 internal/llm provider 适配）。
//
// 设计（eino 全面迁移 P1，见 docs/superpowers/specs/2026-06-07-eino-full-migration.md）：
//   - 按 role 解析到 provider 部署（role→别名→provider，解析由 Resolver 经多级缓存运行期热读，
//     取代旧的 cfg.LLM.Agents + lookupLLMField switch；前端改「模型模块」即时生效）
//   - 从 provider 部署的 base_url/default_model/api_key_env 构造**原生** eino ChatModel
//   - 返回 eino 的 model.ToolCallingChatModel——消费方（ChatModelAgent/deep）直接用 eino 类型，不再包一层
//
// 并发性（2026-06-11 源码复核，纠正旧「per-hunter 独立 model 铁律」）：
// 共享同一个 ChatModel 给多个并发 agent 是**安全**的——ChatModelAgent 通过
// model.WithTools **call option** 配工具（adk/chatmodel.go:964），openai 组件的 WithTools
// 返回**全新** ChatModel（不改原对象，acl 层 `nc:=*c` 拷贝），并非会改内部状态的 BindTools。
// 故旧铁律「共享会被 BindTools 串行化」不成立（active 路径 deep_swarm 已共用一个 model）。
// For 每次仍返回新实例只是简单默认，不是并发要求；需要共享时直接复用返回值即可。
package einollm

import (
	"context"
	"fmt"
	"os"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/config/llmcfg"
)

// Resolver 把 role 解析到 provider 部署行（*llmstore.Store 自动满足）。
// 解析走多级缓存（L1/L2/DB），运行期热读，前端改配置即时对全进程生效。
type Resolver interface {
	ProviderForRole(ctx context.Context, role string) (llmcfg.Provider, error)
}

// Factory 按 role 产出 eino ChatModel；无状态，可全局共享一份。
// stepTimeout 来自 cfg.Runner.StepLLMTimeoutSeconds（启动期一次性读，非热改，故仍走 yaml）。
type Factory struct {
	resolver    Resolver
	stepTimeout time.Duration
}

// New 用 Resolver + config 构造 Factory。cfg 仅取 StepLLMTimeoutSeconds（单步看门狗超时）。
func New(resolver Resolver, cfg config.Config) *Factory {
	return &Factory{
		resolver:    resolver,
		stepTimeout: time.Duration(cfg.Runner.StepLLMTimeoutSeconds) * time.Second,
	}
}

// For 按 role 返回一个**新建**的 eino ChatModel（共享亦安全，见包注释并发性说明）。
func (f *Factory) For(ctx context.Context, role string) (model.ToolCallingChatModel, error) {
	p, err := f.resolver.ProviderForRole(ctx, role)
	if err != nil {
		return nil, fmt.Errorf("einollm.For(%q): 解析 provider: %w", role, err)
	}
	return f.build(ctx, p)
}

// build 从 provider 部署行构造原生 eino ChatModel。
func (f *Factory) build(ctx context.Context, p llmcfg.Provider) (model.ToolCallingChatModel, error) {
	apiKey := os.Getenv(p.APIKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("einollm: env %s 为空（provider=%s）", p.APIKeyEnv, p.Key)
	}

	switch p.Type {
	case "openai_compat", "":
		// 国产 provider（小米/DeepSeek/通义/GLM/Moonshot/豆包-Ark）全走 OpenAI 兼容协议。
		// LIUSHA_EINO_DEBUG_HTTP=1 时注入 dump transport 打请求/响应体（定位 provider 4xx）。
		cm, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
			APIKey:  apiKey,
			BaseURL: p.BaseURL,
			Model:   p.DefaultModel,
			// 单步 LLM 看门狗：超 step_llm_timeout_seconds 未返回即判定失败（配合 ModelRetryConfig 重试），
			// 取代此前的"隐式不可控 deadline"。HTTPClient 非 nil（debug dump）时此 Timeout 不生效，由 debug client 自带。
			Timeout:    f.stepTimeout,
			HTTPClient: newDebugHTTPClient(),
		})
		if err != nil {
			return nil, fmt.Errorf("einollm: 构造 openai-compat ChatModel(%s): %w", p.Key, err)
		}
		return cm, nil
	case "anthropic":
		// TODO(P1)：接 eino-ext claude adapter（或 OpenAI 兼容层）。
		// 当前 liusha 激活的是国产 openai_compat 三角色，anthropic 暂不阻塞迁移。
		return nil, fmt.Errorf("einollm: provider type=anthropic 尚未接入（P1 待办，provider=%s）", p.Key)
	default:
		return nil, fmt.Errorf("einollm: 未知 provider type=%q（provider=%s）", p.Type, p.Key)
	}
}
