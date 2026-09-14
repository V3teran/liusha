package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// Tool 工具接口
type Tool interface {
	// Name 工具名称
	Name() string

	// Description 工具描述
	Description() string

	// Parameters 参数 Schema（JSON Schema）
	Parameters() json.RawMessage

	// Execute 执行工具
	Execute(ctx context.Context, input string) (string, error)
}

// ToolRegistry 工具注册表
type ToolRegistry struct {
	tools map[string]Tool
}

// NewToolRegistry 创建工具注册表
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

// Register 注册工具
func (r *ToolRegistry) Register(tool Tool) error {
	name := tool.Name()
	if name == "" {
		return fmt.Errorf("工具名称不能为空")
	}

	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("工具 %q 已存在", name)
	}

	r.tools[name] = tool
	return nil
}

// Get 获取工具
func (r *ToolRegistry) Get(name string) (Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// List 列出所有工具
func (r *ToolRegistry) List() []Tool {
	tools := make([]Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, tool)
	}
	return tools
}

// Execute 执行工具
func (r *ToolRegistry) Execute(ctx context.Context, name string, input string) (string, error) {
	tool, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("工具 %q 不存在", name)
	}

	return tool.Execute(ctx, input)
}

// ─────────────────────────────────────────────
//  函数工具
// ─────────────────────────────────────────────

// FuncTool 函数式工具
type FuncTool struct {
	name        string
	description string
	parameters  json.RawMessage
	fn          func(ctx context.Context, input string) (string, error)
}

// NewFuncTool 创建函数工具
func NewFuncTool(
	name string,
	description string,
	parameters json.RawMessage,
	fn func(ctx context.Context, input string) (string, error),
) *FuncTool {
	return &FuncTool{
		name:        name,
		description: description,
		parameters:  parameters,
		fn:          fn,
	}
}

// Name 工具名称
func (t *FuncTool) Name() string {
	return t.name
}

// Description 工具描述
func (t *FuncTool) Description() string {
	return t.description
}

// Parameters 参数 Schema
func (t *FuncTool) Parameters() json.RawMessage {
	return t.parameters
}

// Execute 执行工具
func (t *FuncTool) Execute(ctx context.Context, input string) (string, error) {
	return t.fn(ctx, input)
}

// ─────────────────────────────────────────────
//  工具执行结果
// ─────────────────────────────────────────────

// ToolResult 工具执行结果
type ToolResult struct {
	Success bool   `json:"success"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

// ToJSON 转换为 JSON
func (r *ToolResult) ToJSON() string {
	data, _ := json.Marshal(r)
	return string(data)
}

// NewSuccessResult 创建成功结果
func NewSuccessResult(output string) *ToolResult {
	return &ToolResult{
		Success: true,
		Output:  output,
	}
}

// NewErrorResult 创建错误结果
func NewErrorResult(err error) *ToolResult {
	return &ToolResult{
		Success: false,
		Error:   err.Error(),
	}
}
