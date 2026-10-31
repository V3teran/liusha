package executor

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/rs/zerolog"
)

var _ ActionExecutor = (*Coordinator)(nil)

// Coordinator 是执行层的协调器
type Coordinator struct {
	taskID string
	host   string
	engine *Engine
	logger zerolog.Logger
}

// NewCoordinatorWithEngine 创建 Coordinator。
// （历史参数 findings FindingLister 已删：prompt 注入既有 finding 的特性在架构演进中
// 被按需工具检索取代，该字段自始至终无消费者。）
func NewCoordinatorWithEngine(taskID, host string, engine *Engine, logger zerolog.Logger) *Coordinator {
	return &Coordinator{
		taskID: taskID,
		host:   host,
		engine: engine,
		logger: logger,
	}
}

// Execute 实现 ExecutorInterface
func (c *Coordinator) Execute(ctx context.Context, action explorationgraph.Node) ([]evaluator.Attempt, Outcome, error) {
	if !action.IsAction() {
		return nil, OutcomeDone, fmt.Errorf("Coordinator: 节点不是 Action: %s", action.ID)
	}

	c.logger.Info().
		Str("action_id", action.ID).
		Msg("[COORDINATOR] Execute called")

	return c.engine.Execute(ctx, action)
}
