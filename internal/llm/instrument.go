// instrument.go：LLM 调用计费埋点的共享类型（sink / 上下文标签 / 成本估算接口）。
//
// 旧的 Generator 装饰器实现已被 eino 路径取代（见 internal/einollm/usage_recorder.go，
// 它用 eino callbacks 在 graph 节点边界落库，逻辑等价）。此处只保留两条路径共用的抽象类型。
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
// RouteKey 写入 llm_invocation.role，取值如 "traffic-analysis" / "orchestrator" /
// "exploitation" / "inspector"，便于按角色维度统计成本和路由生效情况。
type CallMeta struct {
	HunterID  *string
	OwnerType *string // 'passive_session' / 'active_scan'
	OwnerID   *string // passive_session.id / active_scan.id
	RouteKey  string
}

// PricingProvider 抽象成本估算。
// 之所以不直接依赖 observability.Pricing：observability 已 import llm（用 Usage 类型），
// 反向引用会造成 import cycle，故定义最小本地接口。
type PricingProvider interface {
	Estimate(provider, model string, u Usage) float64
}
