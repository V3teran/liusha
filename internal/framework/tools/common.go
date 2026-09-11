package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
)

// ============================================
// 通用工具集
// ============================================

// EchoTool 回显工具（测试用）。
type EchoTool struct{}

// Name 实现 Tool 接口。
func (t *EchoTool) Name() string {
	return "echo"
}

// Description 实现 Tool 接口。
func (t *EchoTool) Description() string {
	return "Echo the input back"
}

// Schema 实现 Tool 接口。
func (t *EchoTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		InputSchema:  json.RawMessage(`{"type": "object", "properties": {"message": {"type": "string"}}}`),
		OutputSchema: json.RawMessage(`{"type": "object", "properties": {"echo": {"type": "string"}}}`),
	}
}

// Execute 实现 Tool 接口。
func (t *EchoTool) Execute(ctx context.Context, input core.ToolInput) (core.ToolOutput, error) {
	var params map[string]any
	if err := json.Unmarshal(input.Arguments, &params); err != nil {
		return core.ToolOutput{Error: err.Error()}, err
	}

	message, ok := params["message"].(string)
	if !ok {
		return core.ToolOutput{Error: "message is required"}, fmt.Errorf("message is required")
	}

	result := map[string]any{"echo": message}
	resultJSON, _ := json.Marshal(result)

	return core.ToolOutput{Result: resultJSON}, nil
}

// SleepTool 延迟工具（测试用）。
type SleepTool struct{}

// Name 实现 Tool 接口。
func (t *SleepTool) Name() string {
	return "sleep"
}

// Description 实现 Tool 接口。
func (t *SleepTool) Description() string {
	return "Sleep for specified milliseconds"
}

// Schema 实现 Tool 接口。
func (t *SleepTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		InputSchema:  json.RawMessage(`{"type": "object", "properties": {"ms": {"type": "integer"}}}`),
		OutputSchema: json.RawMessage(`{"type": "object", "properties": {"slept_ms": {"type": "integer"}}}`),
	}
}

// Execute 实现 Tool 接口。
func (t *SleepTool) Execute(ctx context.Context, input core.ToolInput) (core.ToolOutput, error) {
	var params map[string]any
	if err := json.Unmarshal(input.Arguments, &params); err != nil {
		return core.ToolOutput{Error: err.Error()}, err
	}

	ms, ok := params["ms"].(float64)
	if !ok {
		return core.ToolOutput{Error: "ms is required"}, fmt.Errorf("ms is required")
	}

	select {
	case <-time.After(time.Duration(ms) * time.Millisecond):
		result := map[string]any{"slept_ms": int(ms)}
		resultJSON, _ := json.Marshal(result)
		return core.ToolOutput{Result: resultJSON}, nil
	case <-ctx.Done():
		return core.ToolOutput{Error: "canceled"}, ctx.Err()
	}
}

// TransformTool 转换工具（通用数据转换）。
type TransformTool struct {
	transformer func(any) (any, error)
}

// NewTransformTool 创建转换工具。
func NewTransformTool(transformer func(any) (any, error)) *TransformTool {
	return &TransformTool{transformer: transformer}
}

// Name 实现 Tool 接口。
func (t *TransformTool) Name() string {
	return "transform"
}

// Description 实现 Tool 接口。
func (t *TransformTool) Description() string {
	return "Transform data using custom function"
}

// Schema 实现 Tool 接口。
func (t *TransformTool) Schema() core.ToolSchema {
	return core.ToolSchema{
		InputSchema:  json.RawMessage(`{"type": "object"}`),
		OutputSchema: json.RawMessage(`{"type": "object"}`),
	}
}

// Execute 实现 Tool 接口。
func (t *TransformTool) Execute(ctx context.Context, input core.ToolInput) (core.ToolOutput, error) {
	var data any
	if err := json.Unmarshal(input.Arguments, &data); err != nil {
		return core.ToolOutput{Error: err.Error()}, err
	}

	result, err := t.transformer(data)
	if err != nil {
		return core.ToolOutput{Error: err.Error()}, err
	}

	resultJSON, _ := json.Marshal(result)
	return core.ToolOutput{Result: resultJSON}, nil
}

// ============================================
// 工具注册助手
// ============================================

// RegisterCommonTools 注册常用工具。
func RegisterCommonTools(registry *core.RegistryImpl) error {
	tools := []core.Tool{
		&EchoTool{},
		&SleepTool{},
	}

	for _, tool := range tools {
		if err := registry.RegisterTool(tool); err != nil {
			return fmt.Errorf("register tool %s: %w", tool.Name(), err)
		}
	}

	return nil
}
