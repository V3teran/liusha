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

func testPolicy(t *testing.T) *scope.Policy {
	t.Helper()
	p, err := scope.NewPolicy(scope.Rules{
		Domains: []string{"example.com"},
	})
	require.NoError(t, err)
	return p
}

func TestRegisterTool_DuplicateErrors(t *testing.T) {
	s, _ := mcpserver.New(testPolicy(t))
	echo := mcpserver.ToolFunc(func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	require.NoError(t, s.RegisterTool("echo", "描述", nil, echo))
	err := s.RegisterTool("echo", "描述", nil, echo)
	require.Error(t, err)
}

func TestRegisterTool_CallOK(t *testing.T) {
	s, _ := mcpserver.New(testPolicy(t))
	called := false
	h := mcpserver.ToolFunc(func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called = true
		return mcp.NewToolResultText("pong"), nil
	})
	require.NoError(t, s.RegisterTool("ping", "测试工具", nil, h))
	res, err := s.Call(context.Background(), "ping", map[string]any{})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, res, "pong")
}
