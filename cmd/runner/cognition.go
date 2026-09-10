package main

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/httpreplay"
	"github.com/V3teran/liusha/internal/planner"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/evaluator"
)

// agentTrafficScope scopes AgentStore reads to a single task.
// Satisfies executor.TrafficSource: returns (Source, ok, err) where ok=false
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
// PlannerAgent (async strategy) → Loop → Agent (tactical run) → Verifier (promotion gate).
func (h handler) runCognition(
	ctx context.Context,
	assignmentID, taskID, host string,
	run executor.AgentFunc,
) (executor.Report, error) {
	h.logger.Info().
		Str("task_id", taskID).
		Str("run_func_ptr", fmt.Sprintf("%p", run)).
		Msg("[RUN_COGNITION] Entry: received AgentFunc")

	if h.world == nil || taskID == "" || h.eventBus == nil {
		return executor.Report{}, fmt.Errorf("world and eventBus are required")
	}

	coord := executor.NewCoordinator(taskID, host, h.findings, run, h.logger)
	replaySource := &agentTrafficScope{store: h.agentStore, taskID: taskID}
	promoter := evaluator.New(h.world, executor.NewReplayer(replaySource))

	// 启动 PlannerAgent（异步规划器）
	// PlannerAgent 内部会通过 h.router 获取 LLM

	plannerAgent := planner.New(planner.Config{
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

	// 等待初始规划完成后再启动 Loop
	select {
	case <-plannerAgent.WaitInitialPlanDone():
		h.logger.Info().Str("task_id", taskID).Msg("initial planning done, starting execution loop")
	case <-ctx.Done():
		return executor.Report{}, ctx.Err()
	}

	execLoop := executor.NewLoop(
		h.world,
		coord,
		promoter,
		h.eventBus,
		h.logger.With().Str("component", "execution_loop").Str("task_id", taskID).Logger(),
	)
	return execLoop.Run(ctx, taskID)
}
