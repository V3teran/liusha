package core

import (
	"context"
	"fmt"
)

// Chain 链接口（轻量级组合）
type Chain interface {
	// Run 执行链
	Run(ctx context.Context, input any) (any, error)
}

// ChainFunc 函数式 Chain
type ChainFunc func(ctx context.Context, input any) (any, error)

// Run 实现 Chain 接口
func (f ChainFunc) Run(ctx context.Context, input any) (any, error) {
	return f(ctx, input)
}

// ─────────────────────────────────────────────
//  顺序链
// ─────────────────────────────────────────────

// SequenceChain 顺序执行多个 Chain
type SequenceChain struct {
	chains []Chain
}

// NewSequenceChain 创建顺序链
func NewSequenceChain(chains ...Chain) *SequenceChain {
	return &SequenceChain{
		chains: chains,
	}
}

// Run 顺序执行所有链
func (c *SequenceChain) Run(ctx context.Context, input any) (any, error) {
	current := input

	for i, chain := range c.chains {
		result, err := chain.Run(ctx, current)
		if err != nil {
			return nil, fmt.Errorf("链 [%d] 执行失败: %w", i, err)
		}
		current = result
	}

	return current, nil
}

// ─────────────────────────────────────────────
//  转换链
// ─────────────────────────────────────────────

// TransformChain 转换链（输入 -> 转换 -> 输出）
type TransformChain struct {
	transform func(ctx context.Context, input any) (any, error)
}

// NewTransformChain 创建转换链
func NewTransformChain(fn func(ctx context.Context, input any) (any, error)) *TransformChain {
	return &TransformChain{
		transform: fn,
	}
}

// Run 执行转换
func (c *TransformChain) Run(ctx context.Context, input any) (any, error) {
	return c.transform(ctx, input)
}

// ─────────────────────────────────────────────
//  路由链
// ─────────────────────────────────────────────

// RouterChain 路由链（根据条件选择不同的链）
type RouterChain struct {
	router  func(ctx context.Context, input any) (string, error)
	routes  map[string]Chain
	default_ Chain
}

// NewRouterChain 创建路由链
func NewRouterChain(router func(ctx context.Context, input any) (string, error)) *RouterChain {
	return &RouterChain{
		router: router,
		routes: make(map[string]Chain),
	}
}

// AddRoute 添加路由
func (c *RouterChain) AddRoute(name string, chain Chain) *RouterChain {
	c.routes[name] = chain
	return c
}

// SetDefault 设置默认路由
func (c *RouterChain) SetDefault(chain Chain) *RouterChain {
	c.default_ = chain
	return c
}

// Run 执行路由
func (c *RouterChain) Run(ctx context.Context, input any) (any, error) {
	// 确定路由
	routeName, err := c.router(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("路由判断失败: %w", err)
	}

	// 查找链
	chain, ok := c.routes[routeName]
	if !ok {
		if c.default_ != nil {
			chain = c.default_
		} else {
			return nil, fmt.Errorf("路由 %q 不存在且无默认路由", routeName)
		}
	}

	return chain.Run(ctx, input)
}

// ─────────────────────────────────────────────
//  映射链
// ─────────────────────────────────────────────

// MapChain 映射链（对输入列表的每个元素执行链）
type MapChain struct {
	chain Chain
}

// NewMapChain 创建映射链
func NewMapChain(chain Chain) *MapChain {
	return &MapChain{
		chain: chain,
	}
}

// Run 对列表中每个元素执行链
func (c *MapChain) Run(ctx context.Context, input any) (any, error) {
	// 输入必须是切片
	items, ok := input.([]any)
	if !ok {
		return nil, fmt.Errorf("输入必须是 []any 类型，实际为 %T", input)
	}

	results := make([]any, len(items))

	for i, item := range items {
		result, err := c.chain.Run(ctx, item)
		if err != nil {
			return nil, fmt.Errorf("处理元素 [%d] 失败: %w", i, err)
		}
		results[i] = result
	}

	return results, nil
}

// ─────────────────────────────────────────────
//  重试链
// ─────────────────────────────────────────────

// RetryChain 重试链（失败时重试）
type RetryChain struct {
	chain      Chain
	maxRetries int
}

// NewRetryChain 创建重试链
func NewRetryChain(chain Chain, maxRetries int) *RetryChain {
	return &RetryChain{
		chain:      chain,
		maxRetries: maxRetries,
	}
}

// Run 执行（失败时重试）
func (c *RetryChain) Run(ctx context.Context, input any) (any, error) {
	var lastErr error

	for i := 0; i <= c.maxRetries; i++ {
		result, err := c.chain.Run(ctx, input)
		if err == nil {
			return result, nil
		}

		lastErr = err

		// 检查上下文是否取消
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}

	return nil, fmt.Errorf("重试 %d 次后仍失败: %w", c.maxRetries, lastErr)
}

// ─────────────────────────────────────────────
//  链构建器
// ─────────────────────────────────────────────

// ChainBuilder 链构建器
type ChainBuilder struct {
	chains []Chain
}

// NewChainBuilder 创建链构建器
func NewChainBuilder() *ChainBuilder {
	return &ChainBuilder{
		chains: make([]Chain, 0),
	}
}

// Add 添加链
func (b *ChainBuilder) Add(chain Chain) *ChainBuilder {
	b.chains = append(b.chains, chain)
	return b
}

// AddFunc 添加函数链
func (b *ChainBuilder) AddFunc(fn func(ctx context.Context, input any) (any, error)) *ChainBuilder {
	b.chains = append(b.chains, ChainFunc(fn))
	return b
}

// Build 构建顺序链
func (b *ChainBuilder) Build() Chain {
	if len(b.chains) == 0 {
		return ChainFunc(func(ctx context.Context, input any) (any, error) {
			return input, nil
		})
	}

	if len(b.chains) == 1 {
		return b.chains[0]
	}

	return NewSequenceChain(b.chains...)
}
