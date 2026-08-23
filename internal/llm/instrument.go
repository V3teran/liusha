// instrument.go：LLM 调用埋点的共享类型（sink / 上下文标签）。
//
// LLM 调用计费装饰器入口，由 provider.Router 注入 UsageRecorder 完成（
// 它在调用边界落库，逻辑等价）。此处只保留两条路径共用的抽象类型。
package llm

import (
	"context"

	"github.com/V3teran/liusha/internal/llminvocation"
)

// CallSink 抽象 llm_invocation 持久化层，便于测试注入 mock。
// 实参为 *llminvocation.Store（其 Append 方法签名一致）。
type CallSink interface {
	Append(ctx context.Context, c llminvocation.Invocation) (int64, error)
}

// CallMeta 是单次 Generate 的上下文标签集，由 runtime 填充。
//
// RouteKey 写入 llm_invocation.role，取值如 "traffic-analysis" / "planner" /
// "exploitation" / "inspector"，便于按角色维度统计 token 用量和路由生效情况。
type CallMeta struct {
	ExecutorID *string
	TaskID   *string // 所属 task.id
	RouteKey string
}
