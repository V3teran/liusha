# Liusha v1 — Plan 5/8: Agent Worker (Eino + Asynq ReAct 骨架)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal（严格收敛）：** 构建 agent-worker 二进制的 **dry-run 骨架**：Asynq 任务队列基础设施 + Eino LLM 工厂 + ReAct 循环（Think→Act→Observe）+ **仅日志输出的 LoggingSink**。**本期不写 graph_node 落库**，仅记 structured log + 更新 AgentTask 状态，避免 LLM 无工具时产生幻觉节点污染 Plan 6 的真实图。

**Non-goals（明确延后）：**
- 工具接入：装配 `internal/tool/runner.DockerRunner` 到 agent-worker、把 `RunTool` 暴露给 Role 作为 ReAct 的 Act 通道（Plan 6）。注：原 Plan 3 "MCP Server" 方案已废弃，改由 `DockerRunner` 直连 `docker.sock`，相关 `vuln-tools` 镜像构建产物（`docker/vuln-tools/Dockerfile`）与代码层 runner 已随 Plan 1 落地。
- Role DSL、ChainMatcher（Plan 6 / 7）
- 真实 graph_node 落库、Finding 去重（Plan 6，等 ScopePolicy + 工具都具备后再动真格）
- `input_node_ids` 真实加载节点 payload 作为 LLM 上下文（Plan 6）

**Architecture:**
- `internal/worker/`：Asynq 任务 payload、client、handler
- `internal/agent/llm/`：Eino ChatModel 工厂，导出 `Generator` 接口（**单一真源**，runtime/llm 都从此引用）
- `internal/agent/runtime/`：ReAct 循环，依赖注入 `Generator` + `Sink`，使单元测试零 API Key 零 DB
- `cmd/agent-worker/`：装配所有组件，启动 Asynq Server

**Tech Stack（全部 @latest）：**
- `github.com/cloudwego/eino`（LLM 统一抽象）
- `github.com/cloudwego/eino-ext/components/model/deepseek`（DeepSeek provider，OpenAI 兼容）
- `github.com/hibiken/asynq`（Redis 任务队列）
- `github.com/jackc/pgx/v5` + `github.com/redis/go-redis/v9`（已在）
- ~~`github.com/prometheus/client_golang`（Task 6 metrics）~~ — Task 6 已撤回（YAGNI，延后到 Plan 9）

**Provider 决策（2026-04-20 更新）：** 原计划使用 Anthropic Claude，经与用户对齐改为 **DeepSeek**。决策依据：成本敏感（ReAct 循环多步调用）+ DeepSeek-V3.2 function calling 成熟 + 中文漏洞语料覆盖好。Plan 6 引入 **per-role 模型映射**：`planner`/`chain_verifier` 用 `deepseek-reasoner`（思考模式，推理强），`web_recon`/`hunter*` 用 `deepseek-chat`（非思考模式，快且便宜）。

**关键默认值：**
- 模型 ID（Plan 5 骨架期单一默认值）：`deepseek-chat`（DeepSeek-V3.2 非思考模式）；Plan 6 按 Role 差异化
- API BaseURL：`https://api.deepseek.com`（官方 OpenAI 兼容端点，但做成 config 可配置，兼容 OneAPI/LiteLLM 自建网关）
- API Key env：`LIUSHA_DEEPSEEK_API_KEY`
- MaxSteps：**5**（Plan 5 是骨架，无工具时不宜循环过多）
- MaxRetry：3
- Retention：24h
- Asynq Unique TTL：30min（用 (engagement_id, task_id) 做 idempotency key）

---

## File Structure

```
liusha/
├── internal/
│   ├── worker/
│   │   ├── task.go             # AgentRunPayload + TypeAgentRun + NewAgentRunTask
│   │   ├── task_test.go
│   │   ├── client.go           # Client.Enqueue 含 asynq.Unique + asynq.TaskID
│   │   ├── client_test.go      # miniredis 验证 unique 去重
│   │   ├── handler.go          # HandleAgentRun + Runner 接口
│   │   └── handler_test.go
│   ├── agent/
│   │   ├── llm/
│   │   │   ├── factory.go      # Generator 接口（单一真源）+ NewDeepSeek
│   │   │   └── factory_test.go
│   │   └── runtime/
│   │       ├── react.go        # RunParams + Run，使用 llm.Generator（不再重复定义接口）
│   │       ├── react_test.go   # stub Generator + LoggingSink 断言
│   │       └── sink.go         # Sink 接口 + LoggingSink 实现
├── internal/config/
│   ├── config.go               # +LLMConfig
│   └── config_test.go
├── config/config.yaml          # +llm 块
├── cmd/agent-worker/
│   ├── main.go                 # 装配 + /healthz HTTP server
│   └── Dockerfile
├── deployments/docker-compose.yml
├── .github/workflows/ci.yml
└── Makefile
```

---

## Task 1: Asynq 任务基础设施

**Files:**
- Create: `internal/worker/task.go`, `internal/worker/task_test.go`
- Create: `internal/worker/client.go`, `internal/worker/client_test.go`
- Create: `internal/worker/handler.go`, `internal/worker/handler_test.go`

- [ ] **Step 1: task.go**

```go
// internal/worker/task.go
package worker

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// TypeAgentRun 是 Asynq 队列里 agent 执行任务的类型字符串。
const TypeAgentRun = "agent:run"

// AgentRunPayload 是 agent-worker 执行 ReAct 循环所需的所有上下文。
// 字段小而稳：避免把大 JSON payload 直接塞 Redis 队列。
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

- [ ] **Step 2: task_test.go**

```go
// internal/worker/task_test.go
package worker_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/worker"
)

func TestNewAgentRunTask_RoundTrip(t *testing.T) {
	engID := uuid.New()
	taskID := uuid.New()
	in := worker.AgentRunPayload{
		EngagementID: engID,
		TaskID:       taskID,
		Role:         "web_recon",
	}
	task, err := worker.NewAgentRunTask(in)
	require.NoError(t, err)
	assert.Equal(t, worker.TypeAgentRun, task.Type())

	var out worker.AgentRunPayload
	require.NoError(t, json.Unmarshal(task.Payload(), &out))
	assert.Equal(t, engID, out.EngagementID)
	assert.Equal(t, taskID, out.TaskID)
	assert.Equal(t, "web_recon", out.Role)
}
```

- [ ] **Step 3: client.go（关键：asynq.Unique + asynq.TaskID 做幂等）**

```go
// internal/worker/client.go
package worker

import (
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

// Client 封装 Asynq Enqueue，对外隐藏 TaskID/Unique/Retry/Retention 等配置。
type Client struct {
	inner *asynq.Client
}

func NewClient(redisAddr string) *Client {
	return &Client{inner: asynq.NewClient(asynq.RedisClientOpt{Addr: redisAddr})}
}

func (c *Client) Close() error { return c.inner.Close() }

// Enqueue 将 AgentRunPayload 入 Asynq 队列。
//
// 幂等策略：
//   - asynq.TaskID("agent:run:" + taskID) 使同一 AgentTask 只入队一次
//   - asynq.Unique(30*time.Minute) 在 30 分钟窗口内防重复入队
//   - MaxRetry=3：LLM 临时失败自动重试
//   - Retention=24h：便于 asynq dashboard 排错
func (c *Client) Enqueue(p AgentRunPayload) error {
	task, err := NewAgentRunTask(p)
	if err != nil {
		return err
	}
	_, err = c.inner.Enqueue(task,
		asynq.Queue("default"),
		asynq.TaskID(fmt.Sprintf("agent:run:%s", p.TaskID)),
		asynq.Unique(30*time.Minute),
		asynq.MaxRetry(3),
		asynq.Retention(24*time.Hour),
	)
	if err != nil {
		return fmt.Errorf("Asynq Enqueue 失败: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: client_test.go（用 miniredis 验证幂等）**

安装 `github.com/alicebob/miniredis/v2@latest`。

```go
// internal/worker/client_test.go
package worker_test

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/worker"
)

func TestClient_Enqueue_Idempotent(t *testing.T) {
	mr := miniredis.RunT(t)
	c := worker.NewClient(mr.Addr())
	defer c.Close()

	taskID := uuid.New()
	p := worker.AgentRunPayload{
		EngagementID: uuid.New(),
		TaskID:       taskID,
		Role:         "web_recon",
	}

	require.NoError(t, c.Enqueue(p))
	// 30min 内同 TaskID 再 Enqueue 应返回 asynq.ErrDuplicateTask
	err := c.Enqueue(p)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Asynq Enqueue 失败")
}
```

- [ ] **Step 5: handler.go**

```go
// internal/worker/handler.go
package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
)

// Runner 是 handler 对 ReAct runtime 的最小依赖；具体实现在 cmd/agent-worker/main.go。
type Runner interface {
	Run(ctx context.Context, p AgentRunPayload) error
}

type HandlerDeps struct {
	Runner Runner
}

// HandleAgentRun 返回 asynq.HandlerFunc：反序列化 payload → 调用 Runner。
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

- [ ] **Step 6: handler_test.go**

```go
// internal/worker/handler_test.go
package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/worker"
)

type fakeRunner struct {
	called bool
	err    error
	got    worker.AgentRunPayload
}

func (f *fakeRunner) Run(_ context.Context, p worker.AgentRunPayload) error {
	f.called = true
	f.got = p
	return f.err
}

func TestHandleAgentRun_Success(t *testing.T) {
	r := &fakeRunner{}
	h := worker.HandleAgentRun(worker.HandlerDeps{Runner: r})

	p := worker.AgentRunPayload{EngagementID: uuid.New(), TaskID: uuid.New(), Role: "web_recon"}
	b, _ := json.Marshal(p)
	task := asynq.NewTask(worker.TypeAgentRun, b)

	err := h(context.Background(), task)
	require.NoError(t, err)
	assert.True(t, r.called)
	assert.Equal(t, p.TaskID, r.got.TaskID)
}

func TestHandleAgentRun_BadPayload(t *testing.T) {
	h := worker.HandleAgentRun(worker.HandlerDeps{Runner: &fakeRunner{}})
	task := asynq.NewTask(worker.TypeAgentRun, []byte("not-json"))
	err := h(context.Background(), task)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "反序列化")
}

func TestHandleAgentRun_RunnerError(t *testing.T) {
	r := &fakeRunner{err: errors.New("boom")}
	h := worker.HandleAgentRun(worker.HandlerDeps{Runner: r})

	p := worker.AgentRunPayload{TaskID: uuid.New()}
	b, _ := json.Marshal(p)
	task := asynq.NewTask(worker.TypeAgentRun, b)

	err := h(context.Background(), task)
	assert.EqualError(t, err, "boom")
}
```

- [ ] **Step 7: 依赖 + 编译**

```bash
go get github.com/hibiken/asynq@latest
go get github.com/alicebob/miniredis/v2@latest
go mod tidy && go mod vendor
go build ./internal/worker/...
go test -race ./internal/worker/...
```

- [ ] **Step 8: 提交**

```bash
git add internal/worker/ go.mod go.sum vendor/
git commit -m "feat(worker): Asynq task types, idempotent client, handler"
```

---

## Task 2: Config LLM 字段

**Files:**
- Modify: `internal/config/config.go`, `internal/config/config_test.go`
- Modify: `config/config.yaml`

- [ ] **Step 1: config.go 追加**

```go
type LLMConfig struct {
	APIKey       string // 从 env LIUSHA_DEEPSEEK_API_KEY 读
	BaseURL      string // DeepSeek 端点；默认 https://api.deepseek.com，可走自建网关
	DefaultModel string // Plan 5 骨架期单一默认；Plan 6 引入 RoleModels map[string]string
	MaxTokens    int
	MaxSteps     int
}
```

`Config` struct 末尾追加 `LLM LLMConfig`。`Load()` 读：

```go
LLM: LLMConfig{
    APIKey:       v.GetString("DEEPSEEK_API_KEY"),
    BaseURL:      v.GetString("llm.base_url"),
    DefaultModel: v.GetString("llm.default_model"),
    MaxTokens:    v.GetInt("llm.max_tokens"),
    MaxSteps:     v.GetInt("llm.max_steps"),
},
```

`validate()` 只补默认值，**不强制要求 API Key**（cmd/agent-worker 启动时自行 fail-fast）：

```go
if cfg.LLM.DefaultModel == "" {
    cfg.LLM.DefaultModel = "deepseek-chat"
}
if cfg.LLM.BaseURL == "" {
    cfg.LLM.BaseURL = "https://api.deepseek.com"
}
if cfg.LLM.MaxTokens <= 0 {
    cfg.LLM.MaxTokens = 4096
}
if cfg.LLM.MaxSteps <= 0 {
    cfg.LLM.MaxSteps = 5
}
```

- [ ] **Step 2: config/config.yaml 末尾追加**

```yaml
llm:
  default_model: "deepseek-chat"
  base_url: "https://api.deepseek.com"
  max_tokens: 4096
  max_steps: 5
```

- [ ] **Step 3: config_test.go 追加断言**

```go
assert.Equal(t, "deepseek-chat", cfg.LLM.DefaultModel)
assert.Equal(t, "https://api.deepseek.com", cfg.LLM.BaseURL)
assert.Equal(t, 4096, cfg.LLM.MaxTokens)
assert.Equal(t, 5, cfg.LLM.MaxSteps)
```

- [ ] **Step 4: 测试 + 提交**

```bash
go test -race -short ./internal/config/
git add internal/config/ config/config.yaml
git commit -m "feat(config): add LLMConfig (DeepSeek deepseek-chat default, maxSteps=5)"
```

---

## Task 3: Eino LLM 工厂（Generator 接口单一真源）

**Files:**
- Create: `internal/agent/llm/factory.go`
- Create: `internal/agent/llm/factory_test.go`

**关键约定：** `Generator` 接口**只在 `internal/agent/llm` 里定义一次**。`internal/agent/runtime/react.go` 直接 import 并引用 `llm.Generator`，不再重复定义。这样测试 stub 和生产实现满足同一个接口签名，避免两份接口 drift。

**Provider：** DeepSeek（V3.2，OpenAI 兼容）。Plan 6 引入 per-role 模型映射时，`NewDeepSeek` 接受动态 `modelID` 即可，无需重构。

- [ ] **Step 1: 失败测试**

```go
// internal/agent/llm/factory_test.go
package llm_test

import (
	"testing"

	"github.com/V3teran/liusha/internal/agent/llm"
	"github.com/stretchr/testify/assert"
)

func TestNewDeepSeek_RejectsEmptyAPIKey(t *testing.T) {
	_, err := llm.NewDeepSeek("", "https://api.deepseek.com", "deepseek-chat", 1024)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "api key")
}

func TestNewDeepSeek_RejectsEmptyBaseURL(t *testing.T) {
	_, err := llm.NewDeepSeek("sk-xxx", "", "deepseek-chat", 1024)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "base url")
}
```

- [ ] **Step 2: 运行确认 FAIL**

```bash
go test -run TestNewDeepSeek ./internal/agent/llm/ 2>&1 | head -10
```

- [ ] **Step 3: factory.go**

```go
// internal/agent/llm/factory.go
package llm

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/deepseek"
	einoschema "github.com/cloudwego/eino/schema"
)

// Generator 是 ReAct 循环对 LLM 的最小依赖；此包是 **唯一定义处**。
// runtime 包直接 import llm.Generator，不重复定义，避免接口漂移。
type Generator interface {
	Generate(ctx context.Context, msgs []*einoschema.Message) (*einoschema.Message, error)
}

type deepseekAdapter struct {
	inner *deepseek.ChatModel
}

func (a *deepseekAdapter) Generate(ctx context.Context, msgs []*einoschema.Message) (*einoschema.Message, error) {
	return a.inner.Generate(ctx, msgs)
}

// NewDeepSeek 创建 DeepSeek Generator（fail-fast：空 key / 空 base url 直接报错）。
//
// Plan 5 骨架期所有 Role 共用同一个 Generator 实例。Plan 6 引入 per-role 映射时，
// 按 Role 构造多个 Generator 实例，此工厂函数不变。
func NewDeepSeek(apiKey, baseURL, modelID string, maxTokens int) (Generator, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("deepseek api key 未配置 (LIUSHA_DEEPSEEK_API_KEY)")
	}
	if baseURL == "" {
		return nil, fmt.Errorf("deepseek base url 未配置")
	}
	ctx := context.Background()
	m, err := deepseek.NewChatModel(ctx, &deepseek.ChatModelConfig{
		APIKey:    apiKey,
		Model:     modelID,
		BaseURL:   baseURL,
		MaxTokens: maxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("初始化 DeepSeek model 失败: %w", err)
	}
	return &deepseekAdapter{inner: m}, nil
}
```

- [ ] **Step 4: 安装依赖**

```bash
go get github.com/cloudwego/eino@latest
go get github.com/cloudwego/eino-ext/components/model/deepseek@latest
go mod tidy && go mod vendor
```

- [ ] **Step 5: 测试 + 编译**

```bash
go test -race -run TestNewDeepSeek ./internal/agent/llm/
go build ./internal/agent/...
```

- [ ] **Step 6: 提交**

```bash
git add internal/agent/llm/ go.mod go.sum vendor/
git commit -m "feat(agent/llm): Eino+DeepSeek Generator factory (single source of truth)"
```

---

## Task 4: ReAct 运行时（Sink 接口 + LoggingSink）

**Files:**
- Create: `internal/agent/runtime/sink.go`
- Create: `internal/agent/runtime/react.go`
- Create: `internal/agent/runtime/react_test.go`

**核心决定：** Plan 5 **不做真实 graph_node 落库**。Sink 实现为 `LoggingSink`：把 final answer / tool-call summary 记到 structured log，AgentTask 状态机走通（running → succeeded/failed），但 graph 图空空如也。Plan 6 把 `internal/tool/runner.DockerRunner` 接入 Role 的 Act 通道后，再把 Sink 替换成 `GraphSink`（调 CachingStore.AddNode）。

- [ ] **Step 1: sink.go**

```go
// internal/agent/runtime/sink.go
package runtime

import (
	"context"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Observation 是单次 ReAct 循环的输出。
// Plan 5 只记日志；Plan 6 会新增 GraphSink 实现将 final answer 写 graph_node。
type Observation struct {
	EngagementID uuid.UUID
	TaskID       uuid.UUID
	Role         string
	Kind         string // "final_answer" | "tool_call_deferred"
	Content      string
	ToolName     string // Kind=="tool_call_deferred" 时填
	Step         int
}

// Sink 是 ReAct 循环对外的观察点接收器。
type Sink interface {
	Observe(ctx context.Context, o Observation) error
}

// LoggingSink 仅打结构化日志，不落库。Plan 5 骨架用此实现。
type LoggingSink struct {
	log zerolog.Logger
}

func NewLoggingSink(log zerolog.Logger) *LoggingSink {
	return &LoggingSink{log: log}
}

func (s *LoggingSink) Observe(_ context.Context, o Observation) error {
	s.log.Info().
		Str("engagement_id", o.EngagementID.String()).
		Str("task_id", o.TaskID.String()).
		Str("role", o.Role).
		Str("kind", o.Kind).
		Str("tool_name", o.ToolName).
		Int("step", o.Step).
		Str("content", o.Content).
		Msg("react observation (dry-run)")
	return nil
}
```

- [ ] **Step 2: react.go**

```go
// internal/agent/runtime/react.go
package runtime

import (
	"context"
	"fmt"

	einoschema "github.com/cloudwego/eino/schema"

	"github.com/V3teran/liusha/internal/agent/llm"
	"github.com/V3teran/liusha/internal/worker"
)

// RunParams 包含一次 ReAct 循环所需的全部依赖。
type RunParams struct {
	Payload      worker.AgentRunPayload
	SystemPrompt string
	MaxSteps     int
	Gen          llm.Generator // 直接引用 llm 包的接口，唯一真源
	Sink         Sink
}

// Run 执行 ReAct 循环（Think → Act → Observe），Plan 5 骨架版：
//   - 无 ToolCalls（final answer）→ Sink.Observe(final_answer) → nil
//   - 有 ToolCalls → Sink.Observe(tool_call_deferred) + 追加占位 Tool 消息 → 继续
//   - 超 MaxSteps → 返回 error
func Run(ctx context.Context, p RunParams) error {
	if p.MaxSteps <= 0 {
		return fmt.Errorf("MaxSteps 必须 > 0")
	}
	msgs := []*einoschema.Message{
		{Role: einoschema.System, Content: p.SystemPrompt},
		{Role: einoschema.User, Content: buildUserPrompt(p.Payload)},
	}

	for step := 0; step < p.MaxSteps; step++ {
		resp, err := p.Gen.Generate(ctx, msgs)
		if err != nil {
			return fmt.Errorf("step %d LLM Generate 失败: %w", step, err)
		}

		if len(resp.ToolCalls) == 0 {
			// Final answer：走 Sink 观察点，Plan 5 只记日志
			return p.Sink.Observe(ctx, Observation{
				EngagementID: p.Payload.EngagementID,
				TaskID:       p.Payload.TaskID,
				Role:         p.Payload.Role,
				Kind:         "final_answer",
				Content:      resp.Content,
				Step:         step,
			})
		}

		msgs = append(msgs, resp)
		for _, tc := range resp.ToolCalls {
			// Plan 5 没有真实工具：记下 LLM 想调什么，并返回占位 observation
			if err := p.Sink.Observe(ctx, Observation{
				EngagementID: p.Payload.EngagementID,
				TaskID:       p.Payload.TaskID,
				Role:         p.Payload.Role,
				Kind:         "tool_call_deferred",
				ToolName:     tc.Function.Name,
				Content:      tc.Function.Arguments,
				Step:         step,
			}); err != nil {
				return fmt.Errorf("step %d Sink.Observe 失败: %w", step, err)
			}
			msgs = append(msgs, &einoschema.Message{
				Role:       einoschema.Tool,
				Content:    fmt.Sprintf("工具 %s 尚未在 Plan 5 中接入（骨架阶段），请基于现有上下文直接推理给出结论。", tc.Function.Name),
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
		}
	}

	return fmt.Errorf("超过最大步数 %d 未收敛", p.MaxSteps)
}

// buildUserPrompt 仅用 UUID/role/inputNodeIDs，不加载节点 payload。
// Plan 5 明确承认：LLM 看到只是 UUID 字符串，推理结果仅用于链路贯通（不落库、不污染图）。
// Plan 6 会替换为加载真实 node.Payload 作为上下文。
func buildUserPrompt(p worker.AgentRunPayload) string {
	if len(p.InputNodeIDs) == 0 {
		return fmt.Sprintf(
			"Role: %s\nEngagement: %s\n[Plan 5 骨架：无工具、无图上下文]\n请示范一次 Think→Act→Observe 流程。",
			p.Role, p.EngagementID,
		)
	}
	ids := make([]string, len(p.InputNodeIDs))
	for i, id := range p.InputNodeIDs {
		ids[i] = id.String()
	}
	return fmt.Sprintf(
		"Role: %s\nEngagement: %s\n候选输入节点 ID（无 payload）: %v\n[Plan 5 骨架：无工具、无图上下文]",
		p.Role, p.EngagementID, ids,
	)
}
```

- [ ] **Step 3: react_test.go**

```go
// internal/agent/runtime/react_test.go
package runtime_test

import (
	"context"
	"errors"
	"testing"

	einoschema "github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/agent/runtime"
	"github.com/V3teran/liusha/internal/worker"
)

type stubGenerator struct {
	calls    int
	response *einoschema.Message
	err      error
}

func (s *stubGenerator) Generate(_ context.Context, _ []*einoschema.Message) (*einoschema.Message, error) {
	s.calls++
	return s.response, s.err
}

type recordingSink struct {
	observations []runtime.Observation
	returnErr    error
}

func (s *recordingSink) Observe(_ context.Context, o runtime.Observation) error {
	s.observations = append(s.observations, o)
	return s.returnErr
}

func TestReAct_FinalAnswerEmitsObservation(t *testing.T) {
	gen := &stubGenerator{response: &einoschema.Message{
		Role:    einoschema.Assistant,
		Content: "未发现漏洞（骨架测试）",
	}}
	sink := &recordingSink{}

	err := runtime.Run(context.Background(), runtime.RunParams{
		Payload:      worker.AgentRunPayload{EngagementID: uuid.New(), TaskID: uuid.New(), Role: "web_recon"},
		SystemPrompt: "你是 web_recon",
		MaxSteps:     5,
		Gen:          gen,
		Sink:         sink,
	})

	require.NoError(t, err)
	assert.Equal(t, 1, gen.calls)
	require.Len(t, sink.observations, 1)
	assert.Equal(t, "final_answer", sink.observations[0].Kind)
	assert.Equal(t, "未发现漏洞（骨架测试）", sink.observations[0].Content)
}

func TestReAct_ToolCallEmitsDeferredObservation(t *testing.T) {
	// 第一次返回 tool call，第二次返回 final answer
	responses := []*einoschema.Message{
		{
			Role: einoschema.Assistant,
			ToolCalls: []*einoschema.ToolCall{{
				ID:   "tc1",
				Type: einoschema.ToolType("function"),
				Function: einoschema.FunctionCall{Name: "nuclei_scan", Arguments: `{"target":"x"}`},
			}},
		},
		{Role: einoschema.Assistant, Content: "分析完成"},
	}
	gen := &multiResponseGenerator{responses: responses}
	sink := &recordingSink{}

	err := runtime.Run(context.Background(), runtime.RunParams{
		Payload:      worker.AgentRunPayload{EngagementID: uuid.New(), TaskID: uuid.New(), Role: "web_recon"},
		SystemPrompt: "x",
		MaxSteps:     5,
		Gen:          gen,
		Sink:         sink,
	})

	require.NoError(t, err)
	require.Len(t, sink.observations, 2)
	assert.Equal(t, "tool_call_deferred", sink.observations[0].Kind)
	assert.Equal(t, "nuclei_scan", sink.observations[0].ToolName)
	assert.Equal(t, "final_answer", sink.observations[1].Kind)
}

func TestReAct_ExceedsMaxStepsReturnsError(t *testing.T) {
	gen := &stubGenerator{response: &einoschema.Message{
		Role: einoschema.Assistant,
		ToolCalls: []*einoschema.ToolCall{{
			ID: "tc", Type: einoschema.ToolType("function"),
			Function: einoschema.FunctionCall{Name: "nuclei_scan", Arguments: `{}`},
		}},
	}}
	sink := &recordingSink{}

	err := runtime.Run(context.Background(), runtime.RunParams{
		Payload:      worker.AgentRunPayload{EngagementID: uuid.New(), TaskID: uuid.New(), Role: "web_recon"},
		SystemPrompt: "x",
		MaxSteps:     3,
		Gen:          gen,
		Sink:         sink,
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "最大步数")
	assert.Equal(t, 3, gen.calls)
}

func TestReAct_LLMErrorPropagates(t *testing.T) {
	gen := &stubGenerator{err: errors.New("network broken")}
	err := runtime.Run(context.Background(), runtime.RunParams{
		Payload:      worker.AgentRunPayload{TaskID: uuid.New()},
		SystemPrompt: "x",
		MaxSteps:     5,
		Gen:          gen,
		Sink:         &recordingSink{},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "LLM Generate 失败")
}

type multiResponseGenerator struct {
	responses []*einoschema.Message
	idx       int
}

func (m *multiResponseGenerator) Generate(_ context.Context, _ []*einoschema.Message) (*einoschema.Message, error) {
	if m.idx >= len(m.responses) {
		return nil, errors.New("exhausted")
	}
	r := m.responses[m.idx]
	m.idx++
	return r, nil
}
```

- [ ] **Step 4: 运行测试**

```bash
go test -race ./internal/agent/runtime/
```
期望 4 个测试全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/agent/runtime/
git commit -m "feat(agent/runtime): ReAct loop with Sink interface + LoggingSink"
```

---

## Task 5: cmd/agent-worker 二进制 + Dockerfile + Compose

**Files:**
- Create: `cmd/agent-worker/main.go`
- Create: `cmd/agent-worker/Dockerfile`
- Modify: `deployments/docker-compose.yml`
- Modify: `.github/workflows/ci.yml`, `Makefile`

- [ ] **Step 1: main.go（dry-run 骨架：Logging Sink + healthz）**

```go
// cmd/agent-worker/main.go
// agent-worker 启动 Asynq Server，消费 agent:run 任务，执行 ReAct 循环。
// Plan 5 骨架：Sink=LoggingSink（不写 graph_node），仅暴露 /healthz 活性探针。
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"

	"github.com/V3teran/liusha/internal/agent/llm"
	"github.com/V3teran/liusha/internal/agent/runtime"
	"github.com/V3teran/liusha/internal/config"
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

	if cfg.LLM.APIKey == "" {
		log.Fatal().Msg("LIUSHA_DEEPSEEK_API_KEY 未设置")
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

	gen, err := llm.NewDeepSeek(cfg.LLM.APIKey, cfg.LLM.BaseURL, cfg.LLM.DefaultModel, cfg.LLM.MaxTokens)
	if err != nil {
		log.Fatal().Err(err).Msg("初始化 Eino DeepSeek 失败")
	}

	// Plan 5：不落库，仅结构化日志；Plan 6 替换为 graphSink。
	sink := runtime.NewLoggingSink(log.Logger)

	reactRunner := &reactRunnerImpl{cfg: cfg, gen: gen, sink: sink}

	mux := asynq.NewServeMux()
	mux.Handle(worker.TypeAgentRun, worker.HandleAgentRun(worker.HandlerDeps{Runner: reactRunner}))

	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: cfg.RedisAddr},
		asynq.Config{
			Queues:      map[string]int{"critical": 6, "default": 3},
			Concurrency: 5,
		},
	)

	// 健康探针（Plan 5 骨架仅 /healthz，Prometheus 延后到 Plan 9 E2E）
	httpSrv := startHealthzServer(":9090")
	defer func() {
		shutdownCtx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	log.Info().Str("redis", cfg.RedisAddr).Msg("agent-worker 启动")

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(mux) }()

	select {
	case <-ctx.Done():
		srv.Shutdown()
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal().Err(err).Msg("Asynq server 异常退出")
		}
	}
}

type reactRunnerImpl struct {
	cfg  config.Config
	gen  llm.Generator
	sink runtime.Sink
}

func (r *reactRunnerImpl) Run(ctx context.Context, p worker.AgentRunPayload) error {
	return runtime.Run(ctx, runtime.RunParams{
		Payload:      p,
		SystemPrompt: fmt.Sprintf("你是 %s，专注于渗透测试中的漏洞检测与验证。当前为骨架阶段，工具尚未接入。", p.Role),
		MaxSteps:     r.cfg.LLM.MaxSteps,
		Gen:          r.gen,
		Sink:         r.sink,
	})
}

// startHealthzServer 起一个最小 HTTP server，暴露 /healthz 活性探针。
func startHealthzServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("healthz server 异常")
		}
	}()
	return srv
}
```

- [ ] **Step 2: Dockerfile**

```dockerfile
# cmd/agent-worker/Dockerfile
# syntax=docker/dockerfile:1
FROM golang:stable-alpine AS builder
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
EXPOSE 9090
ENTRYPOINT ["/app/agent-worker"]
```

- [ ] **Step 3: docker-compose.yml 追加服务**

```yaml
  # agent-worker：消费 Asynq 队列，执行 ReAct 循环（Plan 5 骨架：不落 graph_node，仅日志）。
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
      LIUSHA_DEEPSEEK_API_KEY: "${LIUSHA_DEEPSEEK_API_KEY}"
    depends_on:
      postgres: { condition: service_healthy }
      redis:    { condition: service_healthy }
    ports:
      - "9090:9090"  # /healthz
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:9090/healthz"]
      interval: 10s
      timeout: 3s
      retries: 3
    profiles:
      - agent
```

- [ ] **Step 4: Makefile**

```makefile
.PHONY: build-agent-worker
build-agent-worker:
	docker build -f cmd/agent-worker/Dockerfile -t liusha/agent-worker .
```

- [ ] **Step 5: CI**

在 `.github/workflows/ci.yml` 的 build job 末尾追加：

```yaml
      - name: build agent-worker image
        run: docker build -f cmd/agent-worker/Dockerfile -t liusha/agent-worker:ci .
```

- [ ] **Step 6: 全量验收**

```bash
go vet ./...
gofmt -l $(find . -name '*.go' -not -path './vendor/*')
go test -race -short ./...
go build ./...
```
gofmt 无输出，测试全 PASS。

- [ ] **Step 7: 提交**

```bash
git add cmd/agent-worker/ deployments/docker-compose.yml .github/workflows/ci.yml Makefile
git commit -m "feat(agent-worker): cmd binary with healthz, compose wiring"
git tag v0.5.0-plan5
```

---

## Task 6: ~~Metrics + Healthz 骨架~~ — **已撤回（DEPRECATED）**

**状态：** 2026-04-20 用户驳回，本 Plan 不实现。

**撤回原因：** 骨架阶段（Plan 5）没有 Prometheus scraper 消费端，引入 `prometheus/client_golang` + `/metrics` 端点属于 YAGNI 过度设计。`LoggingSink` 输出的结构化 zerolog 日志（role / status / step / duration）已覆盖当前可观测需求；真要做指标聚合时再引入。

**延后到：** Plan 9 端到端上线阶段——届时有真实 scrape target 和告警规则需求，再决定是继续用 Prometheus 还是直接对接已有可观测平台（OTLP / Loki / etc）。

**回滚动作：** 删除 `internal/metrics/`、`cmd/agent-worker/main.go` 的 `/metrics` handler 与 `promhttp` 导入、`go.mod` 的 `prometheus/client_golang` 依赖；`v0.5.0-plan5` tag 改到 Task 5 完成点打。

---

## Self-Review

### Spec coverage

| Spec 要求 | 覆盖任务 |
|---|---|
| Asynq 任务队列基础设施 + 幂等 | Task 1 |
| Config LLM 字段（sonnet-4-5, maxSteps=5） | Task 2 |
| Eino Generator 接口单一真源 | Task 3 |
| ReAct 循环 Think→Act→Observe + Sink 抽象 | Task 4 |
| LoggingSink（Plan 5 不落 graph_node） | Task 4 |
| cmd/agent-worker 二进制 + Dockerfile + compose | Task 5 |
| /healthz 活性探针 | Task 5 |
| ~~Prometheus 指标~~ | ~~Task 6~~ **已撤回（YAGNI，延后 Plan 9）** |
| CI build step | Task 5 |

### 本期 dry-run scope 对比 v1 原计划

| 项目 | 原计划 | 本期（骨架） | 延后到 |
|---|---|---|---|
| graph_node 落库 | NodeSink 真写 PG | LoggingSink 仅日志 | Plan 6 |
| DockerRunner 工具接入 | 未接入 | 未装配（Role 拿不到 tool handle） | Plan 6 |
| EngagementID 闭包穿透 | nodeSink 内 uuid.Nil 骨架 | Sink 接口直接带 payload 字段 | — |
| Generator 接口 | llm + runtime 各定义一份 | llm 唯一定义 | — |
| Enqueue 幂等 | 注释说 Unique 但代码没写 | asynq.TaskID + asynq.Unique 真落实 | — |
| MaxSteps | 15 | 5 | Plan 6 可加大 |
| 模型 ID | claude-sonnet-4-6（不存在） | deepseek-chat（Plan 5 骨架单值） | Plan 6 per-role 映射 |
| 可观测性 | 无 | /healthz + LoggingSink 结构化日志（Prometheus 延后到 Plan 9） | Plan 9 |

### Placeholder scan

- 没有任何 `uuid.Nil` 的反模式 `var _ = uuid.Nil`。
- 没有 `buildFindingNode` 返回 interface{} 的伪实现。
- 所有 commit 点的代码都是可编译通过的完整版本。

### Type consistency

- `llm.Generator` 在 llm 包唯一定义，runtime 直接 import；stubGenerator 满足同一接口。
- `runtime.Sink` 接口抽象对外观察点，LoggingSink 是默认实现；Plan 6 新增 GraphSink 时零影响其他组件。
- `worker.Runner` 接口在 worker 包定义，cmd/agent-worker/main.go 的 `reactRunnerImpl` 实现。

### 安全自查

- `LIUSHA_DEEPSEEK_API_KEY` 从 env 读，不入代码、不入 config.yaml。
- `/healthz` 暴露 9090 端口，骨架阶段仅作 k8s/compose liveness probe；Prometheus scrape 端延后到 Plan 9 再讨论暴露策略。
- Asynq Unique 防重入，LLM 费用不会因 retry storm 爆炸。
