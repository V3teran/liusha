// Package executor 定义执行层的核心接口。
package executor

import (
	"context"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/explorationgraph"
)

// ActionExecutor 执行单个 Action 并产出 Attempt 列表（domain-agnostic）。
// executor.Agent 事件循环经它把图上的 open 动作交给具体引擎（ReAct Engine）执行。
// 返回的 Outcome 是执行产出档（预算内完成 / 迭代预算耗尽），终态 CAS 由此分叉。
type ActionExecutor interface {
	Execute(ctx context.Context, action explorationgraph.Node) ([]evaluator.Attempt, Outcome, error)
}

// Outcome 是一次 Action 执行的产出档（与图终态一一对应：engine 判定，agent 落图）。
type Outcome string

const (
	// OutcomeDone 预算内正常收敛（含降级提取路径——那不是预算问题）。
	OutcomeDone Outcome = "completed"
	// OutcomeExhausted 迭代预算耗尽（ReAct 终态 max_iterations）：有产出但未收敛。
	// 与 failed（错误）语义区分——处置应当增预算或拆动作，而非原样重跑。
	OutcomeExhausted Outcome = "exhausted"
)

// Report 是认知循环的执行报告
type Report struct {
	Steps      int    // 执行的动作数量
	Promoted   int    // 晋升的节点数量
	Attempts   int    // 产出的 Attempt 数量
	StopReason string // 停止原因
}

// 停止原因常量
const (
	stopMaxSteps = "达到最大步数"
	stopCanceled = "任务取消"
)
