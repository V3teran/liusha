package mcpserver

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/scope"
	"github.com/mark3labs/mcp-go/mcp"
)

type scopeGuard struct {
	policy *scope.Policy
	inner  ToolHandler
}

// WithScopeGuard 包装 ToolHandler，调用前从 target 参数校验 scope。
func WithScopeGuard(policy *scope.Policy, inner ToolHandler) ToolHandler {
	return &scopeGuard{policy: policy, inner: inner}
}

func (g *scopeGuard) Call(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	target, _ := args["target"].(string)
	if target == "" {
		return nil, fmt.Errorf("工具调用缺少 target 参数")
	}
	if !g.policy.InScope("GET", target) {
		return nil, fmt.Errorf("out-of-scope 目标: %s", target)
	}
	return g.inner.Call(ctx, req)
}
