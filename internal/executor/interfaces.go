// Package executor 定义执行层的核心接口。
package executor

import (
	"context"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/explorationgraph"
)

// ActionExecutor 执行单个 Action 并产出 Attempt 列表（domain-agnostic）。
// executor.Agent 事件循环经它把图上的 open 动作交给具体引擎（ReAct Engine）执行。
type ActionExecutor interface {
	Execute(ctx context.Context, action explorationgraph.Node) ([]evaluator.Attempt, error)
}

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
