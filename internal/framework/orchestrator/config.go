package orchestrator

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// WorkflowConfig 是工作流配置的顶层结构
type WorkflowConfig struct {
	// Name 是工作流名称
	Name string `yaml:"name"`

	// Description 是工作流描述（可选）
	Description string `yaml:"description,omitempty"`

	// Agents 是 Agent 定义列表
	Agents []AgentConfig `yaml:"agents"`

	// Workflow 是执行步骤列表
	Workflow []StepConfig `yaml:"workflow"`
}

// AgentConfig 是单个 Agent 的配置
type AgentConfig struct {
	// Name 是 Agent 唯一名称
	Name string `yaml:"name"`

	// Type 是 Agent 类型（planner/executor/monitor/evaluator）
	Type string `yaml:"type"`

	// Config 是 Agent 特定配置
	Config map[string]interface{} `yaml:"config,omitempty"`
}

// StepConfig 是单个执行步骤的配置
type StepConfig struct {
	// Name 是步骤名称
	Name string `yaml:"name"`

	// Agent 是执行该步骤的 Agent 名称
	Agent string `yaml:"agent"`

	// Input 是步骤输入（支持模板）
	Input map[string]interface{} `yaml:"input,omitempty"`

	// Output 是步骤输出变量名
	Output string `yaml:"output,omitempty"`

	// Condition 是执行条件（可选，模板表达式）
	Condition string `yaml:"condition,omitempty"`

	// Parallel 表示是否并发执行
	Parallel bool `yaml:"parallel,omitempty"`

	// Timeout 是步骤超时时间（秒）
	Timeout int `yaml:"timeout,omitempty"`
}

// LoadWorkflowConfig 从文件加载工作流配置
func LoadWorkflowConfig(path string) (*WorkflowConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read workflow config: %w", err)
	}

	var config WorkflowConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse workflow config: %w", err)
	}

	// 验证配置
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("validate workflow config: %w", err)
	}

	return &config, nil
}

// ParseWorkflowConfig 从 YAML 字符串解析配置
func ParseWorkflowConfig(yamlData []byte) (*WorkflowConfig, error) {
	var config WorkflowConfig
	if err := yaml.Unmarshal(yamlData, &config); err != nil {
		return nil, fmt.Errorf("parse workflow config: %w", err)
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("validate workflow config: %w", err)
	}

	return &config, nil
}

// Validate 验证配置的正确性
func (c *WorkflowConfig) Validate() error {
	// 验证名称
	if c.Name == "" {
		return fmt.Errorf("workflow name is required")
	}

	// 验证 Agents
	if len(c.Agents) == 0 {
		return fmt.Errorf("at least one agent is required")
	}

	agentNames := make(map[string]bool)
	for i, agent := range c.Agents {
		if agent.Name == "" {
			return fmt.Errorf("agent[%d]: name is required", i)
		}

		if agent.Type == "" {
			return fmt.Errorf("agent[%d]: type is required", i)
		}

		if agentNames[agent.Name] {
			return fmt.Errorf("agent[%d]: duplicate name %q", i, agent.Name)
		}
		agentNames[agent.Name] = true

		// 验证 Agent 类型
		validTypes := map[string]bool{
			"planner":   true,
			"executor":  true,
			"monitor":   true,
			"evaluator": true,
		}
		if !validTypes[agent.Type] {
			return fmt.Errorf("agent[%d]: invalid type %q (must be one of: planner, executor, monitor, evaluator)", i, agent.Type)
		}
	}

	// 验证 Workflow
	if len(c.Workflow) == 0 {
		return fmt.Errorf("at least one workflow step is required")
	}

	stepNames := make(map[string]bool)
	for i, step := range c.Workflow {
		if step.Name == "" {
			return fmt.Errorf("workflow[%d]: name is required", i)
		}

		if step.Agent == "" {
			return fmt.Errorf("workflow[%d]: agent is required", i)
		}

		if stepNames[step.Name] {
			return fmt.Errorf("workflow[%d]: duplicate name %q", i, step.Name)
		}
		stepNames[step.Name] = true

		// 验证 Agent 引用
		if !agentNames[step.Agent] {
			return fmt.Errorf("workflow[%d]: agent %q not found in agents list", i, step.Agent)
		}
	}

	return nil
}

// GetAgent 根据名称获取 Agent 配置
func (c *WorkflowConfig) GetAgent(name string) (*AgentConfig, error) {
	for _, agent := range c.Agents {
		if agent.Name == name {
			return &agent, nil
		}
	}
	return nil, fmt.Errorf("agent %q not found", name)
}

// GetStep 根据名称获取步骤配置
func (c *WorkflowConfig) GetStep(name string) (*StepConfig, error) {
	for _, step := range c.Workflow {
		if step.Name == name {
			return &step, nil
		}
	}
	return nil, fmt.Errorf("step %q not found", name)
}

// ToYAML 将配置序列化为 YAML
func (c *WorkflowConfig) ToYAML() ([]byte, error) {
	return yaml.Marshal(c)
}

// AgentTypes 返回所有支持的 Agent 类型
func AgentTypes() []string {
	return []string{"planner", "executor", "monitor", "evaluator"}
}

// DefaultTimeout 返回默认超时时间（秒）
func DefaultTimeout() int {
	return 300 // 5 分钟
}
