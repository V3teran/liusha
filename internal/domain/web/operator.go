package web

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/cognition"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/verifier"
	"github.com/V3teran/liusha/internal/worldmodel"
)

var _ cognition.Executor = (*Executor)(nil)

// FindingLister 列出 task+host 下的 finding。收窄依赖 + 便于测试替身。
type FindingLister interface {
	ListByTaskAndHost(ctx context.Context, taskID, host string, limit int) ([]finding.VulnFinding, error)
}

// AgentFunc 执行一次战术 agent（RunSolo 的注入点，cmd/runner 侧实现）。
// 只返回 error——产出的漏洞由 agent 内 write_finding 落 finding.Store，Executor 事后收割。
// 「怎么打」全权归 agent（LLM 战术自由），Executor 不干预。
type AgentFunc func(ctx context.Context, move worldmodel.Node) error

// Executor 是 web 域的 cognition.Executor：跑 move-scoped agent → 收割新 finding → 转 Attempt。
type Executor struct {
	taskID   string
	host     string
	findings FindingLister
	run      AgentFunc
}

// NewExecutor 构造 web Executor
func NewExecutor(taskID, host string, findings FindingLister, run AgentFunc) *Executor {
	return &Executor{taskID: taskID, host: host, findings: findings, run: run}
}

// Execute 实现 cognition.Executor：快照运行前 finding → 跑 agent → 差集收割新 finding → 转 Attempt
func (o *Executor) Execute(ctx context.Context, action worldmodel.Node) ([]verifier.Attempt, error) {
	if !action.IsAction() {
		return nil, fmt.Errorf("web.Executor: 节点不是 Action: %s", action.ID)
	}

	// 快照运行前的 finding
	before, err := o.findings.ListByTaskAndHost(ctx, o.taskID, o.host, 0)
	if err != nil {
		return nil, fmt.Errorf("web.Executor: 快照运行前 finding 失败: %w", err)
	}
	seen := make(map[string]bool, len(before))
	for _, f := range before {
		seen[f.ID] = true
	}

	// 执行 agent
	if err := o.run(ctx, action); err != nil {
		return nil, fmt.Errorf("web.Executor: 战术 agent 执行失败: %w", err)
	}

	// 收割新 finding
	after, err := o.findings.ListByTaskAndHost(ctx, o.taskID, o.host, 0)
	if err != nil {
		return nil, fmt.Errorf("web.Executor: 收割 finding 失败: %w", err)
	}

	var attempts []verifier.Attempt
	for _, f := range after {
		if seen[f.ID] {
			continue
		}
		a, ok, err := AttemptFromFinding(o.taskID, f)
		if err != nil {
			return nil, fmt.Errorf("web.Executor: finding %s 转 Attempt 失败: %w", f.ID, err)
		}
		if ok {
			attempts = append(attempts, a)
		}
	}
	return attempts, nil
}
