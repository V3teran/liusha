package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunner_SimpleWorkflow(t *testing.T) {
	// 创建简单配置
	config := &WorkflowConfig{
		Name: "simple_workflow",
		Agents: []AgentConfig{
			{Name: "planner", Type: "planner"},
			{Name: "executor", Type: "executor"},
		},
		Workflow: []StepConfig{
			{
				Name:   "plan",
				Agent:  "planner",
				Input:  map[string]interface{}{"task": "scan"},
				Output: "plan_result",
			},
			{
				Name:   "execute",
				Agent:  "executor",
				Input:  map[string]interface{}{"plan": "{{ steps.plan.output }}"},
				Output: "execution_result",
			},
		},
	}

	// 创建 Runner
	factory := NewMockAgentFactory()
	runner, err := NewRunner(config, factory)
	require.NoError(t, err)

	// 执行工作流
	ctx := context.Background()
	err = runner.Run(ctx)
	require.NoError(t, err)

	// 验证输出
	planOutput, ok := runner.GetOutput("plan")
	require.True(t, ok)
	assert.NotNil(t, planOutput)

	executeOutput, ok := runner.GetOutput("execute")
	require.True(t, ok)
	assert.NotNil(t, executeOutput)

	// 验证所有输出
	allOutputs := runner.GetAllOutputs()
	assert.Len(t, allOutputs, 2)
}

func TestRunner_WithLogger(t *testing.T) {
	config := &WorkflowConfig{
		Name: "test",
		Agents: []AgentConfig{
			{Name: "agent1", Type: "planner"},
		},
		Workflow: []StepConfig{
			{Name: "step1", Agent: "agent1"},
		},
	}

	factory := NewMockAgentFactory()
	logger := zerolog.Nop()

	runner, err := NewRunner(config, factory, WithLogger(logger))
	require.NoError(t, err)
	assert.NotNil(t, runner.logger)
}

func TestRunner_WithContext(t *testing.T) {
	config := &WorkflowConfig{
		Name: "test",
		Agents: []AgentConfig{
			{Name: "agent1", Type: "planner"},
		},
		Workflow: []StepConfig{
			{Name: "step1", Agent: "agent1"},
		},
	}

	factory := NewMockAgentFactory()
	ctx := map[string]interface{}{
		"task_id": "task-123",
		"user":    "test-user",
	}

	runner, err := NewRunner(config, factory, WithContext(ctx))
	require.NoError(t, err)
	assert.Equal(t, "task-123", runner.context["task_id"])
}

func TestRunner_Timeout(t *testing.T) {
	config := &WorkflowConfig{
		Name: "test",
		Agents: []AgentConfig{
			{Name: "agent1", Type: "planner"},
		},
		Workflow: []StepConfig{
			{
				Name:    "step1",
				Agent:   "agent1",
				Timeout: 1, // 1 秒超时
			},
		},
	}

	factory := NewMockAgentFactory()
	runner, err := NewRunner(config, factory)
	require.NoError(t, err)

	ctx := context.Background()
	err = runner.Run(ctx)
	require.NoError(t, err) // PoC 版本不会真正超时
}

func TestRunner_MultipleSteps(t *testing.T) {
	config := &WorkflowConfig{
		Name: "multi_step_workflow",
		Agents: []AgentConfig{
			{Name: "monitor", Type: "monitor"},
			{Name: "planner", Type: "planner"},
			{Name: "executor", Type: "executor"},
			{Name: "evaluator", Type: "evaluator"},
		},
		Workflow: []StepConfig{
			{Name: "initialize", Agent: "monitor", Output: "task"},
			{Name: "plan", Agent: "planner", Output: "actions"},
			{Name: "execute", Agent: "executor", Output: "observations"},
			{Name: "evaluate", Agent: "evaluator", Output: "results"},
		},
	}

	factory := NewMockAgentFactory()
	runner, err := NewRunner(config, factory)
	require.NoError(t, err)

	ctx := context.Background()
	err = runner.Run(ctx)
	require.NoError(t, err)

	// 验证所有步骤都有输出
	allOutputs := runner.GetAllOutputs()
	assert.Len(t, allOutputs, 4)
	assert.Contains(t, allOutputs, "initialize")
	assert.Contains(t, allOutputs, "plan")
	assert.Contains(t, allOutputs, "execute")
	assert.Contains(t, allOutputs, "evaluate")
}

func TestRunner_Cleanup(t *testing.T) {
	config := &WorkflowConfig{
		Name: "test",
		Agents: []AgentConfig{
			{Name: "agent1", Type: "planner"},
		},
		Workflow: []StepConfig{
			{Name: "step1", Agent: "agent1"},
		},
	}

	factory := NewMockAgentFactory()
	runner, err := NewRunner(config, factory)
	require.NoError(t, err)

	ctx := context.Background()
	err = runner.Run(ctx)
	require.NoError(t, err)

	// 清理资源
	err = runner.Cleanup(ctx)
	require.NoError(t, err)
}

func TestRunner_InvalidConfig(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		factory := NewMockAgentFactory()
		_, err := NewRunner(nil, factory)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "config is required")
	})

	t.Run("nil factory", func(t *testing.T) {
		config := &WorkflowConfig{
			Name:   "test",
			Agents: []AgentConfig{{Name: "a", Type: "planner"}},
			Workflow: []StepConfig{{Name: "s", Agent: "a"}},
		}
		_, err := NewRunner(config, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "factory is required")
	})
}

func TestRunner_GetOutputNotFound(t *testing.T) {
	config := &WorkflowConfig{
		Name: "test",
		Agents: []AgentConfig{
			{Name: "agent1", Type: "planner"},
		},
		Workflow: []StepConfig{
			{Name: "step1", Agent: "agent1"},
		},
	}

	factory := NewMockAgentFactory()
	runner, err := NewRunner(config, factory)
	require.NoError(t, err)

	_, ok := runner.GetOutput("nonexistent")
	assert.False(t, ok)
}

func TestMockAgentFactory(t *testing.T) {
	factory := NewMockAgentFactory()

	// 注册自定义 Agent
	mockAgent := NewMockAgent("custom")
	factory.RegisterAgent("planner", mockAgent)

	// 创建 Agent
	config := AgentConfig{Name: "test", Type: "planner"}
	agent, err := factory.CreateAgent(config)
	require.NoError(t, err)
	assert.Equal(t, "custom", agent.Name())

	// 未注册的类型会创建默认 Mock
	config2 := AgentConfig{Name: "test2", Type: "executor"}
	agent2, err := factory.CreateAgent(config2)
	require.NoError(t, err)
	assert.NotNil(t, agent2)
}

func TestMockAgent(t *testing.T) {
	agent := NewMockAgent("test_agent")

	assert.Equal(t, "test_agent", agent.Name())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Run 应该等待 ctx 取消
	err := agent.Run(ctx)
	assert.Error(t, err) // context deadline exceeded

	// Stop 应该成功
	err = agent.Stop(context.Background())
	assert.NoError(t, err)

	// ExportState 应该返回 JSON
	state, err := agent.ExportState()
	require.NoError(t, err)
	assert.Contains(t, string(state), "test_agent")

	// ImportState 应该成功
	err = agent.ImportState([]byte(`{}`))
	assert.NoError(t, err)
}

func TestRunner_EndToEnd(t *testing.T) {
	// 端到端测试：完整的工作流执行
	yaml := `
name: end_to_end_test
description: Complete workflow execution test

agents:
  - name: monitor
    type: monitor
    config:
      interval: 30

  - name: planner
    type: planner
    config:
      max_actions: 10

  - name: executor
    type: executor
    config:
      pool_size: 5

  - name: evaluator
    type: evaluator

workflow:
  - name: initialize
    agent: monitor
    input:
      task_id: "task-123"
    output: task

  - name: generate_plan
    agent: planner
    input:
      task: "{{ steps.initialize.output }}"
    output: plan
    timeout: 60

  - name: execute_actions
    agent: executor
    input:
      actions: "{{ steps.generate_plan.output.actions }}"
    output: observations
    parallel: true
    timeout: 300

  - name: verify_results
    agent: evaluator
    input:
      observations: "{{ steps.execute_actions.output }}"
    output: results

  - name: finalize
    agent: monitor
    input:
      results: "{{ steps.verify_results.output }}"
    output: final_report
`

	config, err := ParseWorkflowConfig([]byte(yaml))
	require.NoError(t, err)

	factory := NewMockAgentFactory()
	logger := zerolog.Nop()

	runner, err := NewRunner(config, factory, WithLogger(logger))
	require.NoError(t, err)

	ctx := context.Background()
	err = runner.Run(ctx)
	require.NoError(t, err)

	// 验证所有步骤都执行了
	allOutputs := runner.GetAllOutputs()
	assert.Len(t, allOutputs, 5)

	// 验证每个步骤的输出
	for _, stepName := range []string{"initialize", "generate_plan", "execute_actions", "verify_results", "finalize"} {
		output, ok := runner.GetOutput(stepName)
		assert.True(t, ok, "step %s should have output", stepName)
		assert.NotNil(t, output)

		// 验证输出包含预期字段
		outputMap, ok := output.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "success", outputMap["status"])
		assert.Equal(t, stepName, outputMap["step"])
	}

	// 清理
	err = runner.Cleanup(ctx)
	require.NoError(t, err)
}
