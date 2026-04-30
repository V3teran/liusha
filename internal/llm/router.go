// router.go：基于 Factory 的多 provider 路由层 + retry/fallback 装配。
//
// 设计要点（黑客松借鉴创新 11 + 共识 E）：
//   - Router 只装饰 Factory.For，不重新解析路由：Factory 已实现 routes/role 解析。
//   - 每次 For 都把 fallback_provider 一次性构造好（不再套 retry，避免双重重试）。
//   - opts 默认走 spec §8.5 退避表，可通过 NewRouterWithOptions 注入测试用零延迟版本。
//   - Router 不持有 ctx：每次 For 由调用方传入；同 ctx/role 多次调用共享 Factory 内的 Generator 缓存。
package llm

import (
	"context"
)

// Router 是按 role 路由 + retry/fallback 装配的入口。
// runtime 的所有 LLM 调用都应走 Router，而非直接拿 Factory。
type Router struct {
	factory *Factory
	opts    RetryOptions
}

// NewRouter 用 Factory + 默认 RetryOptions（spec §8.5）构造。
func NewRouter(factory *Factory) *Router {
	return NewRouterWithOptions(factory, DefaultRetryOptions())
}

// NewRouterWithOptions 注入自定义 RetryOptions（测试用零延迟版本）。
func NewRouterWithOptions(factory *Factory, opts RetryOptions) *Router {
	return &Router{factory: factory, opts: opts}
}

// For 解析 role → primary Generator（每次新建无状态实例），同时构造 fallback Generator
// （fallback_provider 字段配置；为空则不套 fallback），最后用 WithRetry 包成最终 Generator。
//
// 注意：tools 不在此处绑定。调用方在 g.Generate(ctx, msgs, tools) 时动态传入，
// 修复 v1 Factory 缓存 Generator 导致跨 task tools 错乱的并发 bug。
//
// fallback 解析规则：
//   - 取 cfg.LLM.FallbackProvider 字段值（provider key，如 "qwen"）
//   - 通过 Factory 按 provider key 直接构造（与 primary 共享底层 ClientPool 内的 HTTP client）
//   - fallback 为空 string 时不传 fallback（WithRetry 收 nil 后耗尽即抛）
//   - fallback 实例本身不套 retry：避免循环重试 / 双层 backoff
func (r *Router) For(ctx context.Context, role string) (Generator, error) {
	primary, err := r.factory.For(ctx, role)
	if err != nil {
		return nil, err
	}

	var fallback Generator
	if fbKey := r.factory.cfg.LLM.FallbackProvider; fbKey != "" {
		fb, fbErr := r.factory.forProviderKey(ctx, fbKey)
		if fbErr == nil {
			fallback = fb
		}
		// fallback 构造失败时静默降级：primary retry 耗尽后直接抛错（与无 fallback 等价）。
	}
	return WithRetry(primary, fallback, r.opts), nil
}
