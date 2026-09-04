// Package registry — 工具注册中心 + Interceptor 链 + 并发执行。
package registry

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/provider"
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

// LoggingInterceptor 记录工具调用开始/结束（slog，不打 args）。
func LoggingInterceptor(ctx context.Context, tool Tool, args []byte, next ExecuteFunc) (ToolResult, error) {
	start := time.Now()
	res, err := next(ctx, tool, args)
	slog.DebugContext(ctx, "tool.execute",
		"tool", tool.Name(),
		"elapsed_ms", time.Since(start).Milliseconds(),
		"has_signal", res.Signal != nil,
		"has_error", res.Error != "",
	)
	return res, err
}

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

// TimeoutInterceptor 从 context 值 ctxKeyTimeout 读超时，不存在时使用 defaultToolTimeout。
const defaultToolTimeout = 120 * time.Second

type ctxKey int

const ctxKeyTimeout ctxKey = 1

// WithToolTimeout 向 ctx 注入工具调用超时。
func WithToolTimeout(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, ctxKeyTimeout, d)
}

func TimeoutInterceptor(ctx context.Context, tool Tool, args []byte, next ExecuteFunc) (ToolResult, error) {
	d := defaultToolTimeout
	if v, ok := ctx.Value(ctxKeyTimeout).(time.Duration); ok && v > 0 {
		d = v
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

// PreExecuteInterceptor 检查 context 中注入的 Constraint 列表。
// 违规时直接返回拒绝结果，不调用 next。
func PreExecuteInterceptor(ctx context.Context, tool Tool, args []byte, next ExecuteFunc) (ToolResult, error) {
	constraints, _ := ctx.Value(ctxKeyConstraints).([]Constraint)
	for _, c := range constraints {
		if reason := checkConstraint(c, tool, args); reason != "" {
			return ToolResult{Error: fmt.Sprintf("constraint %s violated: %s", c.Kind, reason)}, nil
		}
	}
	return next(ctx, tool, args)
}

const ctxKeyConstraints ctxKey = 2

// WithConstraints 向 ctx 注入待检查的 Constraint 列表。
func WithConstraints(ctx context.Context, cs []Constraint) context.Context {
	return context.WithValue(ctx, ctxKeyConstraints, cs)
}

func checkConstraint(c Constraint, tool Tool, _ []byte) string {
	switch c.Kind {
	case ConstraintPassiveOnly:
		// 被动扫描只允许 read/list 类工具
		if tool.Name() != "run_command" {
			return ""
		}
		return "passive_only: run_command 禁用"
	case ConstraintNoDestructive:
		return "" // 由工具自身 Schema description 声明，此处不做硬拦截
	default:
		return ""
	}
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
// 链顺序（架构规格 §8）：
//
//	PreExecute → Logging → EvidenceCapture → Timeout → ErrorMask → Tool.Execute
func New() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
		interceptors: []Interceptor{
			PreExecuteInterceptor,
			LoggingInterceptor,
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
// 并发不安全，须在工具注册完成后、首次 ExecuteParallel 前调用。
func (r *Registry) AddInterceptor(i Interceptor) {
	// 插入到 ErrorMask 之前，保证 ErrorMask 始终是链的最后一道
	last := len(r.interceptors) - 1
	if last >= 0 {
		r.interceptors = append(r.interceptors[:last], append([]Interceptor{i}, r.interceptors[last:]...)...)
	} else {
		r.interceptors = append(r.interceptors, i)
	}
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
func (r *Registry) Schemas() []provider.ToolSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]provider.ToolSchema, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, provider.ToolSchema{
			Name:        t.Name(),
			Description: t.Desc(),
			Parameters:  t.Schema(),
		})
	}
	return out
}

// execute 通过 Interceptor 链执行单个工具调用。
func (r *Registry) execute(ctx context.Context, call provider.ToolCall) ToolResult {
	t, ok := r.Get(call.Name)
	if !ok {
		return ToolResult{Error: fmt.Sprintf("tool %q not registered", call.Name)}
	}
	final := ExecuteFunc(func(ctx context.Context, tool Tool, args []byte) (ToolResult, error) {
		return tool.Execute(ctx, args)
	})
	res, err := chain(r.interceptors, final)(ctx, t, call.Arguments)
	if err != nil {
		// 到这里说明是 context cancel，直接标记错误
		return ToolResult{Error: err.Error()}
	}
	return res
}

// ExecuteParallel 并发执行一批 tool_call，结果按原始顺序收集。
// LLM 单次返回多个 tool_call 时使用。
func (r *Registry) ExecuteParallel(ctx context.Context, calls []provider.ToolCall) []ToolResult {
	if len(calls) == 0 {
		return nil
	}
	results := make([]ToolResult, len(calls))
	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Add(1)
		go func(idx int, c provider.ToolCall) {
			defer wg.Done()
			results[idx] = r.execute(ctx, c)
		}(i, call)
	}
	wg.Wait()
	return results
}
