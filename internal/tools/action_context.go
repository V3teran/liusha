package tools

import "context"

// actionCtxKey 是 action 作用域的 context 键（类型化键防字符串碰撞）。
type actionCtxKey struct{}

// WithActionContext 把当前执行的 Action ID 挂进 ctx——engine 在把 ctx 交给 ReAct
// runtime 前调用，归属边创建与监控活动追踪据此识别当前 action。
func WithActionContext(ctx context.Context, actionID string) context.Context {
	return context.WithValue(ctx, actionCtxKey{}, actionID)
}

// ActionIDFromContext 从 context 中获取当前 actionID（监控活动追踪等跨包消费者用）。
func ActionIDFromContext(ctx context.Context) string {
	if actionID, ok := ctx.Value(actionCtxKey{}).(string); ok {
		return actionID
	}
	return ""
}
