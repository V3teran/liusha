package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration_SimpleSecurityScan 验证完整的工作流执行
func TestIntegration_SimpleSecurityScan(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// 1. 加载真实配置文件
	configPath := filepath.Join("..", "..", "..", "workflows", "simple_security_scan.yaml")
	config, err := LoadWorkflowConfig(configPath)
	require.NoError(t, err)

	t.Logf("Loaded workflow: %s", config.Name)
	t.Logf("Description: %s", config.Description)
	t.Logf("Agents: %d", len(config.Agents))
	t.Logf("Steps: %d", len(config.Workflow))

	// 2. 验证配置内容
	assert.Equal(t, "simple_security_scan", config.Name)
	assert.Len(t, config.Agents, 2)
	assert.Len(t, config.Workflow, 2)

	// 验证 Agents
	plannerAgent, err := config.GetAgent("planner")
	require.NoError(t, err)
	assert.Equal(t, "planner", plannerAgent.Type)
	assert.Equal(t, 10, plannerAgent.Config["max_actions"])

	executorAgent, err := config.GetAgent("executor")
	require.NoError(t, err)
	assert.Equal(t, "executor", executorAgent.Type)
	assert.Equal(t, 5, executorAgent.Config["pool_size"])

	// 验证 Workflow Steps
	planStep, err := config.GetStep("generate_plan")
	require.NoError(t, err)
	assert.Equal(t, "planner", planStep.Agent)
	assert.Equal(t, "plan", planStep.Output)
	assert.Equal(t, 60, planStep.Timeout)

	executeStep, err := config.GetStep("execute_scan")
	require.NoError(t, err)
	assert.Equal(t, "executor", executeStep.Agent)
	assert.Equal(t, "observations", executeStep.Output)
	assert.Equal(t, 300, executeStep.Timeout)

	// 3. 创建 Runner 并执行
	factory := NewMockAgentFactory()
	logger := zerolog.New(os.Stdout).With().
		Str("test", "integration").
		Logger()

	initialContext := map[string]interface{}{
		"task_id":   "task-integration-001",
		"objective": "Scan target for security vulnerabilities",
		"target":    "https://example.com",
	}

	runner, err := NewRunner(
		config,
		factory,
		WithLogger(logger),
		WithContext(initialContext),
	)
	require.NoError(t, err)

	ctx := context.Background()
	err = runner.Run(ctx)
	require.NoError(t, err)

	// 4. 验证执行结果
	allOutputs := runner.GetAllOutputs()
	t.Logf("Workflow outputs: %d steps", len(allOutputs))

	// 验证 Step 1 输出
	planOutput, ok := runner.GetOutput("generate_plan")
	require.True(t, ok, "generate_plan should have output")
	assert.NotNil(t, planOutput)
	t.Logf("Plan output: %+v", planOutput)

	// 验证 Step 2 输出
	executeOutput, ok := runner.GetOutput("execute_scan")
	require.True(t, ok, "execute_scan should have output")
	assert.NotNil(t, executeOutput)
	t.Logf("Execute output: %+v", executeOutput)

	// 5. 清理资源
	err = runner.Cleanup(ctx)
	require.NoError(t, err)

	t.Log("✅ Integration test passed: workflow execution successful")
}

// TestIntegration_ConfigValidation 验证配置验证功能
func TestIntegration_ConfigValidation(t *testing.T) {
	tests := []struct {
		name        string
		yaml        string
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid minimal config",
			yaml: `
name: minimal
agents:
  - name: agent1
    type: planner
workflow:
  - name: step1
    agent: agent1
`,
			expectError: false,
		},
		{
			name: "missing workflow name",
			yaml: `
agents:
  - name: agent1
    type: planner
workflow:
  - name: step1
    agent: agent1
`,
			expectError: true,
			errorMsg:    "name is required",
		},
		{
			name: "invalid agent type",
			yaml: `
name: test
agents:
  - name: agent1
    type: invalid_type
workflow:
  - name: step1
    agent: agent1
`,
			expectError: true,
			errorMsg:    "invalid type",
		},
		{
			name: "undefined agent reference",
			yaml: `
name: test
agents:
  - name: agent1
    type: planner
workflow:
  - name: step1
    agent: undefined_agent
`,
			expectError: true,
			errorMsg:    "not found in agents list",
		},
		{
			name: "duplicate agent names",
			yaml: `
name: test
agents:
  - name: agent1
    type: planner
  - name: agent1
    type: executor
workflow:
  - name: step1
    agent: agent1
`,
			expectError: true,
			errorMsg:    "duplicate name",
		},
		{
			name: "duplicate step names",
			yaml: `
name: test
agents:
  - name: agent1
    type: planner
workflow:
  - name: step1
    agent: agent1
  - name: step1
    agent: agent1
`,
			expectError: true,
			errorMsg:    "duplicate name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseWorkflowConfig([]byte(tt.yaml))

			if tt.expectError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
				t.Logf("✅ Expected error: %v", err)
			} else {
				assert.NoError(t, err)
				t.Log("✅ Valid config parsed successfully")
			}
		})
	}
}

// TestIntegration_MultiAgentWorkflow 验证多 Agent 协作
func TestIntegration_MultiAgentWorkflow(t *testing.T) {
	yaml := `
name: multi_agent_collaboration
description: 4-Agent collaborative security testing

agents:
  - name: monitor
    type: monitor
    config:
      check_interval: 30

  - name: planner
    type: planner
    config:
      max_actions: 15
      strategy: "parallel"

  - name: executor
    type: executor
    config:
      pool_size: 10

  - name: evaluator
    type: evaluator
    config:
      confidence_threshold: 0.8

workflow:
  # Monitor 初始化任务
  - name: initialize_task
    agent: monitor
    input:
      mode: "active"
    output: task_info
    timeout: 30

  # Planner 生成执行计划
  - name: create_execution_plan
    agent: planner
    input:
      task: "{{ steps.initialize_task.output }}"
      constraints: "{{ context.constraints }}"
    output: execution_plan
    timeout: 120

  # Executor 执行测试
  - name: execute_tests
    agent: executor
    input:
      plan: "{{ steps.create_execution_plan.output }}"
    output: test_results
    parallel: true
    timeout: 600

  # Evaluator 评估结果
  - name: evaluate_results
    agent: evaluator
    input:
      results: "{{ steps.execute_tests.output }}"
    output: evaluation
    timeout: 60

  # Monitor 生成报告
  - name: generate_report
    agent: monitor
    input:
      evaluation: "{{ steps.evaluate_results.output }}"
    output: final_report
    timeout: 30
`

	config, err := ParseWorkflowConfig([]byte(yaml))
	require.NoError(t, err)

	// 验证配置
	assert.Equal(t, "multi_agent_collaboration", config.Name)
	assert.Len(t, config.Agents, 4)
	assert.Len(t, config.Workflow, 5)

	// 执行工作流
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

	// 验证步骤链
	expectedSteps := []string{
		"initialize_task",
		"create_execution_plan",
		"execute_tests",
		"evaluate_results",
		"generate_report",
	}

	for _, stepName := range expectedSteps {
		output, ok := runner.GetOutput(stepName)
		assert.True(t, ok, "step %s should have output", stepName)
		assert.NotNil(t, output)
	}

	// 清理
	err = runner.Cleanup(ctx)
	require.NoError(t, err)

	t.Log("✅ Multi-agent workflow executed successfully")
}

// TestIntegration_ConfigReload 验证配置热加载
func TestIntegration_ConfigReload(t *testing.T) {
	// 创建临时配置文件
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test_workflow.yaml")

	yaml1 := `
name: version_1
agents:
  - name: agent1
    type: planner
workflow:
  - name: step1
    agent: agent1
    output: result
`

	err := os.WriteFile(configPath, []byte(yaml1), 0644)
	require.NoError(t, err)

	// 加载第一版配置
	config1, err := LoadWorkflowConfig(configPath)
	require.NoError(t, err)
	assert.Equal(t, "version_1", config1.Name)
	assert.Len(t, config1.Workflow, 1)

	// 修改配置文件
	yaml2 := `
name: version_2
agents:
  - name: agent1
    type: planner
  - name: agent2
    type: executor
workflow:
  - name: step1
    agent: agent1
    output: result1
  - name: step2
    agent: agent2
    output: result2
`

	err = os.WriteFile(configPath, []byte(yaml2), 0644)
	require.NoError(t, err)

	// 重新加载配置
	config2, err := LoadWorkflowConfig(configPath)
	require.NoError(t, err)
	assert.Equal(t, "version_2", config2.Name)
	assert.Len(t, config2.Agents, 2)
	assert.Len(t, config2.Workflow, 2)

	t.Log("✅ Config reload successful")
}

// BenchmarkWorkflowExecution 性能基准测试
func BenchmarkWorkflowExecution(b *testing.B) {
	yaml := `
name: benchmark
agents:
  - name: agent1
    type: planner
workflow:
  - name: step1
    agent: agent1
    output: result
`

	config, err := ParseWorkflowConfig([]byte(yaml))
	if err != nil {
		b.Fatal(err)
	}

	factory := NewMockAgentFactory()
	logger := zerolog.Nop()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		runner, err := NewRunner(config, factory, WithLogger(logger))
		if err != nil {
			b.Fatal(err)
		}

		ctx := context.Background()
		if err := runner.Run(ctx); err != nil {
			b.Fatal(err)
		}

		_ = runner.Cleanup(ctx)
	}
}
