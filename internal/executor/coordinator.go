package executor

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/verifier"
	"github.com/V3teran/liusha/internal/worldmodel"
)

var _ ExecutorInterface = (*Coordinator)(nil)

// FindingLister 列出 task+host 下的 finding。收窄依赖 + 便于测试替身。
type FindingLister interface {
	ListByTaskAndHost(ctx context.Context, taskID, host string, limit int) ([]finding.VulnFinding, error)
}

// AgentFunc 执行一次战术 agent（RunSolo 的注入点，cmd/runner 侧实现）。
// 只返回 error——产出的漏洞由 agent 内 write_finding 落 finding.Store，Executor 事后收割。
// 「怎么打」全权归 agent（LLM 战术自由），Executor 不干预。
type AgentFunc func(ctx context.Context, move worldmodel.Node) error

// Coordinator 是执行层的协调器：跑 move-scoped agent → 收割新 finding → 转 Attempt。
type Coordinator struct {
	taskID   string
	host     string
	findings FindingLister
	run      AgentFunc
	logger   zerolog.Logger
}

// NewExecutor 构造 web Executor
func NewCoordinator(taskID, host string, findings FindingLister, run AgentFunc, logger zerolog.Logger) *Coordinator {
	return &Coordinator{taskID: taskID, host: host, findings: findings, run: run, logger: logger}
}

// Execute 实现 ExecutorInterface：快照运行前 finding → 跑 agent → 差集收割新 finding → 转 Attempt
func (c *Coordinator) Execute(ctx context.Context, action worldmodel.Node) ([]verifier.Attempt, error) {
	if !action.IsAction() {
		return nil, fmt.Errorf("Coordinator: 节点不是 Action: %s", action.ID)
	}

	c.logger.Info().
		Str("action_id", action.ID).
		Str("run_func_ptr", fmt.Sprintf("%p", c.run)).
		Msg("[COORDINATOR] Execute called")

	// 快照运行前的 finding
	before, err := c.findings.ListByTaskAndHost(ctx, c.taskID, c.host, 0)
	if err != nil {
		return nil, fmt.Errorf("Coordinator: 快照运行前 finding 失败: %w", err)
	}
	seen := make(map[string]bool, len(before))
	for _, f := range before {
		seen[f.ID] = true
	}

	c.logger.Info().
		Int("findings_before", len(before)).
		Str("action_id", action.ID).
		Msg("[COORDINATOR] Before execution snapshot")

	c.logger.Info().
		Str("action_id", action.ID).
		Msg("[COORDINATOR] Calling c.run (AgentFunc)...")

	// 执行 agent
	if err := c.run(ctx, action); err != nil {
		c.logger.Error().
			Err(err).
			Str("action_id", action.ID).
			Msg("[COORDINATOR] c.run returned error")
		return nil, fmt.Errorf("Coordinator: 战术 agent 执行失败: %w", err)
	}

	c.logger.Info().
		Str("action_id", action.ID).
		Msg("[COORDINATOR] c.run completed successfully")

	// 收割新 finding
	after, err := c.findings.ListByTaskAndHost(ctx, c.taskID, c.host, 0)
	if err != nil {
		return nil, fmt.Errorf("Coordinator: 收割 finding 失败: %w", err)
	}

	c.logger.Info().
		Int("findings_after", len(after)).
		Int("findings_before", len(before)).
		Str("action_id", action.ID).
		Msg("[COORDINATOR] After execution harvest")

	var attempts []verifier.Attempt
	for _, f := range after {
		if seen[f.ID] {
			continue
		}
		a, ok, err := AttemptFromFinding(c.taskID, f)
		if err != nil {
			return nil, fmt.Errorf("Coordinator: finding %s 转 Attempt 失败: %w", f.ID, err)
		}
		if ok {
			attempts = append(attempts, a)
		}
	}

	c.logger.Info().
		Int("attempts", len(attempts)).
		Str("action_id", action.ID).
		Msg("[COORDINATOR] Returning attempts")

	return attempts, nil
}
