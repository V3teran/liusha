package orchestrator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWorkflowConfig(t *testing.T) {
	t.Run("valid simple config", func(t *testing.T) {
		yaml := `
name: simple_scan
description: Simple security scan workflow

agents:
  - name: planner
    type: planner
    config:
      max_actions: 10

  - name: executor
    type: executor
    config:
      pool_size: 5

workflow:
  - name: generate_plan
    agent: planner
    input:
      task_id: "task-123"
    output: plan

  - name: execute_actions
    agent: executor
    input:
      actions: "{{ steps.generate_plan.output.actions }}"
    output: observations
`

		config, err := ParseWorkflowConfig([]byte(yaml))
		require.NoError(t, err)

		assert.Equal(t, "simple_scan", config.Name)
		assert.Equal(t, "Simple security scan workflow", config.Description)
		assert.Len(t, config.Agents, 2)
		assert.Len(t, config.Workflow, 2)

		// 验证 Agent
		assert.Equal(t, "planner", config.Agents[0].Name)
		assert.Equal(t, "planner", config.Agents[0].Type)
		assert.Equal(t, 10, config.Agents[0].Config["max_actions"])

		// 验证 Workflow
		assert.Equal(t, "generate_plan", config.Workflow[0].Name)
		assert.Equal(t, "planner", config.Workflow[0].Agent)
		assert.Equal(t, "plan", config.Workflow[0].Output)
	})

	t.Run("missing name", func(t *testing.T) {
		yaml := `
agents:
  - name: planner
    type: planner

workflow:
  - name: step1
    agent: planner
`

		_, err := ParseWorkflowConfig([]byte(yaml))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "name is required")
	})

	t.Run("no agents", func(t *testing.T) {
		yaml := `
name: test
agents: []
workflow:
  - name: step1
    agent: planner
`

		_, err := ParseWorkflowConfig([]byte(yaml))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "at least one agent is required")
	})

	t.Run("duplicate agent name", func(t *testing.T) {
		yaml := `
name: test
agents:
  - name: planner
    type: planner
  - name: planner
    type: executor

workflow:
  - name: step1
    agent: planner
`

		_, err := ParseWorkflowConfig([]byte(yaml))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate name")
	})

	t.Run("invalid agent type", func(t *testing.T) {
		yaml := `
name: test
agents:
  - name: agent1
    type: invalid_type

workflow:
  - name: step1
    agent: agent1
`

		_, err := ParseWorkflowConfig([]byte(yaml))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid type")
	})

	t.Run("agent not found in workflow", func(t *testing.T) {
		yaml := `
name: test
agents:
  - name: planner
    type: planner

workflow:
  - name: step1
    agent: nonexistent
`

		_, err := ParseWorkflowConfig([]byte(yaml))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found in agents list")
	})

	t.Run("duplicate step name", func(t *testing.T) {
		yaml := `
name: test
agents:
  - name: planner
    type: planner

workflow:
  - name: step1
    agent: planner
  - name: step1
    agent: planner
`

		_, err := ParseWorkflowConfig([]byte(yaml))
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate name")
	})
}

func TestWorkflowConfig_GetAgent(t *testing.T) {
	config := &WorkflowConfig{
		Agents: []AgentConfig{
			{Name: "planner", Type: "planner"},
			{Name: "executor", Type: "executor"},
		},
	}

	t.Run("found", func(t *testing.T) {
		agent, err := config.GetAgent("planner")
		require.NoError(t, err)
		assert.Equal(t, "planner", agent.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := config.GetAgent("nonexistent")
		assert.Error(t, err)
	})
}

func TestWorkflowConfig_GetStep(t *testing.T) {
	config := &WorkflowConfig{
		Workflow: []StepConfig{
			{Name: "step1", Agent: "planner"},
			{Name: "step2", Agent: "executor"},
		},
	}

	t.Run("found", func(t *testing.T) {
		step, err := config.GetStep("step1")
		require.NoError(t, err)
		assert.Equal(t, "step1", step.Name)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := config.GetStep("nonexistent")
		assert.Error(t, err)
	})
}

func TestWorkflowConfig_ToYAML(t *testing.T) {
	config := &WorkflowConfig{
		Name: "test",
		Agents: []AgentConfig{
			{Name: "planner", Type: "planner"},
		},
		Workflow: []StepConfig{
			{Name: "step1", Agent: "planner"},
		},
	}

	yaml, err := config.ToYAML()
	require.NoError(t, err)
	assert.Contains(t, string(yaml), "name: test")
	assert.Contains(t, string(yaml), "planner")
}

func TestAgentTypes(t *testing.T) {
	types := AgentTypes()
	assert.Len(t, types, 4)
	assert.Contains(t, types, "planner")
	assert.Contains(t, types, "executor")
	assert.Contains(t, types, "monitor")
	assert.Contains(t, types, "evaluator")
}

func TestWorkflowConfig_ComplexConfig(t *testing.T) {
	yaml := `
name: advanced_scan
description: Advanced security scanning with multiple agents

agents:
  - name: monitor
    type: monitor
    config:
      check_interval: 30

  - name: planner
    type: planner
    config:
      max_actions: 20
      strategy: "aggressive"

  - name: executor_pool
    type: executor
    config:
      pool_size: 10
      timeout: 600

  - name: evaluator
    type: evaluator
    config:
      confidence_threshold: 0.8

workflow:
  - name: initialize
    agent: monitor
    output: task

  - name: generate_plan
    agent: planner
    input:
      task: "{{ steps.initialize.output }}"
      mode: "full_scan"
    output: plan
    timeout: 120

  - name: execute_actions
    agent: executor_pool
    input:
      actions: "{{ steps.generate_plan.output.actions }}"
    output: observations
    parallel: true
    timeout: 600

  - name: verify_results
    agent: evaluator
    input:
      observations: "{{ steps.execute_actions.output }}"
    output: results
    condition: "{{ steps.execute_actions.success }}"

  - name: report
    agent: monitor
    input:
      results: "{{ steps.verify_results.output }}"
`

	config, err := ParseWorkflowConfig([]byte(yaml))
	require.NoError(t, err)

	assert.Equal(t, "advanced_scan", config.Name)
	assert.Len(t, config.Agents, 4)
	assert.Len(t, config.Workflow, 5)

	// 验证复杂配置
	plannerAgent, err := config.GetAgent("planner")
	require.NoError(t, err)
	assert.Equal(t, "aggressive", plannerAgent.Config["strategy"])

	executeStep, err := config.GetStep("execute_actions")
	require.NoError(t, err)
	assert.True(t, executeStep.Parallel)
	assert.Equal(t, 600, executeStep.Timeout)

	verifyStep, err := config.GetStep("verify_results")
	require.NoError(t, err)
	assert.Equal(t, "{{ steps.execute_actions.success }}", verifyStep.Condition)
}
