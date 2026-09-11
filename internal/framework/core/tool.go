package core

import (
	"context"
	"encoding/json"
)

// Tool 是工具的统一接口。
// 工具是原子级的能力单元，可被 Agent 调用。
type Tool interface {
	// Name 返回工具的唯一名称
	Name() string

	// Description 返回工具的描述（给 LLM 看）
	Description() string

	// Schema 返回工具的输入输出规范
	Schema() ToolSchema

	// Execute 执行工具
	Execute(ctx context.Context, input ToolInput) (ToolOutput, error)
}

// ToolSchema 是工具的规范定义。
type ToolSchema struct {
	// 输入参数的 JSON Schema
	InputSchema json.RawMessage `json:"input_schema"`

	// 输出结果的 JSON Schema
	OutputSchema json.RawMessage `json:"output_schema"`

	// 是否需要用户确认
	RequiresConfirmation bool `json:"requires_confirmation"`

	// 预估执行时间（毫秒）
	EstimatedDurationMs int64 `json:"estimated_duration_ms,omitempty"`
}

// ToolInput 是工具的输入。
type ToolInput struct {
	// 输入参数（JSON 对象）
	Arguments json.RawMessage `json:"arguments"`

	// 执行上下文（环境变量、配置等）
	Context map[string]any `json:"context,omitempty"`

	// 调用来源（哪个 Agent 调用）
	Caller string `json:"caller,omitempty"`
}

// ToolOutput 是工具的输出。
type ToolOutput struct {
	// 执行结果（JSON）
	Result json.RawMessage `json:"result"`

	// 错误信息（非 Go error，是业务错误）
	Error string `json:"error,omitempty"`

	// 元数据（执行时间、资源消耗等）
	Metadata map[string]any `json:"metadata,omitempty"`

	// 是否需要人工审核结果
	RequiresReview bool `json:"requires_review"`
}

// ToolCategory 是工具分类。
type ToolCategory string

const (
	CategoryFileSystem  ToolCategory = "filesystem"  // 文件系统
	CategoryNetwork     ToolCategory = "network"     // 网络
	CategoryDatabase    ToolCategory = "database"    // 数据库
	CategoryComputation ToolCategory = "computation" // 计算
	CategoryKnowledge   ToolCategory = "knowledge"   // 知识查询
	CategoryControl     ToolCategory = "control"     // 控制流
	CategoryCustom      ToolCategory = "custom"      // 自定义
)

// ToolMetadata 是工具的元信息。
type ToolMetadata struct {
	// 工具名称
	Name string `json:"name"`

	// 工具分类
	Category ToolCategory `json:"category"`

	// 版本
	Version string `json:"version"`

	// 作者
	Author string `json:"author,omitempty"`

	// 标签
	Tags []string `json:"tags,omitempty"`

	// 是否危险（需要审批）
	IsDangerous bool `json:"is_dangerous"`

	// 依赖的其他工具
	Dependencies []string `json:"dependencies,omitempty"`
}

// ToolRegistry 是工具注册中心。
type ToolRegistry interface {
	// Register 注册工具
	Register(tool Tool) error

	// Unregister 注销工具
	Unregister(name string) error

	// Get 获取工具
	Get(name string) (Tool, error)

	// List 列出所有工具
	List() []Tool

	// ListByCategory 按分类列出工具
	ListByCategory(category ToolCategory) []Tool

	// Exists 检查工具是否存在
	Exists(name string) bool

	// Execute 执行工具（带中间件）
	Execute(ctx context.Context, name string, input ToolInput) (ToolOutput, error)
}

// ToolMiddleware 是工具中间件。
type ToolMiddleware interface {
	// Before 在工具执行前调用
	Before(ctx context.Context, tool Tool, input ToolInput) (context.Context, ToolInput, error)

	// After 在工具执行后调用
	After(ctx context.Context, tool Tool, output ToolOutput) (ToolOutput, error)

	// OnError 在工具执行出错时调用
	OnError(ctx context.Context, tool Tool, err error) error
}

// ToolExecutor 是工具执行器（带中间件链）。
type ToolExecutor interface {
	// Execute 执行工具
	Execute(ctx context.Context, tool Tool, input ToolInput) (ToolOutput, error)

	// Use 添加中间件
	Use(middleware ToolMiddleware)
}

// ToolWrapper 包装普通函数为 Tool。
type ToolWrapper struct {
	name        string
	description string
	schema      ToolSchema
	handler     func(context.Context, ToolInput) (ToolOutput, error)
}

// NewToolWrapper 创建工具包装器。
func NewToolWrapper(name, description string, schema ToolSchema, handler func(context.Context, ToolInput) (ToolOutput, error)) *ToolWrapper {
	return &ToolWrapper{
		name:        name,
		description: description,
		schema:      schema,
		handler:     handler,
	}
}

// Name 实现 Tool 接口。
func (t *ToolWrapper) Name() string {
	return t.name
}

// Description 实现 Tool 接口。
func (t *ToolWrapper) Description() string {
	return t.description
}

// Schema 实现 Tool 接口。
func (t *ToolWrapper) Schema() ToolSchema {
	return t.schema
}

// Execute 实现 Tool 接口。
func (t *ToolWrapper) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	return t.handler(ctx, input)
}
