// agentctx.go 把 agent 身份放进调用链 ctx：工具拦截器据此给时间线事件
// 与遥测归属调用方（四 agent 共用一条拦截器链，归属只能来自 ctx）。

package cognition

import "context"

type agentCtxKey struct{}

// WithAgent 返回携带 agent 名的 ctx（agent 生命周期启动时注入，随 Run 全链透传）。
func WithAgent(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, agentCtxKey{}, name)
}

// AgentFromCtx 取 agent 名（未注入返回空串，事件按 assistant 归档兜底）。
func AgentFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(agentCtxKey{}).(string); ok {
		return v
	}
	return ""
}
