package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/orchestrator"
	"github.com/V3teran/liusha/internal/framework/registry"
)

// 这个示例演示如何在业务层注册 Agent 到 Framework

// ============================================
// 业务层 Agent 实现
// ============================================

// BusinessPlanner 是业务层的 Planner Agent
type BusinessPlanner struct {
	name   string
	config map[string]interface{}
}

func NewBusinessPlanner(name string, config map[string]interface{}) *BusinessPlanner {
	return &BusinessPlanner{
		name:   name,
		config: config,
	}
}

func (p *BusinessPlanner) Name() string { return p.name }

func (p *BusinessPlanner) Run(ctx context.Context) error {
	fmt.Printf("[Planner %s] Running with config: %+v\n", p.name, p.config)
	<-ctx.Done()
	return ctx.Err()
}

func (p *BusinessPlanner) Stop(ctx context.Context) error {
	fmt.Printf("[Planner %s] Stopping\n", p.name)
	return nil
}

func (p *BusinessPlanner) ExportState() (json.RawMessage, error) {
	return json.Marshal(map[string]interface{}{
		"name":   p.name,
		"config": p.config,
	})
}

func (p *BusinessPlanner) ImportState(data json.RawMessage) error {
	return nil
}

// BusinessExecutor 是业务层的 Executor Agent
type BusinessExecutor struct {
	name   string
	config map[string]interface{}
}

func NewBusinessExecutor(name string, config map[string]interface{}) *BusinessExecutor {
	return &BusinessExecutor{
		name:   name,
		config: config,
	}
}

func (e *BusinessExecutor) Name() string { return e.name }

func (e *BusinessExecutor) Run(ctx context.Context) error {
	fmt.Printf("[Executor %s] Running with config: %+v\n", e.name, e.config)
	<-ctx.Done()
	return ctx.Err()
}

func (e *BusinessExecutor) Stop(ctx context.Context) error {
	fmt.Printf("[Executor %s] Stopping\n", e.name)
	return nil
}

func (e *BusinessExecutor) ExportState() (json.RawMessage, error) {
	return json.Marshal(map[string]interface{}{
		"name":   e.name,
		"config": e.config,
	})
}

func (e *BusinessExecutor) ImportState(data json.RawMessage) error {
	return nil
}

// ============================================
// 注册函数：业务层在 init() 中注册
// ============================================

func init() {
	// 注册 Planner
	registry.MustRegister("planner", func(config orchestrator.AgentConfig) (core.Agent, error) {
		fmt.Printf("Creating Planner: %s\n", config.Name)
		return NewBusinessPlanner(config.Name, config.Config), nil
	})

	// 注册 Executor
	registry.MustRegister("executor", func(config orchestrator.AgentConfig) (core.Agent, error) {
		fmt.Printf("Creating Executor: %s\n", config.Name)
		return NewBusinessExecutor(config.Name, config.Config), nil
	})

	fmt.Println("✅ Business Agents registered to Framework")
}

// ============================================
// 主程序：加载配置并执行
// ============================================

func main() {
	fmt.Println("\n=== Agent Registration Demo ===\n")

	// 1. 显示已注册的 Agent 类型
	types := registry.RegisteredTypes()
	fmt.Printf("Registered Agent Types: %v\n\n", types)

	// 2. 解析工作流配置
	yaml := `
name: demo_workflow
description: Demo workflow using registered agents

agents:
  - name: my_planner
    type: planner
    config:
      max_actions: 10
      strategy: "sequential"

  - name: my_executor
    type: executor
    config:
      pool_size: 5
      timeout: 300

workflow:
  - name: plan_step
    agent: my_planner
    input:
      task: "scan target"
    output: plan

  - name: execute_step
    agent: my_executor
    input:
      plan: "{{ steps.plan_step.output }}"
    output: result
`

	config, err := orchestrator.ParseWorkflowConfig([]byte(yaml))
	if err != nil {
		log.Fatalf("Parse config failed: %v", err)
	}

	fmt.Printf("Loaded workflow: %s\n\n", config.Name)

	// 3. 使用全局注册表作为 AgentFactory
	runner, err := orchestrator.NewRunner(config, registry.Global())
	if err != nil {
		log.Fatalf("Create runner failed: %v", err)
	}

	// 4. 执行工作流
	ctx := context.Background()
	if err := runner.Run(ctx); err != nil {
		log.Fatalf("Run workflow failed: %v", err)
	}

	// 5. 输出结果
	fmt.Println("\n=== Workflow Execution Results ===\n")
	outputs := runner.GetAllOutputs()
	for stepName, output := range outputs {
		fmt.Printf("Step %q output: %+v\n", stepName, output)
	}

	// 6. 清理
	if err := runner.Cleanup(ctx); err != nil {
		log.Fatalf("Cleanup failed: %v", err)
	}

	fmt.Println("\n✅ Demo completed successfully!")
}
