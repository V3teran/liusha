package audit

import "context"

// actor 上下文传播：HTTP 层认证完成后把「当前操作者」写入 request context，
// 使跨层调用（dispatch 等服务层）落审计时能如实记录操作者，而不是退化为静态标签。
//
// 放在 audit 包（而非 httpapi）是因为"操作者是谁"是审计域的概念，
// 写入方（认证中间件）与读取方（任意落审计的层级）都只依赖本包。

type actorCtxKey struct{}

// WithActor 返回携带操作者标识的 context（如 "user:admin"）。
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorCtxKey{}, actor)
}

// ActorFromContext 取出操作者标识；未携带时第二个返回值为 false。
func ActorFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(actorCtxKey{}).(string)
	return v, ok && v != ""
}
