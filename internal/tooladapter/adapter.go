// Package tooladapter 提供工具适配器，将 registry.Tool 适配为 core.Tool
package tooladapter

import (
	"context"
	"encoding/json"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/registry"
)

// LegacyTool 是旧的工具接口
type LegacyTool interface {
	Name() string
	Desc() string
	Schema() json.RawMessage
	Execute(ctx context.Context, argsJSON json.RawMessage) (registry.ToolResult, error)
}

// Adapter 将 registry.Tool 适配为 core.Tool
type Adapter struct {
	legacy LegacyTool
}

// NewAdapter 创建工具适配器
func NewAdapter(legacy LegacyTool) *Adapter {
	return &Adapter{legacy: legacy}
}

// Name 实现 core.Tool 接口
func (a *Adapter) Name() string {
	return a.legacy.Name()
}

// Description 实现 core.Tool 接口
func (a *Adapter) Description() string {
	return a.legacy.Desc()
}

// Schema 实现 core.Tool 接口
func (a *Adapter) Schema() core.ToolSchema {
	return core.ToolSchema{
		InputSchema: a.legacy.Schema(),
	}
}

// Execute 实现 core.Tool 接口
func (a *Adapter) Execute(ctx context.Context, input core.ToolInput) (core.ToolOutput, error) {
	// 调用旧工具
	result, err := a.legacy.Execute(ctx, input.Arguments)
	if err != nil {
		return core.ToolOutput{}, err
	}

	// 转换结果
	if result.Error != "" {
		return core.ToolOutput{Error: result.Error}, nil
	}

	// 序列化输出
	resultJSON, err := json.Marshal(result.Output)
	if err != nil {
		return core.ToolOutput{Error: "failed to marshal output"}, nil
	}

	return core.ToolOutput{Result: resultJSON}, nil
}
