# Liusha v1 — Plan 3/8: MCP Server + Vuln-Tools

> ## ⚠️ DEPRECATED — 已废弃，不再执行
>
> **状态**：于 2026-04-20 废弃。本 Plan 下方的任务列表**不再执行**。
>
> **废弃原因**：架构层面判定独立 `mcp-server` 进程 + MCP JSON-RPC 协议是过度设计——工具调用方只有 `agent-worker` 一家，调的又全是内置 `liusha/vuln-tools` 镜像里的预编译二进制，中间套一层跨进程协议徒增链路延迟与攻击面。保留"按名字调工具"的语义，但落点改成 Go 函数调用。
>
> **后继方案**：
> - **代码层抽象**：`internal/tool/runner.DockerRunner`（见 `internal/tool/runner/docker.go`，已落地于分支 `feat/v1-plan-1-foundation`，commit `ce6c20a`）。`DockerRunner.RunTool(ctx, image, cmd)` 校验镜像/工具白名单，在一次性容器（`AutoRemove=true`）内执行工具，硬编码 `ReadonlyRootfs / CapDrop=ALL / no-new-privileges / 非 root / Memory & PIDs & CPU 限额` 的 `HostConfig`，调用方（Role / prompt）无法覆写。
> - **vuln-tools 镜像**：`docker/vuln-tools/Dockerfile`（本 Plan Task 7 产物保留），包含 `nuclei / ffuf / httpx`；在 compose 里用 `profiles: ["build-only"]` 构建但不启动（见 `deployments/docker-compose.yml`）。
> - **docker.sock 挂载点**：搬到 `agent-worker` 容器（Plan 5 装配），不再是独立 `mcp-server`。
> - **MCP 协议相关代码**（`cmd/mcp-server/`、`internal/mcpserver/`、`github.com/mark3labs/mcp-go` 依赖）已从仓库整块删除（commit `1673411`）。
>
> **Spec 同步**：`docs/superpowers/specs/2026-04-18-liusha-refactor-design.md` §4（容器拓扑已从 7 容器降为 6 容器，删除 `mcp-server` 节点）、§5（"工具沙箱"章节已重写为 `DockerRunner + 一次性容器`）、§8（目录布局已删除 `cmd/mcp-server/` 和 `internal/mcp/`，新增 `internal/tool/runner/`）。
>
> **下游 Plan 影响**：原计划 "Plan 3 输出 MCP Server，Plan 5 agent 接入 MCP 工具" 的依赖关系解除。新的依赖链：Plan 1 Foundation（已含 `DockerRunner` 代码） → Plan 5 agent-worker 里装配 `DockerRunner` → Role 直接 `runner.RunTool(ctx, "liusha/vuln-tools", []string{"nuclei", ...})`。
>
> **下方原文保留**仅作历史参考，**不要**按此执行。

---

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 JSON-RPC 2.0 MCP Server（`cmd/mcp-server`），暴露 `vuln-tools` 工具集（nuclei / ffuf / httpx），AI Agent 通过 MCP 协议调用扫描工具，scope 层强制校验确保只对 in-scope 目标执行。

**Architecture:**
`cmd/mcp-server` 监听 stdio（Claude Desktop 标准通道）或 TCP（可选）；接收 JSON-RPC 2.0 请求 → 路由到对应 ToolHandler → 构造命令行 → 调用 `vuln-tools` 容器（通过 Docker SDK exec）或本地二进制 → 流式返回 stdout → 封装为 MCP ToolResult JSON。ScopePolicy（来自 Plan 2）在每次工具调用前校验目标 URL/Host，拒绝 out-of-scope 请求。

**Tech Stack（全部最新稳定版）：**
- `github.com/mark3labs/mcp-go @latest`（MCP JSON-RPC 2.0 Go 实现）
- `github.com/docker/docker/client @latest`（Docker SDK，exec vuln-tools 容器）
- `github.com/google/uuid @latest`（已有）
- 测试：`github.com/stretchr/testify @latest`（已有）
- vuln-tools Dockerfile：`nuclei` + `ffuf` + `httpx`（projectdiscovery 全家桶）

**Spec Reference:** §9（MCP），§10（vuln-tools），§11（scope enforcement layer 3）。

---

## File Structure

```
liusha/
├── internal/
│   └── mcpserver/
│       ├── server.go            # Task 1：MCP server 初始化 + tool 注册
│       ├── server_test.go       # Task 1
│       ├── tools.go             # Task 2：ToolHandler 接口 + 注册表
│       ├── tools_test.go        # Task 2
│       ├── scope_guard.go       # Task 3：scope 校验中间件
│       ├── scope_guard_test.go  # Task 3
│       └── docker_runner.go     # Task 4：Docker exec 封装
│           docker_runner_test.go # Task 4
├── cmd/
│   └── mcp-server/
│       ├── main.go              # Task 5：启动 MCP server
│       └── Dockerfile           # Task 6
├── docker/
│   └── vuln-tools/
│       └── Dockerfile           # Task 7：nuclei + ffuf + httpx
└── deployments/
    └── docker-compose.yml       # Task 8（modify）：新增 vuln-tools 服务
```

---

## Task 1: MCP Server 骨架

**Files:**
- Create: `internal/mcpserver/server.go`
- Create: `internal/mcpserver/server_test.go`

mcp-go 库以 `server.NewMCPServer` + `server.NewStdioServer` 方式使用；每个 Tool 通过 `server.AddTool` 注册。

- [ ] **Step 1: 安装依赖**

```bash
go get github.com/mark3labs/mcp-go@latest
go mod tidy
go mod vendor
```

- [ ] **Step 2: 写失败测试**

```go
// internal/mcpserver/server_test.go
package mcpserver_test

import (
    "testing"
    "github.com/stretchr/testify/require"
    "github.com/V3teran/liusha/internal/mcpserver"
    "github.com/V3teran/liusha/internal/scope"
)

func TestNewServer_NilPolicyErrors(t *testing.T) {
    _, err := mcpserver.New(nil)
    require.Error(t, err)
}

func TestNewServer_OK(t *testing.T) {
    p, _ := scope.NewPolicy(scope.Rules{
        Domains:     []string{"example.com"},
        TimeSeconds: 60,
        FlowCount:   100,
    })
    s, err := mcpserver.New(p)
    require.NoError(t, err)
    require.NotNil(t, s)
}
```

- [ ] **Step 3: 运行测试，确认失败**

```bash
go test -run TestNewServer ./internal/mcpserver/ 2>&1 | head -5
```
期望：`cannot find package` 或 `undefined`

- [ ] **Step 4: 实现 server.go**

```go
// internal/mcpserver/server.go
package mcpserver

import (
    "errors"
    "github.com/mark3labs/mcp-go/server"
    "github.com/V3teran/liusha/internal/scope"
)

type Server struct {
    mcp    *server.MCPServer
    policy *scope.Policy
}

func New(policy *scope.Policy) (*Server, error) {
    if policy == nil {
        return nil, errors.New("scope policy 不能为 nil")
    }
    s := server.NewMCPServer(
        "liusha-mcp",
        "0.1.0",
        server.WithToolCapabilities(true),
    )
    return &Server{mcp: s, policy: policy}, nil
}

// ServeStdio 在当前进程的 stdin/stdout 上运行 MCP 协议循环。
func (s *Server) ServeStdio() error {
    return server.ServeStdio(s.mcp)
}
```

- [ ] **Step 5: 运行测试，确认通过**

```bash
go test -race -run TestNewServer ./internal/mcpserver/
```
期望：`PASS`

- [ ] **Step 6: 提交**

```bash
git add internal/mcpserver/ go.mod go.sum vendor/
git commit -m "feat(mcpserver): MCP server skeleton with scope policy"
```

---

## Task 2: ToolHandler 接口 + 注册

**Files:**
- Create: `internal/mcpserver/tools.go`
- Create: `internal/mcpserver/tools_test.go`
- Modify: `internal/mcpserver/server.go`（新增 `RegisterTool`）

MCP Tool 的入参通过 JSON Schema 描述；调用时收到 `map[string]any` 参数。

- [ ] **Step 1: 写失败测试**

```go
// internal/mcpserver/tools_test.go
package mcpserver_test

import (
    "context"
    "testing"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "github.com/mark3labs/mcp-go/mcp"
    "github.com/V3teran/liusha/internal/mcpserver"
    "github.com/V3teran/liusha/internal/scope"
)

func testPolicy(t *testing.T) *scope.Policy {
    t.Helper()
    p, err := scope.NewPolicy(scope.Rules{
        Domains:     []string{"example.com"},
        TimeSeconds: 60,
        FlowCount:   100,
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
    h := mcpserver.ToolFunc(func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        called = true
        return mcp.NewToolResultText("pong"), nil
    })
    require.NoError(t, s.RegisterTool("ping", "测试工具", nil, h))
    res, err := s.Call(context.Background(), "ping", map[string]any{})
    require.NoError(t, err)
    assert.True(t, called)
    assert.Contains(t, res, "pong")
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test -run TestRegisterTool ./internal/mcpserver/ 2>&1 | head -5
```

- [ ] **Step 3: 实现 tools.go**

```go
// internal/mcpserver/tools.go
package mcpserver

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "github.com/mark3labs/mcp-go/mcp"
    "github.com/mark3labs/mcp-go/server"
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
```

- [ ] **Step 4: 在 server.go 添加 RegisterTool 和 Call**

在 `Server` 结构体中加入 `tools map[string]ToolHandler`，在 `New` 中初始化：

```go
// server.go 中 Server 结构体改为：
type Server struct {
    mcp    *server.MCPServer
    policy *scope.Policy
    tools  map[string]ToolHandler
}

// New 中初始化 tools：
return &Server{mcp: s, policy: policy, tools: make(map[string]ToolHandler)}, nil
```

添加方法：

```go
// RegisterTool 向 MCP server 注册一个工具；同名重复注册返回错误。
func (s *Server) RegisterTool(name, description string, schema any, h ToolHandler) error {
    if _, exists := s.tools[name]; exists {
        return fmt.Errorf("工具 %q 已注册", name)
    }
    s.tools[name] = h

    var inputSchema mcp.ToolInputSchema
    if schema != nil {
        b, _ := json.Marshal(schema)
        _ = json.Unmarshal(b, &inputSchema)
    }
    tool := mcp.NewTool(name, mcp.WithDescription(description))
    s.mcp.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
        return h.Call(ctx, req)
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
```

- [ ] **Step 5: 运行测试**

```bash
go test -race -run TestRegisterTool ./internal/mcpserver/
```
期望：`PASS`

- [ ] **Step 6: 提交**

```bash
git add internal/mcpserver/
git commit -m "feat(mcpserver): ToolHandler interface and RegisterTool"
```

---

## Task 3: ScopeGuard 中间件

**Files:**
- Create: `internal/mcpserver/scope_guard.go`
- Create: `internal/mcpserver/scope_guard_test.go`

每次工具调用前从参数中提取 `target`（URL 或 Host），通过 ScopePolicy 校验。

- [ ] **Step 1: 写失败测试**

```go
// internal/mcpserver/scope_guard_test.go
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
        Domains:     []string{"allowed.com"},
        TimeSeconds: 60,
        FlowCount:   100,
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
        Domains:     []string{"allowed.com"},
        TimeSeconds: 60,
        FlowCount:   100,
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
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
go test -run TestScopeGuard ./internal/mcpserver/ 2>&1 | head -5
```

- [ ] **Step 3: 实现 scope_guard.go**

```go
// internal/mcpserver/scope_guard.go
package mcpserver

import (
    "context"
    "fmt"
    "github.com/mark3labs/mcp-go/mcp"
    "github.com/V3teran/liusha/internal/scope"
)

type scopeGuard struct {
    policy *scope.Policy
    inner  ToolHandler
}

// WithScopeGuard 包装 ToolHandler，在调用前校验参数中的 target 字段。
func WithScopeGuard(policy *scope.Policy, inner ToolHandler) ToolHandler {
    return &scopeGuard{policy: policy, inner: inner}
}

func (g *scopeGuard) Call(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    target, _ := req.Params.Arguments["target"].(string)
    if target == "" {
        return nil, fmt.Errorf("工具调用缺少 target 参数")
    }
    if !g.policy.InScope("GET", target) {
        return nil, fmt.Errorf("out-of-scope 目标: %s", target)
    }
    return g.inner.Call(ctx, req)
}
```

- [ ] **Step 4: 运行测试**

```bash
go test -race -run TestScopeGuard ./internal/mcpserver/
```
期望：`PASS`

- [ ] **Step 5: 提交**

```bash
git add internal/mcpserver/scope_guard.go internal/mcpserver/scope_guard_test.go
git commit -m "feat(mcpserver): ScopeGuard middleware for tool call enforcement"
```

---

## Task 4: DockerRunner — 容器内执行工具

**Files:**
- Create: `internal/mcpserver/docker_runner.go`
- Create: `internal/mcpserver/docker_runner_test.go`

通过 Docker SDK 在 `vuln-tools` 容器里执行命令，返回合并的 stdout+stderr 字符串。超时通过 ctx 控制。

- [ ] **Step 1: 安装依赖**

```bash
go get github.com/docker/docker/client@latest
go mod tidy
go mod vendor
```

- [ ] **Step 2: 写失败测试（单元测试，不依赖真实容器）**

```go
// internal/mcpserver/docker_runner_test.go
package mcpserver_test

import (
    "testing"
    "github.com/stretchr/testify/require"
    "github.com/V3teran/liusha/internal/mcpserver"
)

func TestNewDockerRunner_EmptyContainerErrors(t *testing.T) {
    _, err := mcpserver.NewDockerRunner("")
    require.Error(t, err)
}

func TestNewDockerRunner_OK(t *testing.T) {
    r, err := mcpserver.NewDockerRunner("liusha-vuln-tools")
    require.NoError(t, err)
    require.NotNil(t, r)
}
```

- [ ] **Step 3: 运行测试，确认失败**

```bash
go test -run TestNewDockerRunner ./internal/mcpserver/ 2>&1 | head -5
```

- [ ] **Step 4: 实现 docker_runner.go**

```go
// internal/mcpserver/docker_runner.go
package mcpserver

import (
    "bytes"
    "context"
    "errors"
    "fmt"
    "io"
    "github.com/docker/docker/api/types/container"
    "github.com/docker/docker/client"
)

// DockerRunner 在指定容器内执行命令并收集输出。
type DockerRunner struct {
    cli           *client.Client
    containerName string
}

func NewDockerRunner(containerName string) (*DockerRunner, error) {
    if containerName == "" {
        return nil, errors.New("containerName 不能为空")
    }
    cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
    if err != nil {
        return nil, fmt.Errorf("初始化 Docker client 失败: %w", err)
    }
    return &DockerRunner{cli: cli, containerName: containerName}, nil
}

// Run 在容器内执行 cmd，返回 stdout+stderr 合并输出。
func (r *DockerRunner) Run(ctx context.Context, cmd []string) (string, error) {
    execCfg := container.ExecOptions{
        Cmd:          cmd,
        AttachStdout: true,
        AttachStderr: true,
    }
    resp, err := r.cli.ContainerExecCreate(ctx, r.containerName, execCfg)
    if err != nil {
        return "", fmt.Errorf("exec create 失败: %w", err)
    }
    attach, err := r.cli.ContainerExecAttach(ctx, resp.ID, container.ExecStartOptions{})
    if err != nil {
        return "", fmt.Errorf("exec attach 失败: %w", err)
    }
    defer attach.Close()

    var buf bytes.Buffer
    if _, err := io.Copy(&buf, attach.Reader); err != nil && !errors.Is(err, io.EOF) {
        return "", fmt.Errorf("读取 exec 输出失败: %w", err)
    }
    return buf.String(), nil
}
```

- [ ] **Step 5: 运行测试**

```bash
go test -race -run TestNewDockerRunner ./internal/mcpserver/
```
期望：`PASS`

- [ ] **Step 6: 提交**

```bash
git add internal/mcpserver/docker_runner.go internal/mcpserver/docker_runner_test.go go.mod go.sum vendor/
git commit -m "feat(mcpserver): DockerRunner wraps Docker SDK exec"
```

---

## Task 5: cmd/mcp-server main.go

**Files:**
- Create: `cmd/mcp-server/main.go`

注册内置工具 `nuclei_scan`、`ffuf_fuzz`、`httpx_probe`；每个工具通过 ScopeGuard 包装后委托 DockerRunner 执行。

- [ ] **Step 1: 写失败测试（编译测试）**

```bash
go build ./cmd/mcp-server/ 2>&1
```
期望：`cannot find package`

- [ ] **Step 2: 实现 main.go**

```go
// cmd/mcp-server/main.go
package main

import (
    "context"
    "os"

    "github.com/mark3labs/mcp-go/mcp"
    "github.com/V3teran/liusha/internal/config"
    engstore "github.com/V3teran/liusha/internal/engagement/store"
    "github.com/V3teran/liusha/internal/logx"
    "github.com/V3teran/liusha/internal/mcpserver"
    "github.com/V3teran/liusha/internal/scope"
    "github.com/google/uuid"
    "github.com/jackc/pgx/v5/pgxpool"
    "encoding/json"
)

func main() {
    cfgPath := os.Getenv("LIUSHA_CONFIG")
    if cfgPath == "" {
        cfgPath = "config/config.yaml"
    }
    cfg, err := config.Load(cfgPath)
    if err != nil {
        panic("加载配置失败: " + err.Error())
    }
    logx.Init(cfg.LogLevel, cfg.Env == "development")
    ctx := logx.WithTraceID(context.Background(), logx.NewTraceID())

    engagementIDStr := os.Getenv("LIUSHA_ENGAGEMENT_ID")
    if engagementIDStr == "" {
        logx.From(ctx).Fatal().Msg("必须设置 LIUSHA_ENGAGEMENT_ID 环境变量")
    }
    engID, err := uuid.Parse(engagementIDStr)
    if err != nil {
        logx.From(ctx).Fatal().Err(err).Msg("LIUSHA_ENGAGEMENT_ID 不是合法 UUID")
    }

    pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
    if err != nil {
        logx.From(ctx).Fatal().Err(err).Msg("连接 postgres 失败")
    }
    defer pool.Close()

    engSt := engstore.NewPostgres(pool)
    eng, err := engSt.Get(ctx, engID)
    if err != nil {
        logx.From(ctx).Fatal().Err(err).Str("engagement_id", engID.String()).Msg("加载 engagement 失败")
    }

    policy, err := buildPolicy(eng.Scope)
    if err != nil {
        logx.From(ctx).Fatal().Err(err).Msg("构建 ScopePolicy 失败")
    }

    runner, err := mcpserver.NewDockerRunner("liusha-vuln-tools")
    if err != nil {
        logx.From(ctx).Fatal().Err(err).Msg("初始化 DockerRunner 失败")
    }

    srv, err := mcpserver.New(policy)
    if err != nil {
        logx.From(ctx).Fatal().Err(err).Msg("初始化 MCP server 失败")
    }

    registerTools(srv, runner, policy)

    logx.From(ctx).Info().Msg("liusha-mcp-server 启动（stdio 模式）")
    if err := srv.ServeStdio(); err != nil {
        logx.From(ctx).Fatal().Err(err).Msg("MCP server 退出")
    }
}

func registerTools(srv *mcpserver.Server, runner *mcpserver.DockerRunner, policy *mcpserver.PolicyProvider) {
    // nuclei_scan：对目标运行 nuclei 扫描
    nuclei := mcpserver.WithScopeGuard(policy, mcpserver.ToolFunc(
        func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
            target, _ := req.Params.Arguments["target"].(string)
            tags, _ := req.Params.Arguments["tags"].(string)
            cmd := []string{"nuclei", "-u", target, "-silent", "-j"}
            if tags != "" {
                cmd = append(cmd, "-tags", tags)
            }
            out, err := runner.Run(ctx, cmd)
            if err != nil {
                return nil, err
            }
            return mcp.NewToolResultText(out), nil
        },
    ))
    _ = srv.RegisterTool("nuclei_scan", "对目标 URL 运行 nuclei 漏洞扫描", map[string]any{
        "type": "object",
        "properties": map[string]any{
            "target": map[string]any{"type": "string", "description": "目标 URL"},
            "tags":   map[string]any{"type": "string", "description": "nuclei 标签过滤（可选）"},
        },
        "required": []string{"target"},
    }, nuclei)

    // ffuf_fuzz：目录爆破
    ffuf := mcpserver.WithScopeGuard(policy, mcpserver.ToolFunc(
        func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
            target, _ := req.Params.Arguments["target"].(string)
            wordlist, _ := req.Params.Arguments["wordlist"].(string)
            if wordlist == "" {
                wordlist = "/usr/share/wordlists/dirb/common.txt"
            }
            cmd := []string{"ffuf", "-u", target + "/FUZZ", "-w", wordlist, "-s"}
            out, err := runner.Run(ctx, cmd)
            if err != nil {
                return nil, err
            }
            return mcp.NewToolResultText(out), nil
        },
    ))
    _ = srv.RegisterTool("ffuf_fuzz", "对目标 URL 进行目录爆破", map[string]any{
        "type": "object",
        "properties": map[string]any{
            "target":   map[string]any{"type": "string", "description": "目标 URL"},
            "wordlist": map[string]any{"type": "string", "description": "字典文件路径（可选）"},
        },
        "required": []string{"target"},
    }, ffuf)

    // httpx_probe：探活并收集响应信息
    httpx := mcpserver.WithScopeGuard(policy, mcpserver.ToolFunc(
        func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
            target, _ := req.Params.Arguments["target"].(string)
            cmd := []string{"httpx", "-u", target, "-silent", "-json", "-title", "-tech-detect"}
            out, err := runner.Run(ctx, cmd)
            if err != nil {
                return nil, err
            }
            return mcp.NewToolResultText(out), nil
        },
    ))
    _ = srv.RegisterTool("httpx_probe", "探测目标 URL 并返回响应元数据", map[string]any{
        "type": "object",
        "properties": map[string]any{
            "target": map[string]any{"type": "string", "description": "目标 URL"},
        },
        "required": []string{"target"},
    }, httpx)
}

func buildPolicy(s map[string]any) (*scope.Policy, error) {
    b, err := json.Marshal(s)
    if err != nil {
        return nil, err
    }
    var rules scope.Rules
    if err := json.Unmarshal(b, &rules); err != nil {
        return nil, err
    }
    return scope.NewPolicy(rules)
}
```

> **注意：** `registerTools` 中 `policy *mcpserver.PolicyProvider` 需改为 `*scope.Policy`，`mcpserver.WithScopeGuard` 第一参数直接传 `policy`（`*scope.Policy`）。调整类型签名保持与 Task 3 一致。

- [ ] **Step 3: 修复编译错误并确认构建**

```bash
go build ./cmd/mcp-server/ 2>&1
```
期望：无输出（编译成功）

- [ ] **Step 4: vet**

```bash
go vet ./cmd/mcp-server/ ./internal/mcpserver/
```

- [ ] **Step 5: 提交**

```bash
git add cmd/mcp-server/
git commit -m "feat(cmd/mcp-server): wire MCP server with nuclei/ffuf/httpx tools"
```

---

## Task 6: cmd/mcp-server/Dockerfile

**Files:**
- Create: `cmd/mcp-server/Dockerfile`
- Modify: `Makefile`（新增 `build-mcp`）

- [ ] **Step 1: 创建 Dockerfile**

```dockerfile
# 阶段一：构建 liusha-mcp-server 二进制（离线 vendor 模式）
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ ./vendor/
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -trimpath -ldflags="-s -w" \
    -o /out/liusha-mcp-server ./cmd/mcp-server

# 阶段二：最终运行镜像
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/liusha-mcp-server /usr/local/bin/liusha-mcp-server
COPY config/ /app/config/
ENV LIUSHA_CONFIG=/app/config/config.yaml
ENTRYPOINT ["/usr/local/bin/liusha-mcp-server"]
```

- [ ] **Step 2: 在 Makefile 添加 build-mcp**

在 `.PHONY` 行添加 `build-mcp`，在 `build-proxy` 后添加：

```makefile
build-mcp:
	docker build -f cmd/mcp-server/Dockerfile -t liusha/mcp-server .
```

- [ ] **Step 3: 尝试构建镜像**

```bash
docker build -f cmd/mcp-server/Dockerfile -t liusha/mcp-server . 2>&1 | tail -5
```
期望：最后一行含 `writing image` 或 `naming to`

- [ ] **Step 4: 提交**

```bash
git add cmd/mcp-server/Dockerfile Makefile
git commit -m "feat(cmd/mcp-server): Dockerfile and Makefile build-mcp target"
```

---

## Task 7: vuln-tools Dockerfile

**Files:**
- Create: `docker/vuln-tools/Dockerfile`

包含 `nuclei`、`ffuf`、`httpx`（全部 projectdiscovery 最新稳定版）。

- [ ] **Step 1: 创建 Dockerfile**

```dockerfile
# vuln-tools：nuclei + ffuf + httpx 扫描工具箱
# 构建时通过 Go 安装工具链；运行时保留二进制即可。
FROM golang:1.25-alpine AS tool-builder
RUN apk add --no-cache git ca-certificates
RUN go install github.com/projectdiscovery/nuclei/v3/cmd/nuclei@latest \
 && go install github.com/ffuf/ffuf/v2@latest \
 && go install github.com/projectdiscovery/httpx/cmd/httpx@latest

FROM alpine:3
RUN apk add --no-cache ca-certificates
COPY --from=tool-builder /go/bin/nuclei  /usr/local/bin/nuclei
COPY --from=tool-builder /go/bin/ffuf    /usr/local/bin/ffuf
COPY --from=tool-builder /go/bin/httpx   /usr/local/bin/httpx
# 预置 dirb 常用字典
RUN apk add --no-cache dirb 2>/dev/null || true
ENTRYPOINT ["/bin/sh"]
```

> **注意：** vuln-tools 容器需要持续运行（`docker compose up`），MCP server 通过 Docker exec 调用其中的工具。因此入口点用 `/bin/sh` 并在 compose 中配置 `command: ["sleep", "infinity"]`。

- [ ] **Step 2: 提交**

```bash
git add docker/vuln-tools/Dockerfile
git commit -m "feat(vuln-tools): Dockerfile with nuclei, ffuf, httpx"
```

---

## Task 8: docker-compose 新增 vuln-tools + mcp-server 服务

**Files:**
- Modify: `deployments/docker-compose.yml`
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: 在 docker-compose.yml 新增两个服务**

在 `proxy` 服务后、`volumes:` 块前插入：

```yaml
  # vuln-tools：持续运行的工具容器，mcp-server 通过 docker exec 调用其中的工具。
  vuln-tools:
    image: liusha/vuln-tools
    build:
      context: ..
      dockerfile: docker/vuln-tools/Dockerfile
    container_name: liusha-vuln-tools
    command: ["sleep", "infinity"]
    profiles:
      - mcp

  # mcp-server：Claude Desktop 通过 stdio 调用的 MCP 服务端。
  mcp-server:
    image: liusha/mcp-server
    build:
      context: ..
      dockerfile: cmd/mcp-server/Dockerfile
    container_name: liusha-mcp-server
    environment:
      LIUSHA_POSTGRES_DSN: postgres://liusha:liusha@postgres:5432/liusha?sslmode=disable
      LIUSHA_CONFIG: /app/config/config.yaml
      LIUSHA_ENGAGEMENT_ID: ""
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    depends_on:
      postgres:
        condition: service_healthy
      vuln-tools:
        condition: service_started
    profiles:
      - mcp
```

- [ ] **Step 2: 更新 CI 新增 mcp/vuln-tools 镜像构建步骤**

在 `.github/workflows/ci.yml` 的 build job 末尾添加：

```yaml
      - name: build mcp-server image
        run: docker build -f cmd/mcp-server/Dockerfile -t liusha/mcp-server:ci .
      - name: build vuln-tools image
        run: docker build -f docker/vuln-tools/Dockerfile -t liusha/vuln-tools:ci .
```

> **注意：** vuln-tools 在 CI 构建时同样面临 Docker 容器内网络问题。
> 如果 CI 环境（GitHub Actions ubuntu-latest）可以访问外网则正常执行；
> 如果不行，参照 proxy 构建的 vendor 方案，预编译二进制并放入构建上下文。

- [ ] **Step 3: 运行 go vet 和全量测试**

```bash
go vet ./...
go test -race -short ./...
go test -race -run Integration ./...
```

- [ ] **Step 4: 提交**

```bash
git add deployments/docker-compose.yml .github/workflows/ci.yml
git commit -m "feat(compose): add vuln-tools and mcp-server services"
```

---

## Task 9: 集成验收

**验收条件（人工验证）：**

- [ ] `make build-mcp` 成功输出镜像 `liusha/mcp-server`
- [ ] `make build-api` 和 `make build-proxy` 不受影响
- [ ] `go test -race -short ./...` 全部 PASS
- [ ] `go test -race -run Integration ./...` 全部 PASS
- [ ] MCP server 能在 stdio 模式下响应 `initialize` 请求（可用 `echo` 测试）
- [ ] ScopeGuard 对 out-of-scope 目标返回错误
- [ ] DockerRunner 对不存在的容器返回可读错误信息

- [ ] **Step 1: 运行全量测试**

```bash
go vet ./... && \
gofmt -l $(find . -name '*.go' -not -path './vendor/*') && \
go test -race -short ./... && \
go test -race -run Integration ./...
```

- [ ] **Step 2: 打标签**

```bash
git tag v0.3.0-plan3
```

---

## Task 10: 标签

- [ ] **Step 1: 确认所有提交干净**

```bash
git status
git log --oneline -10
```

- [ ] **Step 2: 打标签**

```bash
git tag v0.3.0-plan3
git log --oneline -1
```
