// fallback.go 全局备胎 Provider 工厂：primary 重试耗尽（429/529）后由框架
// retry 切到保留 role __fallback__ 的部署。runner 与 api 共用同一装配。

package llmstore

import (
	"context"

	llmcfg "github.com/V3teran/liusha/internal/config/llm"
	fwllm "github.com/V3teran/liusha/internal/framework/llm"
)

// NewFallbackProviderFactory 构造 fwllm.Router 的兜底工厂。
// 备胎用独立 ClientPool（与 primary 池解耦，避免互持连接放大故障面），
// 且工厂自身不套 retry（框架约定：fallback 不再二次退避）。
// 备胎未配置时 For 阶段返回错误，Router 静默降级为无兜底。
func NewFallbackProviderFactory(store *Store, cipher llmcfg.KeyDecrypter) fwllm.RouterFallbackFactory {
	return func(ctx context.Context) (fwllm.Provider, error) {
		p, err := store.ProviderForFallback(ctx)
		if err != nil {
			return nil, err
		}
		key, err := llmcfg.ResolveAPIKey(p, cipher)
		if err != nil {
			return nil, err
		}
		g, err := fwllm.BuildGeneratorWithKey(ctx, fwllm.ProviderSpec{
			Code: p.Code, Type: p.Type, BaseURL: p.BaseURL, Model: p.DefaultModel,
			APIKey: key, MaxTokens: p.MaxTokens, SupportsVision: p.SupportsVision,
			ThinkingBudget: p.ThinkingBudgetTokens,
		}, fwllm.NewClientPool())
		if err != nil {
			return nil, err
		}
		return fwllm.NewProvider(g), nil
	}
}
