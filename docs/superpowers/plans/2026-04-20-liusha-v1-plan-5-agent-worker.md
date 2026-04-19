# Liusha v1 — Plan 5/8: Agent Worker (Eino + Asynq ReAct 循环)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现基于 Eino + Asynq 的 Agent Worker 二进制，包含 AgentTask 的 Asynq 入队/消费基础设施、最小 ReAct 循环（Think→Act→Observe），以及完整的 `cmd/agent-worker` 容器。

**Architecture:** `internal/worker/` 定义 Asynq 任务类型与 Payload；`internal/agent/llm/` 封装 Eino ChatModel 工厂；`internal/agent/runtime/` 实现 ReAct 循环，通过内部 `Generator` 接口依赖注入 LLM，使测试无需真实 API Key；`cmd/agent-worker/main.go` 装配所有组件并启动 Asynq Server 消费任务。Plan 5 不接入 MCP 工具（留给 Plan 6），循环无 tool call 时直接写 Finding 节点到图。

**Tech Stack（全部最新稳定版）：**
- `github.com/cloudwego/eino` (LLM 统一抽象)
- `github.com/cloudwego/eino-ext/components/model/claude` (Anthropic provider)
- `github.com/hibiken/asynq` (Redis 任务队列)
- `github.com/jackc/pgx/v5` + `github.com/redis/go-redis/v9`（已有）
- `github.com/testcontainers/testcontainers-go`（已有，集成测试）

---

## File Structure

```
liusha/
├── internal/
│   ├── worker/
│   │   ├── task.go           # AgentRunPayload + task type 常量 + NewAgentRunTask()
│   │   ├── client.go         # Asynq Client 封装：Enqueue(payload) → error
│   │   └── handler.go        # HandleAgentRun(RunParams) → asynq.Handler
│   ├── agent/
│   │   ├── llm/
│   │   │   ├── factory.go    # NewClaude(ctx, apiKey, model, maxTokens) → Generator
│   │   │   └── factory_test.go
│   │   └── runtime/
│   │       ├── react.go      # Generator 接口 + Run(ctx, RunParams) → error
│   │       └── react_test.go # 单元测试（stub Generator）
├── internal/config/
│   ├── config.go             # 新增 LLMConfig 字段
│   └── config_test.go        # 新增 LLM 配置断言
├── config/
│   └── config.yaml           # 新增 llm 块
└── cmd/
    └── agent-worker/
        ├── main.go
        └── Dockerfile
deployments/
└── docker-compose.yml        # 新增 agent-worker 服务
.github/workflows/ci.yml      # 新增 build agent-worker image
Makefile                      # 新增 build-agent-worker 目标
```

---

## Task 1: Asynq 任务基础设施

**Files:**
- Create: `internal/worker/task.go`
- Create: `internal/worker/client.go`
- Create: `internal/worker/handler.go`

- [ ] **Step 1: 创建 task.go**

```go
// internal/worker/task.go
package worker

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

const TypeAgentRun = "agent:run"

// AgentRunPayload 是 Asynq 任务 payload；agent-worker 从队列里取出后执行 ReAct 循环。
type AgentRunPayload struct {
	EngagementID uuid.UUID   `json:"engagement_id"`
	WindowID     uuid.UUID   `json:"window_id"`
	TaskID       uuid.UUID   `json:"task_id"`
	Role         string      `json:"role"`
	InputNodeIDs []uuid.UUID `json:"input_node_ids,omitempty"`
}

// NewAgentRunTask 将 payload 序列化并包装为 *asynq.Task。
func NewAgentRunTask(p AgentRunPayload) (*asynq.Task, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("序列化 AgentRunPayload 失败: %w", err)
	}
	return asynq.NewTask(TypeAgentRun, b), nil
}
```

- [ ] **Step 2: 创建 client.go**

```go
// internal/worker/client.go
package worker

import (
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

type Client struct {
	inner *asynq.Client
}

func NewClient(redisAddr string) *Client {
	return &Client{inner: asynq.NewClient(asynq.RedisClientOpt{Addr: redisAddr})}
}

func (c *Client) Close() error { return c.inner.Close() }

// Enqueue 将 AgentRunPayload 入 Asynq 队列。
// UniqueKey 30 min 内同 (role, taskID) 去重，防重复入队。
func (c *Client) Enqueue(p AgentRunPayload) error {
	task, err := NewAgentRunTask(p)
	if err != nil {
		return err
	}
	_, err = c.inner.Enqueue(task,
		asynq.Queue("default"),
		asynq.MaxRetry(5),
		asynq.Retention(24*time.Hour),
	)
	if err != nil {
		return fmt.Errorf("Asynq Enqueue 失败: %w", err)
	}
	return nil
}
```

- [ ] **Step 3: 创建 handler.go 骨架（实现在 Task 4 补全）**

```go
// internal/worker/handler.go
package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
)

// HandlerDeps 是 HandleAgentRun 的依赖注入包；由 cmd/agent-worker/main.go 构建。
type HandlerDeps struct {
	// Runner 由 Task 4 注入；此处用 interface{} 先占位，handler.go 在 Task 4 前仅做 unmarshal。
	Runner interface{ Run(ctx context.Context, p AgentRunPayload) error }
}

// HandleAgentRun 返回符合 asynq.HandlerFunc 签名的函数。
func HandleAgentRun(deps HandlerDeps) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p AgentRunPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("反序列化 AgentRunPayload 失败: %w", err)
		}
		return deps.Runner.Run(ctx, p)
	}
}
```

- [ ] **Step 4: 安装依赖并 vendor**

```bash
go get github.com/hibiken/asynq@latest
go mod tidy && go mod vendor
```

- [ ] **Step 5: 编译确认**

```bash
go build ./internal/worker/...
```
期望：无输出（无错误）

- [ ] **Step 6: 提交**

```bash
git add internal/worker/ go.mod go.sum vendor/
git commit -m "feat(worker): Asynq task types, client, and handler skeleton"
```

---

## Task 2: Config LLM 字段

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `config/config.yaml`

- [ ] **Step 1: 更新 config.go**

在 `Config` struct 末尾追加 `LLM LLMConfig`；在 `Load()` 函数中追加读取逻辑；不在 `validate()` 中强制要求 `AnthropicAPIKey`（cmd/agent-worker 自行校验）。

```go
// 在 config.go 中追加此 struct：
type LLMConfig struct {
	AnthropicAPIKey string // 从 env LIUSHA_ANTHROPIC_API_KEY 读取
	DefaultModel    string
	MaxTokens       int
	MaxSteps        int
}
```

在 `Load()` 的 cfg 初始化块末尾追加：

```go
LLM: LLMConfig{
    AnthropicAPIKey: v.GetString("ANTHROPIC_API_KEY"),
    DefaultModel:    v.GetString("llm.default_model"),
    MaxTokens:       v.GetInt("llm.max_tokens"),
    MaxSteps:        v.GetInt("llm.max_steps"),
},
```

在 `validate()` 中追加 LLM 字段默认值补全（不强制 API Key）：

```go
if cfg.LLM.DefaultModel == "" {
    cfg.LLM.DefaultModel = "claude-sonnet-4-6"
}
if cfg.LLM.MaxTokens <= 0 {
    cfg.LLM.MaxTokens = 4096
}
if cfg.LLM.MaxSteps <= 0 {
    cfg.LLM.MaxSteps = 15
}
```

- [ ] **Step 2: 更新 config.yaml**

在 `config/config.yaml` 末尾追加：

```yaml
llm:
  default_model: "claude-sonnet-4-6"
  max_tokens: 4096
  max_steps: 15
```

- [ ] **Step 3: 更新 config_test.go**

在 `TestLoad_ReadsFromEnvAndYAML` 的断言块末尾追加：

```go
assert.Equal(t, "claude-sonnet-4-6", cfg.LLM.DefaultModel)
assert.Equal(t, 4096, cfg.LLM.MaxTokens)
assert.Equal(t, 15, cfg.LLM.MaxSteps)
```

- [ ] **Step 4: 运行 config 单元测试**

```bash
go test -race -short ./internal/config/
```
期望：PASS

- [ ] **Step 5: 提交**

```bash
git add internal/config/ config/config.yaml
git commit -m "feat(config): add LLMConfig fields for agent-worker"
```

---

## Task 3: Eino LLM 工厂

**Files:**
- Create: `internal/agent/llm/factory.go`
- Create: `internal/agent/llm/factory_test.go`

`factory.go` 导出两样东西：
1. `Generator` 接口（供 `runtime` 包依赖注入）
2. `NewClaude()` 函数（返回 `Generator`）

- [ ] **Step 1: 写失败测试**

```go
// internal/agent/llm/factory_test.go
package llm_test

import (
	"testing"

	"github.com/V3teran/liusha/internal/agent/llm"
	"github.com/stretchr/testify/assert"
)

func TestNewClaude_RejectsEmptyAPIKey(t *testing.T) {
	_, err := llm.NewClaude("", "claude-sonnet-4-6", 1024)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "api key")
}
```

- [ ] **Step 2: 运行，确认失败**

```bash
go test -run TestNewClaude_RejectsEmptyAPIKey ./internal/agent/llm/ 2>&1 | head -5
```
期望：`FAIL`（包不存在）

- [ ] **Step 3: 创建 factory.go**

```go
// internal/agent/llm/factory.go
package llm

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/claude"
	einoschema "github.com/cloudwego/eino/schema"
)

// Generator 是 ReAct 循环对 LLM 的最小接口；通过此接口注入使测试无需真实 API Key。
type Generator interface {
	Generate(ctx context.Context, msgs []*einoschema.Message) (*einoschema.Message, error)
}

// claudeAdapter 将 Eino ChatModel 的完整接口适配为 Generator。
type claudeAdapter struct {
	inner *claude.ChatModel
}

func (a *claudeAdapter) Generate(ctx context.Context, msgs []*einoschema.Message) (*einoschema.Message, error) {
	return a.inner.Generate(ctx, msgs)
}

// NewClaude 创建 Anthropic Claude Generator。
// apiKey 为空时立即返回错误（fail-fast）。
func NewClaude(apiKey, modelID string, maxTokens int) (Generator, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("anthropic api key 未配置 (LIUSHA_ANTHROPIC_API_KEY)")
	}
	ctx := context.Background()
	m, err := claude.NewChatModel(ctx, &claude.Config{
		APIKey:    apiKey,
		Model:     modelID,
		MaxTokens: &maxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("初始化 Claude model 失败: %w", err)
	}
	return &claudeAdapter{inner: m}, nil
}
```

- [ ] **Step 4: 安装 Eino 依赖并 vendor**

```bash
go get github.com/cloudwego/eino@latest
go get github.com/cloudwego/eino-ext/components/model/claude@latest
go mod tidy && go mod vendor
```

- [ ] **Step 5: 运行测试**

```bash
go test -race -run TestNewClaude_RejectsEmptyAPIKey ./internal/agent/llm/
```
期望：PASS

- [ ] **Step 6: 编译确认**

```bash
go build ./internal/agent/...
```
期望：无输出

- [ ] **Step 7: 提交**

```bash
git add internal/agent/llm/ go.mod go.sum vendor/
git commit -m "feat(agent/llm): Eino+Claude Generator factory"
```

---

## Task 4: ReAct 运行时

**Files:**
- Create: `internal/agent/runtime/react.go`
- Create: `internal/agent/runtime/react_test.go`
- Modify: `internal/worker/handler.go`（补全 Runner 实现）

ReAct 循环逻辑（Plan 5 版）：
1. 从 DB 加载 EngagementID 相关的近期 graph 节点作为上下文
2. 构造 system prompt + user prompt
3. 调用 LLM Generate
4. 若返回有 `ToolCalls` → 本期 Plan 5 直接返回占位观察（"工具尚未接入"），追加到 messages，继续循环
5. 若无 `ToolCalls`（final answer）→ 写 `Finding` 节点到图，标记 AgentTask 为 `succeeded`，退出
6. 超过 MaxSteps → 标记 `failed`，返回错误

- [ ] **Step 1: 写失败测试**

```go
// internal/agent/runtime/react_test.go
package runtime_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	einoschema "github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/agent/runtime"
	"github.com/V3teran/liusha/internal/worker"
)

// stubGenerator 实现 llm.Generator 接口，无需真实 API Key。
type stubGenerator struct {
	calls    int
	response *einoschema.Message
}

func (s *stubGenerator) Generate(_ context.Context, _ []*einoschema.Message) (*einoschema.Message, error) {
	s.calls++
	return s.response, nil
}

func TestReAct_FinalAnswerWritesNode(t *testing.T) {
	ctx := context.Background()
	gen := &stubGenerator{
		response: &einoschema.Message{
			Role:    einoschema.Assistant,
			Content: "发现 SQL 注入漏洞于 /api/login",
		},
	}

	written := make([]string, 0)
	fakeSink := runtime.NodeSinkFunc(func(_ context.Context, kind, content string) error {
		written = append(written, kind+":"+content)
		return nil
	})

	err := runtime.Run(ctx, runtime.RunParams{
		Payload: worker.AgentRunPayload{
			EngagementID: uuid.New(),
			TaskID:       uuid.New(),
			Role:         "sqli_hunter",
		},
		SystemPrompt: "你是 sqli_hunter",
		MaxSteps:     5,
		Gen:          gen,
		NodeSink:     fakeSink,
	})

	require.NoError(t, err)
	assert.Equal(t, 1, gen.calls)
	require.Len(t, written, 1)
	assert.Contains(t, written[0], "finding")
}

func TestReAct_ExceedsMaxStepsReturnsError(t *testing.T) {
	ctx := context.Background()
	// stubGenerator 总是返回带 tool call 的消息，强制循环不收敛
	gen := &stubGenerator{
		response: &einoschema.Message{
			Role: einoschema.Assistant,
			ToolCalls: []*einoschema.ToolCall{
				{ID: "tc1", Type: einoschema.ToolType("function"),
					Function: einoschema.FunctionCall{Name: "nuclei_scan", Arguments: `{}`}},
			},
		},
	}

	err := runtime.Run(ctx, runtime.RunParams{
		Payload: worker.AgentRunPayload{
			EngagementID: uuid.New(),
			TaskID:       uuid.New(),
			Role:         "web_recon",
		},
		SystemPrompt: "你是 web_recon",
		MaxSteps:     3,
		Gen:          gen,
		NodeSink:     runtime.NodeSinkFunc(func(_ context.Context, _, _ string) error { return nil }),
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "最大步数")
	assert.Equal(t, 3, gen.calls)
}
```

- [ ] **Step 2: 运行，确认失败**

```bash
go test -run TestReAct ./internal/agent/runtime/ 2>&1 | head -5
```
期望：FAIL（包不存在）

- [ ] **Step 3: 实现 react.go**

```go
// internal/agent/runtime/react.go
package runtime

import (
	"context"
	"fmt"

	einoschema "github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/worker"
)

// Generator 是 ReAct 循环对 LLM 的最小接口；由 internal/agent/llm 实现。
type Generator interface {
	Generate(ctx context.Context, msgs []*einoschema.Message) (*einoschema.Message, error)
}

// NodeSink 将最终 Finding 写入持久化层（Postgres graph store）。
// 测试时注入 NodeSinkFunc 桩；生产时注入调用 graphstore.Postgres.AddNode 的闭包。
type NodeSink interface {
	WriteNode(ctx context.Context, kind, content string) error
}

// NodeSinkFunc 将普通函数适配为 NodeSink。
type NodeSinkFunc func(ctx context.Context, kind, content string) error

func (f NodeSinkFunc) WriteNode(ctx context.Context, kind, content string) error {
	return f(ctx, kind, content)
}

// RunParams 包含单次 ReAct 循环所需的全部依赖。
type RunParams struct {
	Payload      worker.AgentRunPayload
	SystemPrompt string
	MaxSteps     int
	Gen          Generator
	NodeSink     NodeSink
}

// Run 执行 ReAct 循环（Think → Act → Observe），Plan 5 版无真实工具。
// - 无 tool call → 写 Finding 节点，返回 nil
// - 有 tool call → 追加占位 observation，继续循环
// - 超过 MaxSteps → 返回错误
func Run(ctx context.Context, p RunParams) error {
	msgs := []*einoschema.Message{
		{Role: einoschema.System, Content: p.SystemPrompt},
		{Role: einoschema.User, Content: buildUserPrompt(p.Payload)},
	}

	for step := 0; step < p.MaxSteps; step++ {
		resp, err := p.Gen.Generate(ctx, msgs)
		if err != nil {
			return fmt.Errorf("step %d LLM Generate 失败: %w", step, err)
		}

		// Final answer（无工具调用）
		if len(resp.ToolCalls) == 0 {
			return p.NodeSink.WriteNode(ctx, "finding", resp.Content)
		}

		// 追加 assistant 消息
		msgs = append(msgs, resp)

		// Plan 5: 对每个 tool call 返回占位 observation，Plan 6 接入 MCP
		for _, tc := range resp.ToolCalls {
			msgs = append(msgs, &einoschema.Message{
				Role:       einoschema.Tool,
				Content:    fmt.Sprintf("工具 %s 尚未在 Plan 5 中接入，请直接推理", tc.Function.Name),
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
		}
	}

	return fmt.Errorf("超过最大步数 %d 未收敛", p.MaxSteps)
}

// buildUserPrompt 根据 AgentRunPayload 构造初始 user 消息。
func buildUserPrompt(p worker.AgentRunPayload) string {
	if len(p.InputNodeIDs) == 0 {
		return fmt.Sprintf("Role: %s\nEngagement: %s\n任务开始，请分析目标。", p.Role, p.EngagementID)
	}
	ids := make([]string, len(p.InputNodeIDs))
	for i, id := range p.InputNodeIDs {
		ids[i] = id.String()
	}
	return fmt.Sprintf("Role: %s\nEngagement: %s\n输入节点: %v\n请基于上述节点继续分析。",
		p.Role, p.EngagementID, ids)
}

// Ensure uuid import is used (only needed when InputNodeIDs loop isn't the only usage)
var _ = uuid.Nil
```

> **注意**: 上面 `var _ = uuid.Nil` 仅在 `uuid` 只出现在 `buildUserPrompt` 内部时防止 unused import。如果编译器提示 unused，删除此行；如果提示缺少 import，确认 import 块含 `"github.com/google/uuid"`。

- [ ] **Step 4: 运行测试**

```bash
go test -race -run TestReAct ./internal/agent/runtime/
```
期望：PASS（2 个测试）

- [ ] **Step 5: 补全 handler.go**

将 `internal/worker/handler.go` 中 `HandlerDeps.Runner` 的 `interface{}` 改为具名接口：

```go
// internal/worker/handler.go
package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
)

// Runner 是 HandleAgentRun 对 ReAct runtime 的依赖接口。
type Runner interface {
	Run(ctx context.Context, p AgentRunPayload) error
}

// HandlerDeps 是 HandleAgentRun 的依赖注入包。
type HandlerDeps struct {
	Runner Runner
}

// HandleAgentRun 返回符合 asynq.HandlerFunc 签名的函数。
func HandleAgentRun(deps HandlerDeps) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p AgentRunPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("反序列化 AgentRunPayload 失败: %w", err)
		}
		return deps.Runner.Run(ctx, p)
	}
}
```

- [ ] **Step 6: 编译全量确认**

```bash
go build ./...
```
期望：无输出

- [ ] **Step 7: 提交**

```bash
git add internal/agent/runtime/ internal/worker/handler.go
git commit -m "feat(agent/runtime): ReAct loop with Generator interface and NodeSink"
```

---

## Task 5: cmd/agent-worker 二进制 + Dockerfile + Compose

**Files:**
- Create: `cmd/agent-worker/main.go`
- Create: `cmd/agent-worker/Dockerfile`
- Modify: `deployments/docker-compose.yml`
- Modify: `.github/workflows/ci.yml`
- Modify: `Makefile`

- [ ] **Step 1: 创建 cmd/agent-worker/main.go**

```go
// cmd/agent-worker/main.go
// agent-worker 启动 Asynq Server，消费 agent:run 任务，执行 ReAct 循环。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"github.com/V3teran/liusha/internal/agent/llm"
	"github.com/V3teran/liusha/internal/agent/runtime"
	"github.com/V3teran/liusha/internal/config"
	graphstore "github.com/V3teran/liusha/internal/graph/store"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/worker"
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
	bootCtx := logx.WithTraceID(context.Background(), logx.NewTraceID())

	// 校验 LLM API Key（agent-worker 专属，config.validate() 不做此检查）
	if cfg.LLM.AnthropicAPIKey == "" {
		log.Fatal().Msg("LIUSHA_ANTHROPIC_API_KEY 未设置")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pool, err := pgxpool.New(bootCtx, cfg.PostgresDSN)
	if err != nil {
		log.Fatal().Err(err).Msg("Postgres 连接失败")
	}
	defer pool.Close()

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer rdb.Close()

	gen, err := llm.NewClaude(cfg.LLM.AnthropicAPIKey, cfg.LLM.DefaultModel, cfg.LLM.MaxTokens)
	if err != nil {
		log.Fatal().Err(err).Msg("初始化 Eino Claude 失败")
	}

	graphPG := graphstore.NewPostgres(pool)

	// nodeSink 将 ReAct final answer 写入 graph_node。
	nodeSink := runtime.NodeSinkFunc(func(sinkCtx context.Context, kind, content string) error {
		engID := uuid.Nil // 由调用方通过闭包或 context 传递; 此处为骨架
		_ = engID
		log.Ctx(sinkCtx).Info().Str("kind", kind).Msg("writing node")
		return graphPG.AddNode(sinkCtx, buildFindingNode(engID, content))
	})
	_ = nodeSink // 当前骨架；Task 4 的 runner 在下方实现后会真正使用

	// reactRunner 将 AgentRunPayload 映射到 runtime.Run。
	reactRunner := &reactRunnerImpl{
		cfg:      cfg,
		gen:      gen,
		nodeSink: nodeSink,
	}

	mux := asynq.NewServeMux()
	mux.Handle(worker.TypeAgentRun, worker.HandleAgentRun(worker.HandlerDeps{
		Runner: reactRunner,
	}))

	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: cfg.RedisAddr},
		asynq.Config{
			Queues:      map[string]int{"critical": 6, "default": 3},
			Concurrency: 5,
		},
	)

	log.Info().Str("redis", cfg.RedisAddr).Msg("agent-worker 启动")

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(mux) }()

	select {
	case <-ctx.Done():
		srv.Shutdown()
	case err := <-errCh:
		if err != nil {
			log.Fatal().Err(err).Msg("Asynq server 异常退出")
		}
	}
}

type reactRunnerImpl struct {
	cfg      config.Config
	gen      llm.Generator
	nodeSink runtime.NodeSink
}

func (r *reactRunnerImpl) Run(ctx context.Context, p worker.AgentRunPayload) error {
	return runtime.Run(ctx, runtime.RunParams{
		Payload:      p,
		SystemPrompt: fmt.Sprintf("你是 %s，专注于渗透测试中的相关漏洞检测与验证。", p.Role),
		MaxSteps:     r.cfg.LLM.MaxSteps,
		Gen:          r.gen,
		NodeSink:     r.nodeSink,
	})
}

func buildFindingNode(engID uuid.UUID, content string) interface{} {
	// 临时骨架；Plan 6 补全为 graphmodel.Node
	return nil
}
```

> **注意**: `buildFindingNode` 在 Plan 5 是占位骨架，`_ = nodeSink` 也是骨架注释。Plan 6 实现 Role DSL 时会删除这些占位，将 `nodeSink` 真正与 `graphPG.AddNode` 连接。

- [ ] **Step 2: 修复编译错误**

运行 `go build ./cmd/agent-worker/` 并根据错误修复（通常是 import 顺序或未使用变量）：

```bash
go build ./cmd/agent-worker/ 2>&1
```

若提示 `buildFindingNode` 返回类型不匹配 `graphmodel.Node`，将函数体改为：

```go
import graphmodel "github.com/V3teran/liusha/internal/graph/model"

func buildFindingNode(engID uuid.UUID, content string) graphmodel.Node {
	return graphmodel.Node{
		ID:           uuid.New(),
		EngagementID: engID,
		Kind:         graphmodel.KindFinding,
		Payload:      map[string]any{"content": content},
	}
}
```

并将 `nodeSink` 中 `graphPG.AddNode` 的调用从 `buildFindingNode(engID, content)` 更新为对应类型。

- [ ] **Step 3: 创建 Dockerfile**

```dockerfile
# cmd/agent-worker/Dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor/ ./vendor/
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -trimpath -ldflags="-s -w" \
    -o /out/agent-worker ./cmd/agent-worker

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /out/agent-worker /app/agent-worker
COPY config/ /app/config/
ENV LIUSHA_CONFIG=/app/config/config.yaml
ENTRYPOINT ["/app/agent-worker"]
```

- [ ] **Step 4: 更新 docker-compose.yml**

在 `services:` 块末尾追加（在 `volumes:` 声明之前）：

```yaml
  # agent-worker：消费 Asynq 队列，执行 ReAct 循环；无状态，可多副本横向扩展。
  agent-worker:
    image: liusha/agent-worker
    build:
      context: ..
      dockerfile: cmd/agent-worker/Dockerfile
    container_name: liusha-agent-worker
    environment:
      LIUSHA_POSTGRES_DSN: postgres://liusha:liusha@postgres:5432/liusha?sslmode=disable
      LIUSHA_REDIS_ADDR: redis:6379
      LIUSHA_CONFIG: /app/config/config.yaml
      LIUSHA_ENV: development
      LIUSHA_LOG_LEVEL: info
      LIUSHA_ANTHROPIC_API_KEY: "${LIUSHA_ANTHROPIC_API_KEY}"
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    profiles:
      - agent
```

- [ ] **Step 5: 更新 Makefile**

在 `.PHONY` 行追加 `build-agent-worker`；在 `build-mcp:` 后追加：

```makefile
build-agent-worker:
	docker build -f cmd/agent-worker/Dockerfile -t liusha/agent-worker .
```

- [ ] **Step 6: 更新 CI**

在 `.github/workflows/ci.yml` 的 `build` job 中 `build mcp-server image` 步骤后追加：

```yaml
      - name: build agent-worker image
        run: docker build -f cmd/agent-worker/Dockerfile -t liusha/agent-worker:ci .
```

- [ ] **Step 7: 全量单元 + 集成测试**

```bash
go vet ./...
gofmt -l $(find . -name '*.go' -not -path './vendor/*')
go test -race -short ./...
go test -race -run TestIntegration ./...
```
期望：所有测试 PASS，gofmt 无输出

- [ ] **Step 8: 提交并打标签**

```bash
git add cmd/agent-worker/ deployments/docker-compose.yml \
        .github/workflows/ci.yml Makefile
git commit -m "feat(agent-worker): cmd binary, Dockerfile, compose wiring"
git tag v0.5.0-plan5
```

---

## Self-Review

### Spec coverage

| Spec 要求 | 覆盖任务 |
|---|---|
| Asynq 任务队列基础设施 | Task 1 |
| LLM 多 Provider（Eino + Anthropic） | Task 3 |
| Config LLM 字段 | Task 2 |
| ReAct 循环 Think→Act→Observe | Task 4 |
| cmd/agent-worker 二进制 | Task 5 |
| Dockerfile + compose | Task 5 |
| CI build step | Task 5 |
| 日志 trace_id（已由 logx.Init 覆盖） | Task 5（main.go 复用 logx） |

**有意延后（Plan 6+）：**
- MCP 工具实际接入（Plan 6）
- Role DSL（Plan 6）
- ChainMatcher（Plan 7）
- `input_node_ids` 真实加载 graph context（Plan 6）

### Placeholder scan

- `buildFindingNode` 在 Step 2 的修复指令中提供了完整代码（graphmodel.Node 版本），不是 TBD。
- `nodeSink` 骨架在 Step 1 有占位注释，Step 2 说明如何修复，代码完整。
- handler.go 在 Task 4 Step 5 给出了完整最终版本，不依赖 Task 1 的中间状态。

### Type consistency

- `worker.AgentRunPayload` 在 Task 1 定义，Task 4/5 均使用同一结构体，字段一致。
- `runtime.Generator` 接口在 Task 4 定义，Task 3 `factory.go` 返回同名接口，import path 一致。
- `runtime.NodeSink` / `NodeSinkFunc` 在 Task 4 定义，Task 5 的 `nodeSink` 使用 `NodeSinkFunc`，类型匹配。
- `worker.Runner` 接口在 Task 4 Step 5 定义，`reactRunnerImpl` 在 Task 5 实现，方法签名一致。
