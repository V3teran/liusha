package executor

import (
	"context"
	"encoding/json"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/registry"
)

// Registry 是 executor 包的工具注册表适配器
type Registry struct {
	inner *registry.Registry
}

// NewRegistry 创建 Registry 适配器
func NewRegistry(inner *registry.Registry) *Registry {
	return &Registry{inner: inner}
}

// Tools 返回所有注册的工具（作为 core.Tool 接口）
func (r *Registry) Tools() []core.Tool {
	schemas := r.inner.Schemas()
	tools := make([]core.Tool, 0, len(schemas))

	for _, schema := range schemas {
		if tool, ok := r.inner.Get(schema.Name); ok {
			tools = append(tools, &toolAdapter{
				inner: tool,
			})
		}
	}

	return tools
}

// toolAdapter 将 registry.Tool 适配为 core.Tool
type toolAdapter struct {
	inner registry.Tool
}

func (t *toolAdapter) Name() string {
	return t.inner.Name()
}

func (t *toolAdapter) Description() string {
	return t.inner.Desc()
}

func (t *toolAdapter) Schema() core.ToolSchema {
	innerSchema := t.inner.Schema()
	schemaJSON, _ := json.Marshal(innerSchema)
	return core.ToolSchema{
		InputSchema: schemaJSON,
	}
}

func (t *toolAdapter) Execute(ctx context.Context, input core.ToolInput) (core.ToolOutput, error) {
	// 调用 registry.Tool 的 Execute
	result, err := t.inner.Execute(ctx, input.Arguments)
	if err != nil {
		return core.ToolOutput{
			Error: err.Error(),
		}, nil
	}

	// 转换为 core.ToolOutput
	outputJSON, _ := json.Marshal(result.Output)
	return core.ToolOutput{
		Result: outputJSON,
		Error:  result.Error,
	}, nil
}
