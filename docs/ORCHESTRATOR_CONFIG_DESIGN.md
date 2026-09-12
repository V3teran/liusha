# Workflow Configuration Design
# Phase 10C-PoC: Orchestrator 可配置性验证

## 设计原则

1. **声明式**：描述"做什么"，不是"怎么做"
2. **简洁**：常见场景配置简单
3. **可扩展**：支持复杂编排（并发、条件、循环）
4. **类型安全**：配置可验证

## 核心概念

### Agent（执行单元）
- 每个 Agent 有唯一名称
- 每个 Agent 有类型（planner/executor/monitor/evaluator）
- Agent 配置独立

### Step（执行步骤）
- 每个 Step 调用一个 Agent
- Step 可以有输入/输出
- Step 可以有条件执行
- Step 可以并发执行

### Workflow（工作流）
- 由多个 Step 组成
- Step 之间可以传递数据
- 支持条件分支、循环、并发

## 配置格式（YAML）

### 基础版本（PoC 验证）

```yaml
# 工作流名称
name: security_scan

# Agent 定义
agents:
  - name: planner
    type: planner
    config:
      max_actions: 10
      
  - name: executor_pool
    type: executor
    config:
      pool_size: 5
      
  - name: evaluator
    type: evaluator

# 工作流定义
workflow:
  # Step 1: 生成计划
  - name: generate_plan
    agent: planner
    input:
      task_id: "{{ context.task_id }}"
      objective: "{{ context.objective }}"
    output: plan
    
  # Step 2: 执行 Actions（并发）
  - name: execute_actions
    agent: executor_pool
    input:
      actions: "{{ steps.generate_plan.output.actions }}"
    output: observations
    parallel: true
    
  # Step 3: 验证结果
  - name: verify_observations
    agent: evaluator
    input:
      observations: "{{ steps.execute_actions.output }}"
    output: results
    
  # Step 4: 检查是否完成
  - name: check_completion
    agent: planner
    input:
      results: "{{ steps.verify_observations.output }}"
    condition: "{{ steps.verify_observations.output.has_findings }}"
```

### 完整版本（未来扩展）

```yaml
name: advanced_security_scan

agents:
  - name: planner
    type: planner
    config:
      max_actions: 10
      
  - name: executor_pool
    type: executor
    config:
      pool_size: 5
      timeout: 300s
      retry:
        max_attempts: 3
        backoff: exponential

workflow:
  - name: generate_plan
    agent: planner
    input:
      task_id: "{{ context.task_id }}"
    output: plan
    
  # 并发执行
  - name: execute_actions
    parallel:
      - name: execute_web
        agent: executor_pool
        input:
          actions: "{{ steps.generate_plan.output.web_actions }}"
        
      - name: execute_api
        agent: executor_pool
        input:
          actions: "{{ steps.generate_plan.output.api_actions }}"
    
  # 条件分支
  - name: verify
    agent: evaluator
    condition: "{{ steps.execute_actions.success }}"
    
  # 循环（直到满足条件）
  - name: replan_loop
    loop:
      condition: "{{ not steps.verify.output.complete }}"
      max_iterations: 3
      steps:
        - name: replan
          agent: planner
        - name: execute
          agent: executor_pool
```

## 配置结构（Go）

```go
// WorkflowConfig 是工作流配置
type WorkflowConfig struct {
    Name     string        `yaml:"name"`
    Agents   []AgentConfig `yaml:"agents"`
    Workflow []StepConfig  `yaml:"workflow"`
}

// AgentConfig 是 Agent 配置
type AgentConfig struct {
    Name   string                 `yaml:"name"`
    Type   string                 `yaml:"type"`
    Config map[string]interface{} `yaml:"config,omitempty"`
}

// StepConfig 是步骤配置
type StepConfig struct {
    Name      string                 `yaml:"name"`
    Agent     string                 `yaml:"agent,omitempty"`
    Input     map[string]interface{} `yaml:"input,omitempty"`
    Output    string                 `yaml:"output,omitempty"`
    Condition string                 `yaml:"condition,omitempty"`
    Parallel  bool                   `yaml:"parallel,omitempty"`
}
```

## 模板语法

使用 Go template 语法：
- `{{ context.task_id }}` - 上下文变量
- `{{ steps.generate_plan.output.actions }}` - 前置步骤输出
- `{{ steps.verify.output.has_findings }}` - 条件判断

## PoC 范围

Phase 10C-PoC 只实现基础版本：
1. ✅ Agent 定义
2. ✅ Step 顺序执行
3. ✅ 简单输入/输出
4. ❌ 不实现：条件分支、循环、并发（未来扩展）

## 验证标准

PoC 成功的标准：
1. 配置文件可以解析
2. 可以根据配置创建 Agents
3. 可以按配置顺序执行 Steps
4. Steps 之间可以传递数据
5. 配置比硬编码更灵活（主观评估）
