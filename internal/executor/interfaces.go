// Package executor 定义执行层的核心接口。
//
// 更新（2026-08-26）：适配统一世界模型（Move 合并到 Node）
package executor

import (
	"context"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// ExecutorInterface 执行一个 Move，产出 Attempt 列表（domain-agnostic）
type ExecutorInterface interface {
	Execute(ctx context.Context, move knowledgegraph.Node) ([]evaluator.Attempt, error)
}

// Promoter 验证 Attempt 并晋升到世界模型（domain-agnostic）
type Promoter interface {
	Promote(ctx context.Context, a evaluator.Attempt) (*knowledgegraph.Node, error)
}

// Report 是认知循环的执行报告
type Report struct {
	Steps      int      // 执行的 Move 数量
	Promoted   int      // 晋升的节点数量
	Attempts   int      // 产出的 Attempt 数量
	StopWhy    string   // 停止原因
	Hypotheses []string // 待验证的假设 ID 列表
}

// 停止原因常量
const (
	stopMaxSteps   = "达到最大步数"
	stopNoProgress = "连续无进展"
	stopCanceled   = "任务取消"
	stopError      = "执行错误"
)
