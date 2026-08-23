package main

import (
	"context"

	"github.com/V3teran/liusha/internal/cognition"
	domainweb "github.com/V3teran/liusha/internal/executor/web"
	executorweb "github.com/V3teran/liusha/internal/executor/web"
	"github.com/V3teran/liusha/internal/httpreplay"
	"github.com/V3teran/liusha/internal/planner"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/verifier"
)

// agentTrafficScope scopes AgentStore reads to a single task.
// Satisfies domainweb.TrafficSource: returns (Source, ok, err) where ok=false
// when the record does not belong to the task.
type agentTrafficScope struct {
	store  *traffic.AgentStore
	taskID string
}

func (s *agentTrafficScope) GetInScope(ctx context.Context, id int64) (httpreplay.Source, bool, error) {
	f, err := s.store.GetByID(ctx, id)
	if err != nil {
		return httpreplay.Source{}, false, err
	}
	if f.TaskID != s.taskID {
		return httpreplay.Source{}, false, nil
	}
	return httpreplay.Source{
		ID:      f.ID,
		Method:  f.Method,
		URL:     f.URL,
		Headers: f.RequestHeaders,
		Body:    f.RequestBody,
	}, true, nil
}

// runCognition drives a single engagement through the L4 cognition loop:
// Planner (strategy) → Executor (tactical run) → Verifier (promotion gate).
//
// When planStore is available, uses ExecutionLoop (execution_plan-based, async).
// Otherwise, falls back to traditional Loop (in-memory planner, sync).
func (h handler) runCognition(
	ctx context.Context,
	assignmentID, taskID, host string,
	run executorweb.AgentFunc,
) (cognition.Report, error) {
	if h.world == nil || assignmentID == "" {
		return cognition.Report{}, run(ctx, planner.Move{})
	}

	executor := executorweb.NewExecutor(assignmentID, host, h.findings, run)
	replaySource := &agentTrafficScope{store: h.agentStore, taskID: taskID}
	promoter := verifier.New(h.world, domainweb.NewReplayer(replaySource))

	// 若有 planStore，使用新的 ExecutionLoop（execution_plan 驱动）
	if h.planStore != nil && h.eventBus != nil {
		execLoop := cognition.NewExecutionLoop(
			h.planStore,
			executor,
			promoter,
			h.eventBus,
			h.logger.With().Str("component", "execution_loop").Str("task_id", taskID).Logger(),
		)
		return execLoop.Run(ctx, taskID)
	}

	// 降级到传统 Loop（内存规划器，同步）
	var strategy planner.Strategy
	if p, err := h.router.For(ctx, "planner"); err == nil {
		strategy = planner.NewLLMStrategy(p)
	} else {
		strategy = planner.NewKillChainStrategy()
	}

	loop := cognition.New(
		planner.New(h.world, strategy),
		executor,
		promoter,
		0,
	).WithEventBus(h.eventBus)

	return loop.Run(ctx, assignmentID)
}
