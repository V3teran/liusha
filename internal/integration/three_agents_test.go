package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/knowledgegraph"
	"github.com/V3teran/liusha/internal/planner"
	"github.com/stretchr/testify/require"
)

// TestThreeAgents_Basic 测试三个 Agent 的基本协同
func TestThreeAgents_Basic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 创建基础设施
	logger := zerolog.New(zerolog.NewTestWriter(t)).With().Timestamp().Logger()
	eventBus := bus.New(ctx)
	graphStore := core.NewInMemoryGraphStore()
	world := knowledgegraph.NewAdapterStore(graphStore)

	taskID := "test-task-1"

	// 创建初始 Objective 节点
	objective := knowledgegraph.Node{
		ID:      "objective-1",
		TaskID:  taskID,
		Kind:    core.KindObjective,
		Content: []byte(`{"description": "测试目标"}`),
	}
	_, err := world.CreateNode(ctx, objective)
	require.NoError(t, err)

	// 创建三个 Agent
	// 1. PlannerAgent
	plannerCfg := planner.PlannerAgentConfig{
		TaskID:   taskID,
		World:    world,
		Planner:  &mockPlanner{},
		EventBus: eventBus,
		Logger:   logger,
	}
	plannerAgent := planner.NewPlannerAgent(plannerCfg)

	// 2. ExecutorAgent
	executorCfg := executor.ExecutorAgentConfig{
		TaskID:   taskID,
		World:    world,
		Executor: &mockExecutor{},
		EventBus: eventBus,
		Logger:   logger,
		MaxSteps: 10,
	}
	executorAgent := executor.NewExecutorAgent(executorCfg)

	// 3. EvaluatorAgent
	mockReplayer := &mockReplayer{}
	evaluatorCfg := evaluator.EvaluatorAgentConfig{
		TaskID:    taskID,
		Evaluator: evaluator.New(world, mockReplayer, nil), // 添加第三个参数 nil (findingWriter)
		EventBus:  eventBus,
		Logger:    logger,
	}
	evaluatorAgent := evaluator.NewEvaluatorAgent(evaluatorCfg)

	// 使用 AgentRunner 统一管理三个 Agent
	runner := core.NewAgentRunner(core.AgentRunnerConfig{
		Logger:       logger,
		StartTimeout: 10 * time.Second,
		StopTimeout:  5 * time.Second,
	})

	runner.AddAgent(plannerAgent)
	runner.AddAgent(executorAgent)
	runner.AddAgent(evaluatorAgent)

	// 启动所有 Agent（AgentRunner 自动并发启动）
	go func() {
		if err := runner.Start(ctx); err != nil {
			t.Logf("AgentRunner 错误: %v", err)
		}
	}()

	// 发起初始事件
	eventBus.PublishTaskStarted(taskID)

	// 等待一段时间让 Agent 运行
	time.Sleep(5 * time.Second)

	// 停止所有 Agent
	cancel()
	if err := runner.Stop(context.Background()); err != nil {
		t.Logf("停止 Agent 时出错: %v", err)
	}

	// 验证结果：检查知识图谱中是否有生成的 Action
	actions, err := world.ListAllActions(ctx, taskID)
	require.NoError(t, err)
	t.Logf("生成的 Action 数量: %d", len(actions))
}

// mockPlanner 是测试用的 Planner
type mockPlanner struct{}

func (m *mockPlanner) Plan(ctx context.Context, world *knowledgegraph.Store, taskID string) ([]knowledgegraph.Node, error) {
	// 简单返回一个 Action
	openState := knowledgegraph.StateOpen
	actionContent := []byte(`{"description": "测试 Action"}`)
	return []knowledgegraph.Node{
		{
			ID:       "action-1",
			TaskID:   taskID,
			Kind:     core.KindAction,
			Content:  actionContent,
			State:    &openState,
			Priority: knowledgegraph.PriorityHigh,
		},
	}, nil
}

// mockExecutor 是测试用的 Executor
type mockExecutor struct{}

func (m *mockExecutor) Execute(ctx context.Context, action knowledgegraph.Node) ([]evaluator.Attempt, error) {
	// 简单返回一个 Attempt
	return []evaluator.Attempt{
		{
			TaskID:     action.TaskID,
			NodeID:     action.ID,
			Kind:       core.KindObservation,
			Primitives: []byte(`[]`),
			Content:    []byte(`{"result": "测试执行结果"}`),
			Priority:   string(knowledgegraph.PriorityMedium),
		},
	}, nil
}

// mockReplayer 是测试用的 Replayer
type mockReplayer struct{}

func (m *mockReplayer) Replay(ctx context.Context, primitives json.RawMessage) (evaluator.Result, error) {
	// 测试用：总是返回验证通过
	return evaluator.Result{
		Confirmed:  true,
		Evaluation: []byte(`{"test": "evidence"}`),
		DurationMs: 100,
	}, nil
}
