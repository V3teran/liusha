// Package lateral 实现内网横向移动/AD域渗透领域的 Executor
package lateral

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/cognition"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/planner"
	"github.com/V3teran/liusha/internal/verifier"
)

var _ cognition.Executor = (*Executor)(nil)

// FindingLister 列出 task+host 下的 finding
type FindingLister interface {
	ListByTaskAndHost(ctx context.Context, taskID, host string, limit int) ([]finding.VulnFinding, error)
}

// AgentFunc 按招法执行横向移动 agent
type AgentFunc func(ctx context.Context, m planner.Move) error

// Executor 是 lateral 域的 cognition.Executor：
// 执行内网横向移动、AD 域渗透、后渗透等任务
type Executor struct {
	taskID   string
	target   string // 内网目标（主机名/IP/域控）
	findings FindingLister
	run      AgentFunc
}

// NewExecutor 构造 lateral Executor
func NewExecutor(taskID, target string, findings FindingLister, run AgentFunc) *Executor {
	return &Executor{
		taskID:   taskID,
		target:   target,
		findings: findings,
		run:      run,
	}
}

// Execute 实现 cognition.Executor：
// 快照 → 执行横向移动 agent → 收割新 finding → 转 Attempt
func (e *Executor) Execute(ctx context.Context, m planner.Move) ([]verifier.Attempt, error) {
	// 快照运行前的 finding
	before, err := e.findings.ListByTaskAndHost(ctx, e.taskID, e.target, 0)
	if err != nil {
		return nil, fmt.Errorf("lateral.Executor: 快照运行前 finding 失败: %w", err)
	}
	seen := make(map[string]bool, len(before))
	for _, f := range before {
		seen[f.ID] = true
	}

	// 执行横向移动 agent
	if err := e.run(ctx, m); err != nil {
		return nil, fmt.Errorf("lateral.Executor: 横向移动 agent 执行失败: %w", err)
	}

	// 收割新 finding
	after, err := e.findings.ListByTaskAndHost(ctx, e.taskID, e.target, 0)
	if err != nil {
		return nil, fmt.Errorf("lateral.Executor: 收割 finding 失败: %w", err)
	}

	var attempts []verifier.Attempt
	for _, f := range after {
		if seen[f.ID] {
			continue
		}
		a, ok, err := AttemptFromFinding(e.taskID, f)
		if err != nil {
			return nil, fmt.Errorf("lateral.Executor: finding %s 转 Attempt 失败: %w", f.ID, err)
		}
		if ok {
			attempts = append(attempts, a)
		}
	}
	return attempts, nil
}
