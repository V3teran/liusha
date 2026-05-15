// Package toolfx 定义 ReAct 循环里的"动作"（工具）抽象与注册表。
//
// 设计要点：
//   - Action 是 LLM 可调用的工具单元，Execute 返回 Result（含 Done 终止信号 + Summary 摘要）。
//   - Registry 在动作执行前后通过 Interceptor 链横切：Observe / Timeout（详见
//     internal/toolruntime/interceptor 包）。命名向 grpc-go 的 UnaryInterceptor 对齐，
//     与 internal/httpapi 的 gin middleware 区分开——本包是"RPC 风格的方法拦截"，
//     不是"HTTP 请求拦截"。
//   - Interceptor 顺序：先注册的在最外层（先 enter、后 exit），洋葱模型。
package toolfx

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/V3teran/liusha/internal/llm"
)

// Result 是动作执行的结果。
//
// Output 是 JSON 编码的工具返回值，会作为 tool message 喂回 LLM。
// Done = true 时 runtime 应终止 ReAct 循环（如 submit_finding 提交完成）。
// Summary 是 ≤200 字的摘要，供 Reviewer 滑动窗用于压缩历史。
type Result struct {
	Output  json.RawMessage
	Done    bool
	Summary string
}

// Action 是 ReAct 循环里可被 LLM 调用的工具。
type Action interface {
	Name() string
	Description() string
	ParametersJSON() json.RawMessage
	Execute(ctx context.Context, args json.RawMessage) (Result, error)
}

// ActionExecutor 是去掉 Action 实例后的执行函数签名，用于 Interceptor 链。
type ActionExecutor func(ctx context.Context, name string, args json.RawMessage) (Result, error)

// Interceptor 是一层装饰：包住 next 返回新的 Executor。命名向 grpc-go 的
// UnaryServerInterceptor 对齐，本包是 RPC 风格方法拦截，区别于 HTTP middleware。
type Interceptor func(next ActionExecutor) ActionExecutor

// Registry 持有已注册的 Action 与 Interceptor 链，是 ReAct runtime 唯一的动作入口。
type Registry struct {
	lock         sync.RWMutex
	actions      map[string]Action
	interceptors []Interceptor
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{actions: make(map[string]Action)}
}

// Register 注册一个动作；重名报错（防止误覆盖）。
func (r *Registry) Register(a Action) error {
	r.lock.Lock()
	defer r.lock.Unlock()
	name := a.Name()
	if _, ok := r.actions[name]; ok {
		return fmt.Errorf("action %q 已注册", name)
	}
	r.actions[name] = a
	return nil
}

// Use 追加 Interceptor；先注册的在最外层（洋葱模型）。
func (r *Registry) Use(itc ...Interceptor) {
	if len(itc) == 0 {
		return
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	r.interceptors = append(r.interceptors, itc...)
}

// Schemas 返回所有已注册动作的 ToolSchema，喂给 LLM 用。
func (r *Registry) Schemas() []llm.ToolSchema {
	r.lock.RLock()
	defer r.lock.RUnlock()
	out := make([]llm.ToolSchema, 0, len(r.actions))
	for _, a := range r.actions {
		out = append(out, llm.ToolSchema{
			Name:        a.Name(),
			Description: a.Description(),
			Parameters:  a.ParametersJSON(),
		})
	}
	return out
}

// Execute 经过 Interceptor 链调用指定动作。
//
// 链构造：base → itc[n-1] → ... → itc[0]，最先 Use 的在最外层。
func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) (Result, error) {
	// 基础 executor：从 actions map 找到目标并执行。
	base := func(ctx context.Context, name string, args json.RawMessage) (Result, error) {
		r.lock.RLock()
		a, ok := r.actions[name]
		r.lock.RUnlock()
		if !ok {
			return Result{}, fmt.Errorf("action 未注册: %s", name)
		}
		return a.Execute(ctx, args)
	}

	// 快照 Interceptor 切片，避免 Execute 进行时被 Use 改写。
	r.lock.RLock()
	itc := make([]Interceptor, len(r.interceptors))
	copy(itc, r.interceptors)
	r.lock.RUnlock()

	wrapped := base
	for i := len(itc) - 1; i >= 0; i-- {
		wrapped = itc[i](wrapped)
	}
	return wrapped(ctx, name, args)
}
