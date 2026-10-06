// Package registry — 工具注册中心 + Interceptor 链 + 并发执行。
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// ─────────────────────────────────────────────
//  Interceptor
// ─────────────────────────────────────────────

// ExecuteFunc 是 Interceptor 链末端的原始执行函数签名。
type ExecuteFunc func(ctx context.Context, tool Tool, args []byte) (ToolResult, error)

// Interceptor 是工具调用的拦截器（中间件）。
// next 是链中的下一个处理器（最终调用 Tool.Execute）。
type Interceptor func(ctx context.Context, tool Tool, args []byte, next ExecuteFunc) (ToolResult, error)

// chain 将多个 Interceptor 组合成一条调用链。
func chain(interceptors []Interceptor, final ExecuteFunc) ExecuteFunc {
	if len(interceptors) == 0 {
		return final
	}
	return func(ctx context.Context, tool Tool, args []byte) (ToolResult, error) {
		return interceptors[0](ctx, tool, args, chain(interceptors[1:], final))
	}
}

// ─────────────────────────────────────────────
//  内置 Interceptor 实现
// ─────────────────────────────────────────────

// EvidenceCaptureInterceptor 将 cmd_output 类工具产出自动包装为 Signal。
// 仅在 ToolResult.Signal 为 nil 且 Output 非空时补充。
func EvidenceCaptureInterceptor(ctx context.Context, tool Tool, args []byte, next ExecuteFunc) (ToolResult, error) {
	res, err := next(ctx, tool, args)
	if err != nil {
		return res, err
	}
	if res.Signal == nil && res.Output != "" {
		content := res.Output
		truncated := false
		if len(content) > 4096 {
			content = content[:4096]
			truncated = true
		}
		res.Signal = &Signal{
			Kind:       SignalCmdOutput,
			ToolName:   tool.Name(),
			Content:    content,
			Detail:     res.Output,
			Truncated:  truncated,
			CapturedAt: time.Now(),
		}
	}
	return res, err
}

// defaultToolTimeout 是工具未声明 Timeout 时的兜底超时。
const defaultToolTimeout = 120 * time.Second

// TimeoutInterceptor 以工具声明的 Timeout（0=默认 120s）包装工具执行，
// 让 Tool.Timeout() 的声明真正生效。
func TimeoutInterceptor(ctx context.Context, tool Tool, args []byte, next ExecuteFunc) (ToolResult, error) {
	d := tool.Timeout()
	if d <= 0 {
		d = defaultToolTimeout
	}
	tctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	return next(tctx, tool, args)
}

// ErrorMaskInterceptor 将非 context cancel / deadline 的 Go error 转成
// ToolResult.Error 字符串，保证 ReAct 循环不中断。
func ErrorMaskInterceptor(ctx context.Context, tool Tool, args []byte, next ExecuteFunc) (ToolResult, error) {
	res, err := next(ctx, tool, args)
	if err == nil {
		return res, nil
	}
	if ctx.Err() != nil {
		return ToolResult{}, err // context 取消直接上抛，中断 ReAct
	}
	return ToolResult{Output: res.Output, Signal: res.Signal, Error: err.Error()}, nil
}

// ─────────────────────────────────────────────
//  Registry
// ─────────────────────────────────────────────

// Registry 管理工具集并实现带 Interceptor 链的并发执行。
// 并发安全：Register/Get/ExecuteParallel 可被多 goroutine 同时调用。
type Registry struct {
	mu           sync.RWMutex
	tools        map[string]Tool
	interceptors []Interceptor
}

// New 返回带默认 Interceptor 链的 Registry。
// 链顺序：EvidenceCapture → Timeout → ErrorMask → Tool.Execute。
// 按需拦截器（工具调用录制/遥测/心跳）经 AddInterceptor 注入，插在 ErrorMask 之前。
func New() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
		interceptors: []Interceptor{
			EvidenceCaptureInterceptor,
			TimeoutInterceptor,
			ErrorMaskInterceptor,
		},
	}
}

// Register 注册一个工具。重名覆盖。
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	r.tools[t.Name()] = t
	r.mu.Unlock()
}

// AddInterceptor 在 Interceptor 链末尾（ErrorMask 之前）追加一个拦截器。
// 适用于在 Registry 构造后注入按需拦截器（如工具调用录制）。
func (r *Registry) AddInterceptor(i Interceptor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// 插入到 ErrorMask 之前，保证 ErrorMask 始终是链的最后一道
	last := len(r.interceptors) - 1
	if last >= 0 {
		r.interceptors = append(r.interceptors[:last], append([]Interceptor{i}, r.interceptors[last:]...)...)
	} else {
		r.interceptors = append(r.interceptors, i)
	}
}

// interceptorChain 返回当前拦截器链副本（execute/WrapTool 与 AddInterceptor 并发安全）。
func (r *Registry) interceptorChain() []Interceptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.interceptors
}

// Interceptors 返回当前注册的所有拦截器的副本。
// 用于创建 sub-registry 时复制拦截器链。
func (r *Registry) Interceptors() []Interceptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	// 返回副本，避免外部修改
	interceptors := make([]Interceptor, len(r.interceptors))
	copy(interceptors, r.interceptors)
	return interceptors
}

// Get 按名称查找工具。
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	t, ok := r.tools[name]
	r.mu.RUnlock()
	return t, ok
}

// Schemas 返回所有已注册工具的 ToolSchema，供 Provider.Complete/Stream 传入。
func (r *Registry) Schemas() []llm.ToolSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]llm.ToolSchema, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, llm.ToolSchema{
			Name:        t.Name(),
			Description: t.Desc(),
			Parameters:  t.Schema(),
		})
	}
	return out
}

// execute 通过 Interceptor 链执行单个工具调用。
func (r *Registry) execute(ctx context.Context, call llm.ToolCall) ToolResult {
	t, ok := r.Get(call.Name)
	if !ok {
		return ToolResult{Error: fmt.Sprintf("tool %q not registered", call.Name)}
	}
	final := ExecuteFunc(func(ctx context.Context, tool Tool, args []byte) (ToolResult, error) {
		return tool.Execute(ctx, args)
	})
	res, err := chain(r.interceptorChain(), final)(ctx, t, call.Arguments)
	if err != nil {
		// 到这里说明是 context cancel，直接标记错误
		return ToolResult{Error: err.Error()}
	}
	return res
}

// WrapTool 返回一个 Execute 走完整 Interceptor 链的装饰工具。
//
// 背景：ReAct runtime 直调 tool.Execute（不经 Registry.execute），拦截器链
// （PreExecute/Logging/EvidenceCapture/Timeout/ErrorMask + AddInterceptor 注入的
// 遥测/心跳）会被整体绕过。调用方在把工具装进 runtime 前先用本方法包一层，
// 即可让框架外的执行路径也走统一链。
func (r *Registry) WrapTool(t Tool) Tool {
	return &wrappedTool{inner: t, reg: r}
}

type wrappedTool struct {
	inner Tool
	reg   *Registry
}

func (w *wrappedTool) Name() string            { return w.inner.Name() }
func (w *wrappedTool) ShortDesc() string       { return w.inner.ShortDesc() }
func (w *wrappedTool) Desc() string            { return w.inner.Desc() }
func (w *wrappedTool) Schema() json.RawMessage { return w.inner.Schema() }
func (w *wrappedTool) Timeout() time.Duration  { return w.inner.Timeout() }
func (w *wrappedTool) ConcurrencySafe() bool   { return w.inner.ConcurrencySafe() }
func (w *wrappedTool) Execute(ctx context.Context, args json.RawMessage) (ToolResult, error) {
	final := ExecuteFunc(func(_ context.Context, tool Tool, a []byte) (ToolResult, error) {
		return tool.Execute(ctx, a)
	})
	return chain(w.reg.interceptorChain(), final)(ctx, w.inner, args)
}

// ExecuteParallel 并发执行一批 tool_call，结果按原始顺序收集。
// LLM 单次返回多个 tool_call 时使用。
func (r *Registry) ExecuteParallel(ctx context.Context, calls []llm.ToolCall) []ToolResult {
	if len(calls) == 0 {
		return nil
	}
	results := make([]ToolResult, len(calls))
	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Add(1)
		go func(idx int, c llm.ToolCall) {
			defer wg.Done()
			results[idx] = r.execute(ctx, c)
		}(i, call)
	}
	wg.Wait()
	return results
}
