// Package provider 实现 LLM 复杂度路由。
//
// 按业务复杂度（simple / medium / complex）查询配置，
// 构造对应 Provider 并套上 retry 装饰器。
//
// 三个复杂度档位对应不同的推理能力需求：
//   - simple  → 快速响应，简单任务（如信息提取、格式化）
//   - medium  → 标准推理，常规任务（如漏洞检测、工具调用）
//   - complex → 深度推理，复杂决策（如战略规划、多步分析）
package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/V3teran/liusha/internal/config/llm"
	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	openaisdk "github.com/sashabaranov/go-openai"
)

// Complexity 是 LLM 复杂度分级标识。
type Complexity string

const (
	ComplexitySimple  Complexity = "simple"  // 快速响应：信息提取、格式化、简单验证
	ComplexityMedium  Complexity = "medium"  // 标准推理：漏洞检测、工具调用、常规分析
	ComplexityComplex Complexity = "complex" // 深度推理：战略规划、多步决策、复杂综合
)

// RouterStore 是 Router 依赖的 llmcfg 查询子集。
type RouterStore interface {
	GetRouting(ctx context.Context) (llmcfg.Routing, error)
	GetProvider(ctx context.Context, key string) (llmcfg.Provider, error)
}

// KeyDecrypter 解密 EncryptedAPIKey，由调用方注入（通常是 *cryptx.Cipher）。
type KeyDecrypter interface {
	Decrypt(sealed []byte) (string, error)
}

// Router 按 Complexity 路由 Provider，内部缓存已构造实例（线程安全）。
type Router struct {
	store     RouterStore
	decrypter KeyDecrypter
	mu        sync.Mutex
	cached    map[Complexity]Provider
}

// NewRouter 构造 Router。
func NewRouter(store RouterStore, dec KeyDecrypter) *Router {
	return &Router{store: store, decrypter: dec, cached: make(map[Complexity]Provider)}
}

// For 返回指定 Complexity 的 Provider（首次构造后缓存）。
// 配置变更后可调用 Invalidate 清缓存。
func (r *Router) For(ctx context.Context, complexity Complexity) (Provider, error) {
	r.mu.Lock()
	if p, ok := r.cached[complexity]; ok {
		r.mu.Unlock()
		return p, nil
	}
	r.mu.Unlock()

	p, err := r.build(ctx, complexity)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.cached[complexity] = p
	r.mu.Unlock()
	return p, nil
}

// Invalidate 清除指定 complexity 的缓存（配置热更新时调用）。
func (r *Router) Invalidate(complexity Complexity) {
	r.mu.Lock()
	delete(r.cached, complexity)
	r.mu.Unlock()
}

// InvalidateAll 清除所有缓存。
func (r *Router) InvalidateAll() {
	r.mu.Lock()
	r.cached = make(map[Complexity]Provider)
	r.mu.Unlock()
}

func (r *Router) build(ctx context.Context, complexity Complexity) (Provider, error) {
	routing, err := r.store.GetRouting(ctx)
	if err != nil {
		return nil, fmt.Errorf("provider/router: load routing: %w", err)
	}
	providerKey, ok := routing.Roles[string(complexity)]
	if !ok {
		return nil, fmt.Errorf("provider/router: no provider configured for complexity %q", complexity)
	}

	provCfg, err := r.store.GetProvider(ctx, providerKey)
	if err != nil {
		return nil, fmt.Errorf("provider/router: load provider %q: %w", providerKey, err)
	}

	apiKey, err := llmcfg.ResolveAPIKey(provCfg, r.decrypter)
	if err != nil {
		return nil, fmt.Errorf("provider/router: resolve api key for %q: %w", providerKey, err)
	}

	model := provCfg.DefaultModel

	var p Provider
	switch provCfg.Type {
	case llmcfg.ProviderTypeAnthropic:
		opts := []anthropicoption.RequestOption{anthropicoption.WithAPIKey(apiKey)}
		if provCfg.BaseURL != "" {
			opts = append(opts, anthropicoption.WithBaseURL(provCfg.BaseURL))
		}
		client := anthropicsdk.NewClient(opts...)
		p, err = NewAnthropic(&client, model, provCfg.MaxTokens)
	case llmcfg.ProviderTypeOpenAICompat:
		ocfg := openaisdk.DefaultConfig(apiKey)
		if provCfg.BaseURL != "" {
			ocfg.BaseURL = provCfg.BaseURL
		}
		client := openaisdk.NewClientWithConfig(ocfg)
		p, err = NewOpenAI(client, model)
	default:
		return nil, fmt.Errorf("provider/router: unknown provider type %q", provCfg.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("provider/router: build provider %q: %w", provCfg.Type, err)
	}
	if p == nil {
		return nil, errors.New("provider/router: built nil provider")
	}

	return WithRetry(p, DefaultRetryConfig()), nil
}
