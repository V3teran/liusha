package mcpserver

import (
	"errors"

	"github.com/V3teran/liusha/internal/scope"
	"github.com/mark3labs/mcp-go/server"
)

// Server 封装 MCP server 实例及 scope 策略。
type Server struct {
	mcp    *server.MCPServer
	policy *scope.Policy
	tools  map[string]ToolHandler
}

// New 创建 MCP server；policy 不能为 nil。
func New(policy *scope.Policy) (*Server, error) {
	if policy == nil {
		return nil, errors.New("scope policy 不能为 nil")
	}
	s := server.NewMCPServer(
		"liusha-mcp",
		"0.1.0",
		server.WithToolCapabilities(true),
	)
	return &Server{mcp: s, policy: policy, tools: make(map[string]ToolHandler)}, nil
}

// ServeStdio 在当前进程的 stdin/stdout 上运行 MCP 协议循环。
func (s *Server) ServeStdio() error {
	return server.ServeStdio(s.mcp)
}
