package main

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/cognition"
	domainweb "github.com/V3teran/liusha/internal/executor/web"
	executorweb "github.com/V3teran/liusha/internal/executor/web"
	"github.com/V3teran/liusha/internal/httpreplay"
	"github.com/V3teran/liusha/internal/planneragent"
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
// PlannerAgent (async strategy) → ExecutionLoop → Executor (tactical run) → Verifier (promotion gate).
func (h handler) runCognition(
	ctx context.Context,
	assignmentID, taskID, host string,
	run executorweb.AgentFunc,
) (cognition.Report, error) {
	if h.world == nil || taskID == "" || h.eventBus == nil {
		return cognition.Report{}, fmt.Errorf("world and eventBus are required")
	}

	executor := executorweb.NewExecutor(taskID, host, h.findings, run)
	replaySource := &agentTrafficScope{store: h.agentStore, taskID: taskID}
	promoter := verifier.New(h.world, domainweb.NewReplayer(replaySource))

	// 启动 PlannerAgent（异步规划器）
	// PlannerAgent 内部会通过 h.router 获取 LLM

	plannerAgent := planneragent.New(planneragent.Config{
		TaskID:       taskID,
		EventBus:     h.eventBus,
		World:        h.world,
		ControlPlane: h.controlPlane,
		Router:       h.router,
		Logger:       h.logger,
	})

	// 在独立 goroutine 中启动 PlannerAgent
	go func() {
		if err := plannerAgent.Start(ctx); err != nil && ctx.Err() == nil {
			h.logger.Error().Err(err).Str("task_id", taskID).Msg("planner agent stopped with error")
		}
	}()
	h.logger.Info().Str("task_id", taskID).Msg("planner agent started")

	execLoop := cognition.NewExecutionLoop(
		h.world,
		executor,
		promoter,
		h.eventBus,
		h.logger.With().Str("component", "execution_loop").Str("task_id", taskID).Logger(),
	)
	return execLoop.Run(ctx, taskID)
}
