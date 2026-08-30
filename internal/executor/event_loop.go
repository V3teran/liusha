package executor

import (
	"context"
)

// eventLoop 监听外部事件（Planner 的控制指令）。
func (a *Executor) eventLoop(ctx context.Context, cancel context.CancelFunc, subscription EventSubscription, state *executionState) {
	for {
		select {
		case event := <-subscription.Events():
			a.handleEvent(ctx, cancel, event, state)

		case <-ctx.Done():
			return
		}
	}
}

// handleEvent 处理事件。
func (a *Executor) handleEvent(ctx context.Context, cancel context.CancelFunc, event Event, state *executionState) {
	switch event.Type {
	case "action.killed":
		// Kill：立即停止执行
		state.stop(true)
		cancel()

	case "action.steered":
		// Steer：注入纠偏消息
		if guidance, ok := event.Payload["guidance"].(string); ok {
			select {
			case state.correctionChan <- guidance:
				// 成功注入
			default:
				// 通道满，丢弃（防止阻塞）
			}
		}

	case "action.completed":
		// 可选：外部通知完成（暂时不处理）

	default:
		// 未知事件类型，忽略
	}
}
