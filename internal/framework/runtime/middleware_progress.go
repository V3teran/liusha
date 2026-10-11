package runtime

import (
	"context"
	"fmt"
)

// ProgressTrackingMiddleware 检测执行进展，防止有活动但无产出的空转。
//
// 检测策略：
// 1. 迭代无产出：连续 N 次迭代没有生成新的有效输出
// 2. 持续失败：连续 N 次工具调用失败
//
// 业界实践：
// - AutoGPT: 连续 5 次无进展则终止
// - CrewAI: 连续 3 次失败则重新规划
// - BabyAGI: 连续 10 次无新任务则认为完成
//
// 本实现区分"空转"和"正常探索"：
// - 空转：重复失败、无新输出
// - 正常探索：尝试不同方法、产生新观察
type ProgressTrackingMiddleware struct {
	// MaxIterationsWithoutOutput 是无新输出的最大迭代次数
	MaxIterationsWithoutOutput int

	// MaxConsecutiveFailures 是连续失败的最大次数
	MaxConsecutiveFailures int

	// lastOutputCount 记录上次检查时的输出数量
	lastOutputCount int

	// iterationsSinceLastOutput 记录自上次产生输出以来的迭代次数
	iterationsSinceLastOutput int
}

// NewProgressTrackingMiddleware 创建进展追踪中间件。
//
// 参数：
//   - maxNoOutputIterations: 无新输出的最大迭代次数（推荐 15-20）
//   - maxConsecutiveFailures: 连续失败的最大次数（推荐 8-12）
func NewProgressTrackingMiddleware(maxNoOutputIterations, maxConsecutiveFailures int) *ProgressTrackingMiddleware {
	if maxNoOutputIterations <= 0 {
		maxNoOutputIterations = 15
	}
	if maxConsecutiveFailures <= 0 {
		maxConsecutiveFailures = 10
	}
	return &ProgressTrackingMiddleware{
		MaxIterationsWithoutOutput: maxNoOutputIterations,
		MaxConsecutiveFailures:     maxConsecutiveFailures,
		lastOutputCount:            0,
		iterationsSinceLastOutput:  0,
	}
}

func (m *ProgressTrackingMiddleware) Name() string {
	return "progress_tracking"
}

func (m *ProgressTrackingMiddleware) BeforeIteration(ctx context.Context, state *IterationState) (*StopSignal, error) {
	// BeforeIteration 不做检测
	return nil, nil
}

func (m *ProgressTrackingMiddleware) AfterIteration(ctx context.Context, state *IterationState) (*StopSignal, error) {
	// 检测 1: 连续失败
	if state.ConsecutiveToolFailures >= m.MaxConsecutiveFailures {
		return &StopSignal{
			ShouldStop: true,
			Reason:     StopReasonNoProgress,
			Message: fmt.Sprintf(
				"连续 %d 次工具调用失败，任务可能无法完成",
				state.ConsecutiveToolFailures,
			),
		}, nil
	}

	// 检测 2: 长时间无新输出
	currentOutputCount := state.OutputCount
	if currentOutputCount > m.lastOutputCount {
		// 有新输出，重置计数器
		m.lastOutputCount = currentOutputCount
		m.iterationsSinceLastOutput = 0
	} else {
		// 无新输出，累计
		m.iterationsSinceLastOutput++
	}

	if m.iterationsSinceLastOutput >= m.MaxIterationsWithoutOutput {
		return &StopSignal{
			ShouldStop: true,
			Reason:     StopReasonNoProgress,
			Message: fmt.Sprintf(
				"连续 %d 次迭代无新输出（observation/result），疑似空转",
				m.iterationsSinceLastOutput,
			),
		}, nil
	}

	return nil, nil
}
