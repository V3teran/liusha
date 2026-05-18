package subtask

import "context"

// Spawner 是 spawn_child 工具调用的最小派单接口。
//
// 设计目的：让 internal/tools/common 包不依赖 ActiveSpawner 具体实现 +
// 测试可注入 stub。Spawner 的所有副作用（PG 落行 + goroutine 启子 react.Run +
// Registry 注册）由实现者完成；返回的 childTaskID 是 PG agent_run.id（uuid）。
//
// 错误语义：
//   - max_children 闸触发 → 返回 ErrMaxChildren（spawn_child 工具直接透传给 LLM）
//   - 任何 PG / 装配错误 → 包装后返回，工具层透传错文本
//   - context 已 cancel → 返回 ctx.Err()
type Spawner interface {
	Spawn(ctx context.Context, brief string) (childTaskID string, err error)
}
