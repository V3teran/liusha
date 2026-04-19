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

// ffufWordlists 将用户可选的 wordlist 名称映射到容器内真实路径。
// 禁止用户直接传任意路径，防止通过 ffuf 读取容器内任意文件。
var ffufWordlists = map[string]string{
	"common":     "/usr/share/wordlists/dirb/common.txt",
	"big":        "/usr/share/wordlists/dirb/big.txt",
	"raft-small": "/usr/share/wordlists/seclists/raft-small-words.txt",
}

// ResolveFfufWordlist 根据枚举名称返回容器内字典文件路径；
// 空字符串视为默认 "common"；未知名称返回错误。
func ResolveFfufWordlist(name string) (string, error) {
	if name == "" {
		name = "common"
	}
	path, ok := ffufWordlists[name]
	if !ok {
		return "", fmt.Errorf("未知 wordlist_name %q；允许: common/big/raft-small", name)
	}
	return path, nil
}

// FfufWordlistNames 返回允许的 wordlist 枚举值（供 schema 构造）。
func FfufWordlistNames() []string {
	return []string{"common", "big", "raft-small"}
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
