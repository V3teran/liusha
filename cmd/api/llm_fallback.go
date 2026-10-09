package main

import (
	"context"

	llmcfg "github.com/V3teran/liusha/internal/config/llm"
	fwllm "github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/llmstore"
)

// newFallbackProviderFactory 构造全局备胎 Provider 工厂（与 runner 同构）。
// primary 重试耗尽（429/529）后由框架 retry 切到保留 role __fallback__ 的部署。
// 备胎用独立 ClientPool（与 primary 池解耦），且工厂自身不套 retry（框架约定：fallback 不再二次退避）。
func newFallbackProviderFactory(store *llmstore.Store, cipher llmcfg.KeyDecrypter) fwllm.RouterFallbackFactory {
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
			Code:           p.Code,
			Type:           p.Type,
			BaseURL:        p.BaseURL,
			Model:          p.DefaultModel,
			APIKey:         key,
			MaxTokens:      p.MaxTokens,
			SupportsVision: p.SupportsVision,
		}, fwllm.NewClientPool())
		if err != nil {
			return nil, err
		}
		return fwllm.NewProvider(g), nil
	}
}
