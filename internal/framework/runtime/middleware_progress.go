package runtime

import (
	"context"
	"fmt"
)

// ProgressTrackingMiddleware 检测执行进展中的「有活动无产出」空转：
// 连续 N 轮迭代没有新观察产出即要求终止。
//
// 连续失败止损不在此处——由 runtime 内置机制唯一承担（迭代中即时生效、
// 阈值随装配下发）：此前这里的失败分支恒不可达（内置默认 8 先于一切
// 中间件阈值在同迭代内触发），双机制冗余且阈值失义，已删除。
//
// 空转与正常探索的区分：正常探索持续产生新观察（OutputCount 前进），
// 空转则是模型换着姿势重复无效动作（观察数停滞）。
type ProgressTrackingMiddleware struct {
	// MaxIterationsWithoutOutput 是无新输出的最大迭代次数
	MaxIterationsWithoutOutput int

	// lastOutputCount 记录上次检查时的输出数量
	lastOutputCount int

	// iterationsSinceLastOutput 记录自上次产生输出以来的迭代次数
	iterationsSinceLastOutput int
}

// NewProgressTrackingMiddleware 创建进展追踪中间件（无新输出检测）。
func NewProgressTrackingMiddleware(maxNoOutputIterations int) *ProgressTrackingMiddleware {
	if maxNoOutputIterations <= 0 {
		maxNoOutputIterations = 15
	}
	return &ProgressTrackingMiddleware{
		MaxIterationsWithoutOutput: maxNoOutputIterations,
		lastOutputCount:            0,
		iterationsSinceLastOutput:  0,
	}
}

// Name 返回中间件标识（日志/调试用）。
func (m *ProgressTrackingMiddleware) Name() string {
	return "progress_tracking"
}

// BeforeIteration 是 Middleware 接口的空实现（检测都在 AfterIteration）。
func (m *ProgressTrackingMiddleware) BeforeIteration(_ context.Context, _ *IterationState) (*StopSignal, error) {
	return nil, nil
}

// AfterIteration 在每轮结束后检测无新输出的空转信号，命中则要求终止。
func (m *ProgressTrackingMiddleware) AfterIteration(_ context.Context, state *IterationState) (*StopSignal, error) {
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
