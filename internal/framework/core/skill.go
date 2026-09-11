package core

import (
	"context"
	"encoding/json"
)

// Skill 是技能的统一接口。
// 技能是高级能力单元，可以组合多个 Tool，实现复杂任务。
type Skill interface {
	// Name 返回技能的唯一名称
	Name() string

	// Description 返回技能的描述
	Description() string

	// Version 返回技能的版本
	Version() string

	// Execute 执行技能
	Execute(ctx context.Context, input SkillInput) (SkillOutput, error)
}

// SkillInput 是技能的输入。
type SkillInput struct {
	// 任务描述
	Task string `json:"task"`

	// 参数
	Parameters map[string]any `json:"parameters,omitempty"`

	// 上下文
	Context map[string]any `json:"context,omitempty"`
}

// SkillOutput 是技能的输出。
type SkillOutput struct {
	// 执行结果
	Result any `json:"result"`

	// 执行步骤（用于调试和审计）
	Steps []SkillStep `json:"steps,omitempty"`

	// 元数据
	Metadata map[string]any `json:"metadata,omitempty"`

	// 是否成功
	Success bool `json:"success"`

	// 错误信息
	Error string `json:"error,omitempty"`
}

// SkillStep 是技能的执行步骤。
type SkillStep struct {
	// 步骤序号
	Index int `json:"index"`

	// 步骤名称
	Name string `json:"name"`

	// 使用的工具
	Tool string `json:"tool,omitempty"`

	// 输入
	Input json.RawMessage `json:"input,omitempty"`

	// 输出
	Output json.RawMessage `json:"output,omitempty"`

	// 耗时（毫秒）
	DurationMs int64 `json:"duration_ms"`

	// 是否成功
	Success bool `json:"success"`

	// 错误信息
	Error string `json:"error,omitempty"`
}

// SkillMetadata 是技能的元信息。
type SkillMetadata struct {
	// 技能名称
	Name string `json:"name"`

	// 版本
	Version string `json:"version"`

	// 作者
	Author string `json:"author,omitempty"`

	// 描述
	Description string `json:"description"`

	// 标签
	Tags []string `json:"tags,omitempty"`

	// 依赖的工具
	RequiredTools []string `json:"required_tools,omitempty"`

	// 依赖的技能
	RequiredSkills []string `json:"required_skills,omitempty"`

	// 预估执行时间（毫秒）
	EstimatedDurationMs int64 `json:"estimated_duration_ms,omitempty"`
}

// SkillRegistry 是技能注册中心。
type SkillRegistry interface {
	// Register 注册技能
	Register(skill Skill) error

	// Unregister 注销技能
	Unregister(name string) error

	// Get 获取技能
	Get(name string) (Skill, error)

	// List 列出所有技能
	List() []Skill

	// Exists 检查技能是否存在
	Exists(name string) bool

	// Execute 执行技能
	Execute(ctx context.Context, name string, input SkillInput) (SkillOutput, error)
}

// SkillBuilder 是技能构建器。
type SkillBuilder interface {
	// SetMetadata 设置元信息
	SetMetadata(metadata SkillMetadata) SkillBuilder

	// AddStep 添加执行步骤
	AddStep(name string, handler func(context.Context, map[string]any) (any, error)) SkillBuilder

	// AddToolCall 添加工具调用步骤
	AddToolCall(toolName string, prepareInput func(map[string]any) ToolInput) SkillBuilder

	// Build 构建技能
	Build() (Skill, error)
}

// SkillWrapper 包装函数为 Skill。
type SkillWrapper struct {
	metadata SkillMetadata
	handler  func(context.Context, SkillInput) (SkillOutput, error)
}

// NewSkillWrapper 创建技能包装器。
func NewSkillWrapper(metadata SkillMetadata, handler func(context.Context, SkillInput) (SkillOutput, error)) *SkillWrapper {
	return &SkillWrapper{
		metadata: metadata,
		handler:  handler,
	}
}

// Name 实现 Skill 接口。
func (s *SkillWrapper) Name() string {
	return s.metadata.Name
}

// Description 实现 Skill 接口。
func (s *SkillWrapper) Description() string {
	return s.metadata.Description
}

// Version 实现 Skill 接口。
func (s *SkillWrapper) Version() string {
	return s.metadata.Version
}

// Execute 实现 Skill 接口。
func (s *SkillWrapper) Execute(ctx context.Context, input SkillInput) (SkillOutput, error) {
	return s.handler(ctx, input)
}

// SkillLoader 从文件加载技能。
type SkillLoader interface {
	// Load 加载技能文件
	Load(path string) (Skill, error)

	// LoadAll 加载目录下所有技能
	LoadAll(dir string) ([]Skill, error)

	// Reload 重新加载技能
	Reload(name string) error
}

// SkillDefinition 是技能的文件定义（YAML/JSON）。
type SkillDefinition struct {
	// 元信息
	Metadata SkillMetadata `json:"metadata"`

	// 执行步骤
	Steps []StepDefinition `json:"steps"`
}

// StepDefinition 是步骤定义。
type StepDefinition struct {
	// 步骤名称
	Name string `json:"name"`

	// 工具名称
	Tool string `json:"tool,omitempty"`

	// 输入参数映射
	InputMapping map[string]string `json:"input_mapping,omitempty"`

	// 输出变量名
	OutputVar string `json:"output_var,omitempty"`

	// 条件执行（表达式）
	Condition string `json:"condition,omitempty"`
}
