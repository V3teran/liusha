package mcpserver_test

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/mcpserver"
	"github.com/V3teran/liusha/internal/scope"
)

func TestScopeGuard_BlocksOutOfScope(t *testing.T) {
	p, _ := scope.NewPolicy(scope.Rules{
		Domains: []string{"allowed.com"},
	})
	inner := mcpserver.ToolFunc(func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("reached"), nil
	})
	guarded := mcpserver.WithScopeGuard(p, inner)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"target": "http://evil.com/path"}
	_, err := guarded.Call(context.Background(), req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out-of-scope")
}

func TestScopeGuard_AllowsInScope(t *testing.T) {
	p, _ := scope.NewPolicy(scope.Rules{
		Domains: []string{"allowed.com"},
	})
	inner := mcpserver.ToolFunc(func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("reached"), nil
	})
	guarded := mcpserver.WithScopeGuard(p, inner)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"target": "http://allowed.com/scan"}
	res, err := guarded.Call(context.Background(), req)
	require.NoError(t, err)
	assert.NotNil(t, res)
}
