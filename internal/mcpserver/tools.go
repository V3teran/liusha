package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// ToolHandler 是所有 MCP 工具的统一接口。
type ToolHandler interface {
	Call(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

// ToolFunc 允许用普通函数实现 ToolHandler。
type ToolFunc func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)

func (f ToolFunc) Call(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return f(ctx, req)
}

// RegisterTool 向 MCP server 注册一个工具；同名重复注册返回错误。
// 内部会强制用 ScopeGuard 包裹 handler，调用方即使忘记手动包装也不会绕过 scope 校验。
func (s *Server) RegisterTool(name, description string, schema any, h ToolHandler) error {
	if _, exists := s.tools[name]; exists {
		return fmt.Errorf("工具 %q 已注册", name)
	}
	if s.policy == nil {
		return fmt.Errorf("注册工具 %q 失败: scope policy 未配置", name)
	}
	// 强制 scope 守护：即便传入的已是 scopeGuard，再包一层也只是重复校验，语义不变。
	guarded := WithScopeGuard(s.policy, h)
	s.tools[name] = guarded

	var rawSchema json.RawMessage
	if schema != nil {
		b, _ := json.Marshal(schema)
		rawSchema = b
	}
	tool := mcp.NewToolWithRawSchema(name, description, rawSchema)
	s.mcp.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return guarded.Call(ctx, req)
	})
	return nil
}

// Call 直接调用已注册的工具并返回文本内容（供测试使用）。
func (s *Server) Call(ctx context.Context, name string, args map[string]any) (string, error) {
	h, ok := s.tools[name]
	if !ok {
		return "", fmt.Errorf("工具 %q 未注册", name)
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := h.Call(ctx, req)
	if err != nil {
		return "", err
	}
	if len(res.Content) == 0 {
		return "", errors.New("工具返回空内容")
	}
	if t, ok := res.Content[0].(mcp.TextContent); ok {
		return t.Text, nil
	}
	return fmt.Sprintf("%v", res.Content[0]), nil
}
