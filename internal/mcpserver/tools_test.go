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
	// 合法 target（example.com）应能通过强制 scope 校验。
	require.NoError(t, s.RegisterTool("ping", "测试工具", nil, h))
	res, err := s.Call(context.Background(), "ping", map[string]any{"target": "http://example.com/x"})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Contains(t, res, "pong")
}

// TestRegisterTool_AlwaysWrapsScopeGuard 验证 RegisterTool 内部强制用 ScopeGuard 包装 handler，
// 即使调用方忘记手动 WithScopeGuard，也能阻止 out-of-scope 目标调到 handler。
func TestRegisterTool_AlwaysWrapsScopeGuard(t *testing.T) {
	s, _ := mcpserver.New(testPolicy(t))
	handlerInvoked := false
	inner := mcpserver.ToolFunc(func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		handlerInvoked = true
		return mcp.NewToolResultText("reached"), nil
	})
	// 注意：这里没有手动 WithScopeGuard，依赖 RegisterTool 内部强制包装。
	require.NoError(t, s.RegisterTool("demo", "描述", nil, inner))

	_, err := s.Call(context.Background(), "demo", map[string]any{"target": "http://evil.com/path"})
	require.Error(t, err, "out-of-scope 目标应被拒绝")
	assert.False(t, handlerInvoked, "handler 不应被调用")
}

// TestResolveFfufWordlist_RejectsCustomPath 验证任意自由路径被拒绝，
// 防止通过 wordlist 参数读取容器内任意文件。
func TestResolveFfufWordlist_RejectsCustomPath(t *testing.T) {
	cases := []string{
		"/etc/shadow",
		"/etc/passwd",
		"../../../etc/hosts",
		"custom",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := mcpserver.ResolveFfufWordlist(name)
			require.Error(t, err, "%q 应被拒绝", name)
		})
	}
}

// TestResolveFfufWordlist_AcceptsWhitelistedName 验证白名单枚举能解析到预期路径。
func TestResolveFfufWordlist_AcceptsWhitelistedName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"common", "/usr/share/wordlists/dirb/common.txt"},
		{"big", "/usr/share/wordlists/dirb/big.txt"},
		{"raft-small", "/usr/share/wordlists/seclists/raft-small-words.txt"},
		{"", "/usr/share/wordlists/dirb/common.txt"}, // 空默认 common
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mcpserver.ResolveFfufWordlist(tc.name)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
