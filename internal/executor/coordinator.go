package executor

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/rs/zerolog"
)

var _ ExecutorInterface = (*Coordinator)(nil)

// FindingLister 列出 task+host 下的 finding
type FindingLister interface {
	ListByTaskAndHost(ctx context.Context, taskID, host string, limit int) ([]finding.VulnFinding, error)
}

// Coordinator 是执行层的协调器
type Coordinator struct {
	taskID   string
	host     string
	findings FindingLister
	engine   *Engine
	logger   zerolog.Logger
}

// NewCoordinatorWithEngine 创建 Coordinator
func NewCoordinatorWithEngine(taskID, host string, findings FindingLister, engine *Engine, logger zerolog.Logger) *Coordinator {
	return &Coordinator{
		taskID:   taskID,
		host:     host,
		findings: findings,
		engine:   engine,
		logger:   logger,
	}
}

// Execute 实现 ExecutorInterface
func (c *Coordinator) Execute(ctx context.Context, action explorationgraph.Node) ([]evaluator.Attempt, error) {
	if !action.IsAction() {
		return nil, fmt.Errorf("Coordinator: 节点不是 Action: %s", action.ID)
	}

	c.logger.Info().
		Str("action_id", action.ID).
		Msg("[COORDINATOR] Execute called")

	return c.engine.Execute(ctx, action, c.taskID, c.host)
}
