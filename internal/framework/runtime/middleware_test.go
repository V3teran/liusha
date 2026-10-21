package runtime

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/framework/llm"
)

func TestLoopDetectionMiddleware_ToolLoop(t *testing.T) {
	m := NewLoopDetectionMiddleware(5, 5)

	// 模拟连续 5 次调用同一工具
	state := &IterationState{
		Iteration:       10,
		RecentToolNames: []string{"tool_a", "tool_a", "tool_a", "tool_a", "tool_a"},
	}

	signal, err := m.AfterIteration(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if signal == nil || !signal.ShouldStop {
		t.Error("expected stop signal for tool loop")
	}

	if signal.Reason != StopReasonToolLoop {
		t.Errorf("expected reason %s, got %s", StopReasonToolLoop, signal.Reason)
	}
}

func TestLoopDetectionMiddleware_NoLoop(t *testing.T) {
	m := NewLoopDetectionMiddleware(5, 5)

	// 工具调用有变化，不应触发
	state := &IterationState{
		Iteration:       10,
		RecentToolNames: []string{"tool_a", "tool_b", "tool_a", "tool_c", "tool_a"},
	}

	signal, err := m.AfterIteration(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if signal != nil && signal.ShouldStop {
		t.Error("should not stop for varied tool calls")
	}
}

func TestProgressTrackingMiddleware_OutputStagnationOnly(t *testing.T) {
	// 连续失败止损已收敛为 runtime 内置单机制（迭代中即时生效、阈值随装配
	// 下发）——中间件只承担无新输出检测；连续失败计数不再触发中间件停止。
	m := NewProgressTrackingMiddleware(15)

	state := &IterationState{
		Iteration:               10,
		ConsecutiveToolFailures: 8,
		OutputCount:             5,
	}
	signal, err := m.AfterIteration(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if signal != nil && signal.ShouldStop {
		t.Error("连续失败分支已移除（runtime 内置单机制），中间件不应因失败计数停止")
	}
}

func TestProgressTrackingMiddleware_NoOutputProgress(t *testing.T) {
	m := NewProgressTrackingMiddleware(5)

	ctx := context.Background()

	// 第一次调用，设置初始 OutputCount
	state := &IterationState{
		Iteration:               1,
		ConsecutiveToolFailures: 0,
		OutputCount:             3,
	}
	_, _ = m.AfterIteration(ctx, state)

	// 模拟连续 5 次迭代无新输出
	for i := 2; i <= 6; i++ {
		state := &IterationState{
			Iteration:               i,
			ConsecutiveToolFailures: 0,
			OutputCount:             3, // 输出数量不变
		}

		signal, err := m.AfterIteration(ctx, state)
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}

		if i < 6 {
			// 前 4 次（迭代 2-5）不应停止
			if signal != nil && signal.ShouldStop {
				t.Errorf("iteration %d: should not stop yet", i)
			}
		} else {
			// 第 6 次（无新输出已经 5 次）应该停止
			if signal == nil || !signal.ShouldStop {
				t.Error("iteration 6: expected stop signal for no output")
			}
		}
	}
}

func TestProgressTrackingMiddleware_WithNewOutput(t *testing.T) {
	m := NewProgressTrackingMiddleware(5)

	ctx := context.Background()

	// 前 3 次无新输出
	for i := 1; i <= 3; i++ {
		state := &IterationState{
			Iteration:   i,
			OutputCount: 3,
		}
		_, _ = m.AfterIteration(ctx, state)
	}

	// 第 4 次有新输出，应该重置计数器
	state := &IterationState{
		Iteration:   4,
		OutputCount: 4, // 新输出
	}
	signal, err := m.AfterIteration(ctx, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if signal != nil && signal.ShouldStop {
		t.Error("should not stop after new output")
	}

	// 再过 4 次无输出，不应停止（因为重置了）
	for i := 5; i <= 8; i++ {
		state := &IterationState{
			Iteration:   i,
			OutputCount: 4, // 保持不变
		}
		signal, err := m.AfterIteration(ctx, state)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if signal != nil && signal.ShouldStop {
			t.Errorf("iteration %d: should not stop yet (reset)", i)
		}
	}
}

func TestGoalAlignmentMiddleware_Aligned(t *testing.T) {
	m := NewGoalAlignmentMiddleware("测试 DVWA 的 XSS 漏洞", 5)

	state := &IterationState{
		Iteration: 5,
		Thought:   "现在访问 DVWA 的反射型 XSS 页面进行测试",
		ToolCalls: []*Action{
			{ToolCall: llm.ToolCall{Name: "http_request"}},
		},
	}

	signal, err := m.AfterIteration(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if signal != nil && signal.ShouldStop {
		t.Error("should not stop for aligned actions")
	}
}

func TestGoalAlignmentMiddleware_Drift(t *testing.T) {
	m := NewGoalAlignmentMiddleware("测试 DVWA 的 XSS 漏洞", 3)

	ctx := context.Background()

	// 连续 3 次偏离目标的操作
	for i := 1; i <= 3; i++ {
		state := &IterationState{
			Iteration: i,
			Thought:   "查看系统日志文件的权限配置",
			ToolCalls: []*Action{
				{ToolCall: llm.ToolCall{Name: "run_command"}},
			},
		}

		signal, err := m.AfterIteration(ctx, state)
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}

		if i < 3 {
			if signal != nil && signal.ShouldStop {
				t.Errorf("iteration %d: should not stop yet", i)
			}
		} else {
			if signal == nil || !signal.ShouldStop {
				t.Error("iteration 3: expected stop signal for goal drift")
			}
			if signal.Reason != StopReasonGoalDrift {
				t.Errorf("expected reason %s, got %s", StopReasonGoalDrift, signal.Reason)
			}
		}
	}
}

func TestMiddlewareChain(t *testing.T) {
	loop := NewLoopDetectionMiddleware(5, 5)
	progress := NewProgressTrackingMiddleware(10)
	goal := NewGoalAlignmentMiddleware("测试目标", 5)

	chain := NewMiddlewareChain(loop, progress, goal)

	// 触发工具循环
	state := &IterationState{
		Iteration:       10,
		RecentToolNames: []string{"tool_a", "tool_a", "tool_a", "tool_a", "tool_a"},
		OutputCount:     5,
	}

	signal, err := chain.AfterIteration(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 应该由第一个中间件（loop）停止
	if signal == nil || !signal.ShouldStop {
		t.Error("expected stop signal from chain")
	}

	if signal.Reason != StopReasonToolLoop {
		t.Errorf("expected reason %s, got %s", StopReasonToolLoop, signal.Reason)
	}
}
