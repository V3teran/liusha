package subtask

import "context"

// SpawnOptions 是 Spawn 的可选参数集合（避免接口签名膨胀）。
//
// FlowID 可选——父 passive 任务传自己的 flow_id（来自 BuilderParams.FlowID），
// ActiveSpawner 拉对应 flow 后填入子 BuilderParams.RequestHeaders/Body/Response*，
// 让子 user prompt 渲染完整 raw HTTP 段 + brief 段，信息密度最高。
// 0 = 不传，子 user prompt 仅渲染 brief。
type SpawnOptions struct {
	FlowID int64
}

// Spawner 是 spawn_striker 工具调用的最小派单接口。
//
// 设计目的：让 internal/tools/common 包不依赖 ActiveSpawner 具体实现 +
// 测试可注入 stub。Spawner 的所有副作用（PG 落行 + goroutine 启子 react.Run +
// Registry 注册）由实现者完成；返回的 childTaskID 是 PG agent_run.id（uuid）。
//
// 错误语义：
//   - max_children 闸触发 → 返回 ErrMaxChildren（spawn_striker 工具直接透传给 LLM）
//   - 任何 PG / 装配错误 → 包装后返回，工具层透传错文本
//   - context 已 cancel → 返回 ctx.Err()
type Spawner interface {
	Spawn(ctx context.Context, brief string, opts SpawnOptions) (childTaskID string, err error)
}
