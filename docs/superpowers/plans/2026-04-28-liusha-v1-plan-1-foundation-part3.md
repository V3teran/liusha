# Liusha v1 Plan 1 (part 3): Agent Runtime + cmd 装配 + Smoke

> 续 part2。本文件含 T17-T32：LLM provider、Pricing、Instrument 装饰器、ActionRegistry、AgentRuntime（ReAct + Observer + Budget；Reflexion 已删，由 T23.5 Observer 替代）、通用 actions、skill loader、worker、httpapi、cmd/{api,agent-worker,vulnapp}、smoke。

---

## 黑客松借鉴增量（part 3 范围，2026-04-29 加入；详见 docs/hks2.md）

| Task | 改动 |
|---|---|
| T19（factory） | 暴露 `For(routeKey string) Generator`：按 `config.llm.routes` 路由到对应 provider；未列表的 route 走 `default_provider` |
| T21（Instrument） | meta 加 `route_key` 字段；写 `llm_call.role`（值 = route_key，便于按 observer/distill 维度统计成本） |
| **T21.5（新增）** | `internal/agent/llm/router.go` + `retry.go`：装饰器外层；retry 按 spec §8.5 错误码退避表；耗尽切 fallback_provider；测试用 mock provider 注入 429/529 |
| T22（registry） | 加中间件链支持：`type ActionMiddleware func(next ActionExecutor) ActionExecutor`；`Registry.Use(mw...)` 顺序套；保留 `Execute(ctx, name, args)` 入口 |
| **T22.5（新增）** | 三个 middleware 实现：`middleware/result_compress.go`（>2KB 落盘 `engagement-store/<eid>/`）、`middleware/loop_detect.go`（四维 hash 滑窗 N=10、连续 ≥3 次同 hash 触发 `loop_detector_abort`）、`middleware/done_validate.go`（action=done 时调对应 Skill 的 DoneValidator，不通过则不算 done） |
| T23（ReAct） | 删 Reflexion 代码 + 测试；ReAct 主循环加：①每 5 步调 `observer.Evaluate`；②action 错误（含 `loop_detector_abort` / `observer_abort`）传播到 `task.result.terminate_by`；③done 经 middleware 校验失败时不停，注入 user message 后继续；④`terminate_by` 取值集扩到 `done | budget | watchdog | observer_abort | loop_detector_abort | engagement_aborted | error | done_force` |
| **T23.5（新增）** | `internal/agent/runtime/observer.go`：`Observer.Evaluate(ctx, window []StepRecord) Verdict`，调 `llm.For("observer")`；返回 `keep_going / steer_with_hint / abort_low_value`；steer 时 hint 通过 `engagement.AppendHint` 落盘并注入下一轮 user msg。`distill.go`：订阅 finding store 的 `OnSaved` hook，调 `llm.For("distill")` 总结成 hint，调 `engagement.AppendHint` |
| T24（通用 actions） | 删 `read_memory / write_memory`；新增 `read_state / write_fact / write_idea / write_hint`；`done` action 改为系统校验式：注册到 ActionRegistry 时挂一个 `DoneValidator`（默认 `AlwaysOK`，Skill 可覆盖）。`write_finding` 在写库后调 finding.OnSaved 触发 distill |
| T25（skill loader） | frontmatter 新增 2 个字段：`done_validator: <key>`（在 ActionRegistry 找对应 Validator，找不到报错）和 `cognitive_map: <path>`（loader 读文件，校验是否含 6 槽位）；`docs/skills/_template/cognitive_map.md` 由 plan 2 创建 |

**Task 编号规则**：T21.5/T22.5/T23.5 三个新 Task 直接在原 Task 后插入；不重排原编号，便于已有 implementer 工单继续生效。

实现端 patch 提示：
- T19 factory：`type Factory struct { providers map[string]Generator; routes map[string]string; defaultKey, lightKey, fallbackKey string }`；`For(role)` 先查 `routes[role]`，再 fallback 到 default
- T21 Instrument 装饰器：在 `CallMeta` 加 `RouteKey string` 字段；写 llm_call 时 `role = meta.RouteKey`
- T21.5 router 包结构：`Router{factory *Factory}`；`Router.For(role) Generator` 返回经 `RetryDecorator` 套过的 Generator；`RetryDecorator` 按 §8.5 表执行重试 + fallback
- T22.5 middleware：result_compress 落盘后 result `{path:"engagement-store/<eid>/<action>-<seq>.txt", size, sha256, snippet}`；loop_detect 用 sha256(action_name+args_canonical+caller_skill+stepIdx%10) 维护 ring buffer
- T23 主循环最简伪码：

```go
for step := 0; ; step++ {
    if budget.Exceeded() || engagement.Status() != active { break }
    if step > 0 && step%5 == 0 {
        if v := observer.Evaluate(ctx, win); v.Decision == "abort_low_value" {
            term = "observer_abort"; break
        } else if v.Decision == "steer_with_hint" {
            engagement.AppendHint(ctx, eid, Hint{From: "observer", Content: v.Hint, Priority: 9})
            messages = append(messages, UserMsg("提示："+v.Hint))
        }
    }
    out := llm.For("react.main").Chat(ctx, messages)
    action, args := parse(out)
    obs, err := registry.Execute(ctx, action, args)  // 经过 middleware 链
    if err != nil { /* 处理 loop_detector_abort / done validate fail / etc */ }
    if action == "done" && err == nil { term = "done"; break }
    state.Append(...)
}
```

- T24 done action：调用前由 done_validate middleware 拦截；middleware 内部从 ActionRegistry 查当前 Skill 注册的 `DoneValidator`，调 `CanDone(state, args) (ok, missing)`；不 ok 则把 `missing` 列表 wrap 成 `errDoneNotReady`，runtime 收到后注入 user msg 不停
- T25 cognitive_map 校验：读 yaml frontmatter 中 `cognitive_map` 路径；读该文件；用正则 `^##\s+\d+\.\s+` 数标题数 ≥ 6（6 槽位）；不齐全报错并在 logger.Warn

---

## Task 17: internal/agent/llm — Generator 接口 + DeepSeek 适配

**Files:**
- Create: `internal/agent/llm/types.go`
- Create: `internal/agent/llm/deepseek.go`
- Create: `internal/agent/llm/types_test.go`

- [ ] **Step 1: 写失败测试 `internal/agent/llm/types_test.go`**

```go
package llm

import "testing"

func TestUsage_Add(t *testing.T) {
	a := Usage{InTokens: 100, OutTokens: 50, CachedTokens: 10}
	b := Usage{InTokens: 200, OutTokens: 80, CachedTokens: 20}
	got := a.Add(b)
	want := Usage{InTokens: 300, OutTokens: 130, CachedTokens: 30}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}
```

- [ ] **Step 2: 实现 `internal/agent/llm/types.go`**

```go
package llm

import (
	"context"
	"encoding/json"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role            `json:"role"`
	Content    string          `json:"content,omitempty"`
	Name       string          `json:"name,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type Usage struct {
	InTokens     int
	OutTokens    int
	CachedTokens int
}

func (u Usage) Add(o Usage) Usage {
	return Usage{
		InTokens:     u.InTokens + o.InTokens,
		OutTokens:    u.OutTokens + o.OutTokens,
		CachedTokens: u.CachedTokens + o.CachedTokens,
	}
}

type Result struct {
	Content      string
	ToolCalls    []ToolCall
	Usage        Usage
	FinishReason string
	Provider     string
	Model        string
}

type Generator interface {
	Generate(ctx context.Context, messages []Message, tools []ToolSchema) (Result, error)
	Provider() string
	Model() string
}
```

- [ ] **Step 3: 实现 `internal/agent/llm/deepseek.go`**

> 设计要点：`Generator` 不安全跨 goroutine 并发使用（eino `BindTools` 改变内部状态）。**调用方负责每个任务建一个 Generator 实例**。`BindTools` 在构造时一次性绑定，不在 `Generate` 里反复调用。

```go
package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/deepseek"
	einoschema "github.com/cloudwego/eino/schema"
)

type DeepSeekConfig struct {
	BaseURL   string
	Model     string
	APIKey    string
	MaxTokens int
}

type deepseekGen struct {
	model    *deepseek.ChatModel
	provider string
	modelID  string
}

// NewDeepSeek 创建一个 DeepSeek Generator。tools 在此一次性绑定，后续 Generate 调用复用。
// Generator 不安全跨 goroutine；每个 task handler 建独立实例。
func NewDeepSeek(ctx context.Context, c DeepSeekConfig, tools []ToolSchema) (Generator, error) {
	cm, err := deepseek.NewChatModel(ctx, &deepseek.ChatModelConfig{
		BaseURL:   c.BaseURL,
		Model:     c.Model,
		APIKey:    c.APIKey,
		MaxTokens: c.MaxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("new deepseek: %w", err)
	}
	if len(tools) > 0 {
		einoTools, err := toEinoTools(tools)
		if err != nil {
			return nil, err
		}
		if err := cm.BindTools(einoTools); err != nil {
			return nil, fmt.Errorf("bind tools: %w", err)
		}
	}
	return &deepseekGen{model: cm, provider: "deepseek", modelID: c.Model}, nil
}

func (g *deepseekGen) Provider() string { return g.provider }
func (g *deepseekGen) Model() string    { return g.modelID }

func (g *deepseekGen) Generate(ctx context.Context, msgs []Message, _ []ToolSchema) (Result, error) {
	einoMsgs := toEinoMessages(msgs)
	out, err := g.model.Generate(ctx, einoMsgs)
	if err != nil {
		return Result{}, fmt.Errorf("deepseek generate: %w", err)
	}
	return fromEinoMessage(out, g.provider, g.modelID), nil
}

func toEinoMessages(in []Message) []*einoschema.Message {
	out := make([]*einoschema.Message, len(in))
	for i, m := range in {
		em := &einoschema.Message{
			Role:       einoschema.RoleType(m.Role),
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			em.ToolCalls = append(em.ToolCalls, einoschema.ToolCall{
				ID: tc.ID,
				Function: einoschema.FunctionCall{
					Name:      tc.Name,
					Arguments: string(tc.Arguments),
				},
			})
		}
		out[i] = em
	}
	return out
}

func toEinoTools(tools []ToolSchema) ([]*einoschema.ToolInfo, error) {
	out := make([]*einoschema.ToolInfo, len(tools))
	for i, t := range tools {
		var params map[string]any
		if err := json.Unmarshal(t.Parameters, &params); err != nil {
			return nil, fmt.Errorf("tool %s params: %w", t.Name, err)
		}
		out[i] = &einoschema.ToolInfo{
			Name:        t.Name,
			Desc:        t.Description,
			ParamsOneOf: einoschema.NewParamsOneOfByOpenAPIV3(params),
		}
	}
	return out, nil
}

func fromEinoMessage(m *einoschema.Message, provider, model string) Result {
	res := Result{
		Content:      m.Content,
		Provider:     provider,
		Model:        model,
		FinishReason: string(m.ResponseMeta.FinishReason),
	}
	for _, tc := range m.ToolCalls {
		res.ToolCalls = append(res.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}
	if m.ResponseMeta.Usage != nil {
		res.Usage = Usage{
			InTokens:     m.ResponseMeta.Usage.PromptTokens,
			OutTokens:    m.ResponseMeta.Usage.CompletionTokens,
			CachedTokens: m.ResponseMeta.Usage.PromptTokensDetails.CachedTokens,
		}
	}
	return res
}
```

- [ ] **Step 4: 跑测试 + Commit**

```bash
go test ./internal/agent/llm/... -race
go vet ./internal/agent/llm/...
git add internal/agent/llm
git commit -m "feat(llm): Generator 接口 + DeepSeek 适配（基于 Eino）"
```

---

## Task 18: internal/agent/llm — Claude 适配（eino-ext）

**Files:**
- Create: `internal/agent/llm/claude.go`
- Modify: `go.mod`（加 `github.com/cloudwego/eino-ext/components/model/claude`）

> 用 eino-ext 官方 claude adapter，不手撸 HTTP。

- [ ] **Step 1: 加依赖**

```bash
go get github.com/cloudwego/eino-ext/components/model/claude@latest
go mod tidy
```

- [ ] **Step 2: 实现 `internal/agent/llm/claude.go`**

```go
package llm

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/claude"
)

type ClaudeConfig struct {
	BaseURL   string
	Model     string
	APIKey    string
	MaxTokens int
}

type claudeGen struct {
	model   *claude.ChatModel
	modelID string
}

// NewClaude 同 NewDeepSeek：tools 一次性绑定，Generator 非线程安全。
func NewClaude(ctx context.Context, c ClaudeConfig, tools []ToolSchema) (Generator, error) {
	cfg := &claude.Config{
		APIKey:    c.APIKey,
		Model:     c.Model,
		MaxTokens: c.MaxTokens,
	}
	// claude.Config.BaseURL 是 *string；空时不设，让 SDK 走默认 endpoint
	if c.BaseURL != "" {
		bu := c.BaseURL
		cfg.BaseURL = &bu
	}
	cm, err := claude.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new claude: %w", err)
	}
	if len(tools) > 0 {
		einoTools, err := toEinoTools(tools)
		if err != nil {
			return nil, err
		}
		if err := cm.BindTools(einoTools); err != nil {
			return nil, fmt.Errorf("bind tools: %w", err)
		}
	}
	return &claudeGen{model: cm, modelID: c.Model}, nil
}

func (g *claudeGen) Provider() string { return "anthropic" }
func (g *claudeGen) Model() string    { return g.modelID }

func (g *claudeGen) Generate(ctx context.Context, msgs []Message, _ []ToolSchema) (Result, error) {
	out, err := g.model.Generate(ctx, toEinoMessages(msgs))
	if err != nil {
		return Result{}, fmt.Errorf("claude generate: %w", err)
	}
	return fromEinoMessage(out, "anthropic", g.modelID), nil
}
```

> 已核对 `claude.Config` 字段（2026-04 main 分支）：`APIKey string` / `Model string` / `MaxTokens int` / `BaseURL *string` / `AuthToken string`，构造函数 `NewChatModel(ctx, *Config) (*ChatModel, error)`。如版本漂移导致字段不一致，按真实 godoc 调整。

- [ ] **Step 3: 编译 + Commit**

```bash
go build ./internal/agent/llm/...
git add internal/agent/llm/claude.go go.mod go.sum
git commit -m "feat(llm): Claude 适配（eino-ext/components/model/claude）"
```

---

## Task 19: internal/agent/llm — OpenAI / Moonshot / Qwen 真实 adapter + factory

**Files:**
- Create: `internal/agent/llm/openai.go`
- Create: `internal/agent/llm/qwen.go`
- Create: `internal/agent/llm/factory.go`
- Create: `internal/agent/llm/factory_test.go`
- Modify: `go.mod`（加 `eino-ext/components/model/openai` 和 `qwen`）

> Moonshot OpenAI-compatible，复用 openai adapter + 自定义 base_url，不需要单独 adapter。

- [ ] **Step 1: 加依赖**

```bash
go get github.com/cloudwego/eino-ext/components/model/openai@latest
go get github.com/cloudwego/eino-ext/components/model/qwen@latest
go mod tidy
```

- [ ] **Step 2: 实现 `internal/agent/llm/openai.go`**

```go
package llm

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/openai"
)

type OpenAIConfig struct {
	BaseURL   string
	Model     string
	APIKey    string
	MaxTokens int
}

type openaiGen struct {
	model    *openai.ChatModel
	provider string // "openai" 或 "moonshot"（OpenAI-compatible 共用）
	modelID  string
}

func NewOpenAI(ctx context.Context, c OpenAIConfig, tools []ToolSchema) (Generator, error) {
	return newOpenAILike(ctx, "openai", c, tools)
}

// NewMoonshot Moonshot 是 OpenAI-compatible，走 openai adapter + 自定义 base_url。
func NewMoonshotViaOpenAI(ctx context.Context, c OpenAIConfig, tools []ToolSchema) (Generator, error) {
	return newOpenAILike(ctx, "moonshot", c, tools)
}

func newOpenAILike(ctx context.Context, providerName string, c OpenAIConfig, tools []ToolSchema) (Generator, error) {
	cfg := &openai.ChatModelConfig{
		BaseURL: c.BaseURL,
		Model:   c.Model,
		APIKey:  c.APIKey,
	}
	// openai.ChatModelConfig.MaxTokens 是 *int
	if c.MaxTokens > 0 {
		mt := c.MaxTokens
		cfg.MaxTokens = &mt
	}
	cm, err := openai.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new %s: %w", providerName, err)
	}
	if len(tools) > 0 {
		einoTools, err := toEinoTools(tools)
		if err != nil {
			return nil, err
		}
		if err := cm.BindTools(einoTools); err != nil {
			return nil, fmt.Errorf("bind tools: %w", err)
		}
	}
	return &openaiGen{model: cm, provider: providerName, modelID: c.Model}, nil
}

func (g *openaiGen) Provider() string { return g.provider }
func (g *openaiGen) Model() string    { return g.modelID }

func (g *openaiGen) Generate(ctx context.Context, msgs []Message, _ []ToolSchema) (Result, error) {
	out, err := g.model.Generate(ctx, toEinoMessages(msgs))
	if err != nil {
		return Result{}, fmt.Errorf("%s generate: %w", g.provider, err)
	}
	return fromEinoMessage(out, g.provider, g.modelID), nil
}
```

- [ ] **Step 3: 实现 `internal/agent/llm/qwen.go`**

```go
package llm

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/qwen"
)

type QwenConfig struct {
	BaseURL   string
	Model     string
	APIKey    string
	MaxTokens int
}

type qwenGen struct {
	model   *qwen.ChatModel
	modelID string
}

func NewQwen(ctx context.Context, c QwenConfig, tools []ToolSchema) (Generator, error) {
	cfg := &qwen.ChatModelConfig{
		BaseURL: c.BaseURL,
		Model:   c.Model,
		APIKey:  c.APIKey,
	}
	// qwen.ChatModelConfig.MaxTokens 是 *int（与 openai 同）
	if c.MaxTokens > 0 {
		mt := c.MaxTokens
		cfg.MaxTokens = &mt
	}
	cm, err := qwen.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new qwen: %w", err)
	}
	if len(tools) > 0 {
		einoTools, err := toEinoTools(tools)
		if err != nil {
			return nil, err
		}
		if err := cm.BindTools(einoTools); err != nil {
			return nil, fmt.Errorf("bind tools: %w", err)
		}
	}
	return &qwenGen{model: cm, modelID: c.Model}, nil
}

func (g *qwenGen) Provider() string { return "qwen" }
func (g *qwenGen) Model() string    { return g.modelID }

func (g *qwenGen) Generate(ctx context.Context, msgs []Message, _ []ToolSchema) (Result, error) {
	out, err := g.model.Generate(ctx, toEinoMessages(msgs))
	if err != nil {
		return Result{}, fmt.Errorf("qwen generate: %w", err)
	}
	return fromEinoMessage(out, "qwen", g.modelID), nil
}
```

- [ ] **Step 4: 实现统一 factory `internal/agent/llm/factory.go`**

```go
package llm

import (
	"context"
	"fmt"
	"os"

	"github.com/V3teran/liusha/internal/config"
)

// BuildProvider 根据 config 构建 Generator。tools 一次绑定（每个 task 调一次）。
func BuildProvider(ctx context.Context, cfg config.Config, providerName string, tools []ToolSchema) (Generator, error) {
	pc, ok := cfg.Providers[providerName]
	if !ok {
		return nil, fmt.Errorf("provider %q not in config", providerName)
	}
	apiKey := os.Getenv(pc.APIKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("env %s empty", pc.APIKeyEnv)
	}
	switch providerName {
	case "deepseek":
		return NewDeepSeek(ctx, DeepSeekConfig{
			BaseURL: pc.BaseURL, Model: pc.DefaultModel, APIKey: apiKey, MaxTokens: pc.MaxTokens,
		}, tools)
	case "anthropic":
		return NewClaude(ctx, ClaudeConfig{
			BaseURL: pc.BaseURL, Model: pc.DefaultModel, APIKey: apiKey, MaxTokens: pc.MaxTokens,
		}, tools)
	case "openai":
		return NewOpenAI(ctx, OpenAIConfig{
			BaseURL: pc.BaseURL, Model: pc.DefaultModel, APIKey: apiKey, MaxTokens: pc.MaxTokens,
		}, tools)
	case "moonshot":
		return NewMoonshotViaOpenAI(ctx, OpenAIConfig{
			BaseURL: pc.BaseURL, Model: pc.DefaultModel, APIKey: apiKey, MaxTokens: pc.MaxTokens,
		}, tools)
	case "qwen":
		return NewQwen(ctx, QwenConfig{
			BaseURL: pc.BaseURL, Model: pc.DefaultModel, APIKey: apiKey, MaxTokens: pc.MaxTokens,
		}, tools)
	}
	return nil, fmt.Errorf("unknown provider %q", providerName)
}
```

- [ ] **Step 5: 写 factory 表驱动测试 `internal/agent/llm/factory_test.go`**

```go
package llm

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/config"
)

func TestBuildProvider_KnownProvidersBuildOK(t *testing.T) {
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"deepseek":  {BaseURL: "https://api.deepseek.com", DefaultModel: "deepseek-chat", APIKeyEnv: "DEEPSEEK_API_KEY", MaxTokens: 4096},
			"anthropic": {BaseURL: "https://api.anthropic.com", DefaultModel: "claude-sonnet-4-6", APIKeyEnv: "ANTHROPIC_API_KEY", MaxTokens: 8192},
			"openai":    {BaseURL: "https://api.openai.com/v1", DefaultModel: "gpt-4o", APIKeyEnv: "OPENAI_API_KEY", MaxTokens: 4096},
			"moonshot":  {BaseURL: "https://api.moonshot.cn/v1", DefaultModel: "kimi-k2-0905-preview", APIKeyEnv: "MOONSHOT_API_KEY", MaxTokens: 4096},
			"qwen":      {BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", DefaultModel: "qwen3-max", APIKeyEnv: "QWEN_API_KEY", MaxTokens: 4096},
		},
	}
	for _, p := range []string{"deepseek", "anthropic", "openai", "moonshot", "qwen"} {
		t.Run(p, func(t *testing.T) {
			t.Setenv(cfg.Providers[p].APIKeyEnv, "fake-key")
			g, err := BuildProvider(context.Background(), cfg, p, nil)
			if err != nil {
				t.Fatalf("build %s: %v", p, err)
			}
			if g.Model() == "" {
				t.Fatalf("%s model empty", p)
			}
		})
	}
}

func TestBuildProvider_MissingKey(t *testing.T) {
	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"deepseek": {DefaultModel: "deepseek-chat", APIKeyEnv: "DEEPSEEK_API_KEY"},
	}}
	t.Setenv("DEEPSEEK_API_KEY", "")
	if _, err := BuildProvider(context.Background(), cfg, "deepseek", nil); err == nil {
		t.Fatal("expected error on empty key")
	}
}
```

- [ ] **Step 6: 跑测试 + Commit**

```bash
go test ./internal/agent/llm/... -race
git add internal/agent/llm go.mod go.sum
git commit -m "feat(llm): OpenAI/Moonshot/Qwen 真实 adapter（eino-ext）+ 统一 BuildProvider factory"
```

---

## Task 20: internal/observability — Pricing 表

**Files:**
- Create: `internal/observability/pricing.go`
- Create: `internal/observability/pricing_test.go`

- [ ] **Step 1: 写失败测试**

```go
package observability

import (
	"math"
	"testing"

	"github.com/V3teran/liusha/internal/agent/llm"
)

func TestEstimate_DeepSeek(t *testing.T) {
	got := DefaultPricing.Estimate("deepseek", "deepseek-chat", llm.Usage{
		InTokens: 1_000_000, OutTokens: 1_000_000,
	})
	if math.Abs(got-(0.27+1.10)) > 1e-6 {
		t.Fatalf("got %v want %v", got, 0.27+1.10)
	}
}

func TestEstimate_Anthropic_CacheDiscount(t *testing.T) {
	got := DefaultPricing.Estimate("anthropic", "claude-sonnet-4-6", llm.Usage{
		InTokens: 1_000_000, CachedTokens: 1_000_000,
	})
	// 1M billed at 3.00, plus cached 1M at 3.00*0.30 = 3 + 0.9 = 3.9
	if math.Abs(got-3.9) > 1e-6 {
		t.Fatalf("got %v want %v", got, 3.9)
	}
}

func TestEstimate_UnknownReturnsZero(t *testing.T) {
	if v := DefaultPricing.Estimate("unknown", "x", llm.Usage{InTokens: 100}); v != 0 {
		t.Fatalf("expected 0 for unknown provider, got %v", v)
	}
}
```

- [ ] **Step 2: 实现 `internal/observability/pricing.go`**

```go
package observability

import "github.com/V3teran/liusha/internal/agent/llm"

type ModelPrice struct {
	InputPerMUSD  float64
	OutputPerMUSD float64
	CacheDiscount float64
}

type Pricing struct {
	table map[string]ModelPrice
}

var DefaultPricing = Pricing{
	table: map[string]ModelPrice{
		"deepseek/deepseek-chat":      {0.27, 1.10, 0.10},
		"anthropic/claude-sonnet-4-6": {3.00, 15.00, 0.30},
		"anthropic/claude-haiku-4-5":  {1.00, 5.00, 0.30},
	},
}

func (p Pricing) Estimate(provider, model string, u llm.Usage) float64 {
	mp, ok := p.table[provider+"/"+model]
	if !ok {
		return 0
	}
	const M = 1_000_000.0
	in := float64(u.InTokens) / M * mp.InputPerMUSD
	out := float64(u.OutTokens) / M * mp.OutputPerMUSD
	cached := float64(u.CachedTokens) / M * mp.InputPerMUSD * mp.CacheDiscount
	return in + out + cached
}
```

- [ ] **Step 3: 跑测试 + Commit**

```bash
go test ./internal/observability/... -race
git add internal/observability
git commit -m "feat(observability): pricing 表 + Estimate（含 cache 折扣）"
```

---

## Task 21: internal/agent/llm/instrument — Generator 装饰器

**Files:**
- Create: `internal/agent/llm/instrument.go`
- Create: `internal/agent/llm/instrument_test.go`

- [ ] **Step 1: 写失败测试**

```go
package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/V3teran/liusha/internal/llmcall"
)

type fakeGen struct {
	res Result
	err error
}

func (f *fakeGen) Generate(_ context.Context, _ []Message, _ []ToolSchema) (Result, error) {
	return f.res, f.err
}
func (f *fakeGen) Provider() string { return "deepseek" }
func (f *fakeGen) Model() string    { return "deepseek-chat" }

type fakeSink struct{ calls []llmcall.Call }

func (s *fakeSink) Insert(_ context.Context, c llmcall.Call) (int64, error) {
	s.calls = append(s.calls, c)
	return int64(len(s.calls)), nil
}

type fakePricing struct{}

func (fakePricing) Estimate(provider, model string, u Usage) float64 { return 0.001 }

func TestInstrument_RecordsSuccess(t *testing.T) {
	inner := &fakeGen{res: Result{
		Content: "hi", Provider: "deepseek", Model: "deepseek-chat",
		Usage: Usage{InTokens: 100, OutTokens: 30}, FinishReason: "stop",
	}}
	sink := &fakeSink{}
	tid, eid := "t1", "e1"
	g := Instrument(inner, sink, CallMeta{TaskID: &tid, EngagementID: &eid}, fakePricing{})
	if _, err := g.Generate(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(sink.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(sink.calls))
	}
	c := sink.calls[0]
	if c.Provider != "deepseek" || c.InTokens != 100 || c.CostUSD != 0.001 || c.Error != "" {
		t.Fatalf("unexpected: %+v", c)
	}
}

func TestInstrument_RecordsError(t *testing.T) {
	inner := &fakeGen{err: errors.New("boom")}
	sink := &fakeSink{}
	g := Instrument(inner, sink, CallMeta{}, fakePricing{})
	if _, err := g.Generate(context.Background(), nil, nil); err == nil {
		t.Fatal("expected error")
	}
	if len(sink.calls) != 1 || sink.calls[0].Error == "" {
		t.Fatalf("expected error recorded, got %+v", sink.calls)
	}
}
```

- [ ] **Step 2: 实现 `internal/agent/llm/instrument.go`**

```go
package llm

import (
	"context"
	"time"

	"github.com/V3teran/liusha/internal/llmcall"
)

type CallSink interface {
	Insert(ctx context.Context, c llmcall.Call) (int64, error)
}

type CallMeta struct {
	TaskID       *string
	EngagementID *string
}

type PricingProvider interface {
	Estimate(provider, model string, u Usage) float64
}

type instrumented struct {
	inner   Generator
	sink    CallSink
	meta    CallMeta
	pricing PricingProvider
}

func Instrument(g Generator, sink CallSink, meta CallMeta, p PricingProvider) Generator {
	return &instrumented{inner: g, sink: sink, meta: meta, pricing: p}
}

func (i *instrumented) Provider() string { return i.inner.Provider() }
func (i *instrumented) Model() string    { return i.inner.Model() }

func (i *instrumented) Generate(ctx context.Context, msgs []Message, tools []ToolSchema) (Result, error) {
	start := time.Now()
	res, err := i.inner.Generate(ctx, msgs, tools)
	latency := time.Since(start)

	call := llmcall.Call{
		TaskID: i.meta.TaskID, EngagementID: i.meta.EngagementID,
		Provider: i.inner.Provider(), Model: i.inner.Model(),
		InTokens:     res.Usage.InTokens,
		OutTokens:    res.Usage.OutTokens,
		CachedTokens: res.Usage.CachedTokens,
		LatencyMs:    int(latency.Milliseconds()),
		FinishReason: res.FinishReason,
	}
	if err != nil {
		call.Error = err.Error()
	} else if i.pricing != nil {
		call.CostUSD = i.pricing.Estimate(i.inner.Provider(), i.inner.Model(), res.Usage)
	}
	_, _ = i.sink.Insert(ctx, call)
	return res, err
}
```

- [ ] **Step 3: 跑测试 + Commit**

```bash
go test ./internal/agent/llm/... -race
git add internal/agent/llm/instrument.go internal/agent/llm/instrument_test.go
git commit -m "feat(llm): Instrument 装饰器（每次调用埋点 llm_call + cost 估算）"
```

---

## Task 21.5: internal/agent/llm — Router + Retry（多 provider 路由 + 429/529 退避，借鉴黑客松创新 11 + 共识 E）

**Files:**
- Create: `internal/agent/llm/router.go`
- Create: `internal/agent/llm/retry.go`
- Create: `internal/agent/llm/router_test.go`
- Create: `internal/agent/llm/retry_test.go`
- Modify: `internal/agent/llm/factory.go`（加 `For(routeKey) Generator` 暴露路由）
- Modify: `internal/agent/llm/instrument.go`（CallMeta 加 `RouteKey string` 字段；`llm_call.role` 写 RouteKey）

**关键点：**
- `Router` 持 `*Factory` + `routes map[string]string`（来自 `cfg.LLM.Routes`）；`For(role) Generator` 先查 routes，未命中走 default
- `For` 返回的 Generator 已套 `RetryDecorator`：按 spec §8.5 表（429 重试 3 次指数退避 1s/4s/16s 切 fallback；529 重试 1 次切 fallback；500/502/503/504 重试 2 次 1s/4s；超时重试 2 次 1s/3s；其他 4xx 直接抛）
- `RetryDecorator.Generate` 拦截 `*llm.HTTPError{Code:int}` 类型；fallback 切换由 Router 配合（Decorator 拿不到 Router）

- [ ] **Step 1: 写失败测试 `router_test.go`**

```go
//go:build !integration

package llm

import (
	"context"
	"testing"
)

func TestRouter_RoutesByKey(t *testing.T) {
	deepseek := &mockGen{tag: "deepseek"}
	haiku := &mockGen{tag: "haiku"}
	r := NewRouter(map[string]Generator{"deepseek": deepseek, "anthropic": haiku},
		map[string]string{"react.main": "deepseek", "observer": "anthropic"},
		"deepseek", "deepseek", "anthropic")
	if r.For("react.main").(*mockGen).tag != "deepseek" {
		t.Fatal("react.main should route to deepseek")
	}
	if r.For("observer").(*mockGen).tag != "haiku" {
		t.Fatal("observer should route to haiku")
	}
	if r.For("unknown.key").(*mockGen).tag != "deepseek" {
		t.Fatal("unknown should fall back to default")
	}
}
```

- [ ] **Step 2: 写失败测试 `retry_test.go`**

测试三件事：
- 429 重试 3 次后切 fallback；fallback 成功 → 整体成功
- 529 重试 1 次后切 fallback
- 其他 4xx（如 401）不重试

- [ ] **Step 3: 跑测试看红 → 实现 router.go + retry.go → 跑绿**

实现要点见本文件顶部"借鉴增量"章节的"实现端 patch 提示"。

- [ ] **Step 4: 改 instrument.go，CallMeta 加 RouteKey**

```go
type CallMeta struct {
    TaskID, EngagementID *string
    RouteKey             string  // 新增；Instrument 写入 llm_call.role
}
```

- [ ] **Step 5: Commit**

```bash
go test ./internal/agent/llm/... -race
git add internal/agent/llm
git commit -m "feat(llm): Router 多 provider 路由 + Retry 装饰器（429/529 退避 + fallback，借鉴黑客松）"
```

---

## Task 22: internal/agent/action — Registry

**Files:**
- Create: `internal/agent/action/registry.go`
- Create: `internal/agent/action/registry_test.go`

- [ ] **Step 1: 写失败测试**

```go
package action

import (
	"context"
	"encoding/json"
	"testing"
)

type echoAction struct{}

func (echoAction) Name() string        { return "echo" }
func (echoAction) Description() string { return "echo back" }
func (echoAction) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}}}`)
}
func (echoAction) Execute(_ context.Context, raw json.RawMessage) (Result, error) {
	return Result{Output: raw}, nil
}

func TestRegistry_RegisterAndExecute(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(echoAction{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(echoAction{}); err == nil {
		t.Fatal("expected duplicate error")
	}
	out, err := r.Execute(context.Background(), "echo", json.RawMessage(`{"msg":"hi"}`))
	if err != nil || string(out.Output) != `{"msg":"hi"}` {
		t.Fatalf("execute failed: out=%s err=%v", string(out.Output), err)
	}
	if _, err := r.Execute(context.Background(), "missing", nil); err == nil {
		t.Fatal("expected unknown action error")
	}
}
```

- [ ] **Step 2: 实现 `internal/agent/action/registry.go`**

```go
package action

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/agent/llm"
)

type Result struct {
	Output json.RawMessage
	Done   bool
}

type Action interface {
	Name() string
	Description() string
	ParametersJSON() json.RawMessage
	Execute(ctx context.Context, args json.RawMessage) (Result, error)
}

type Registry struct {
	actions map[string]Action
}

func NewRegistry() *Registry { return &Registry{actions: map[string]Action{}} }

func (r *Registry) Register(a Action) error {
	if _, ok := r.actions[a.Name()]; ok {
		return fmt.Errorf("action %q already registered", a.Name())
	}
	r.actions[a.Name()] = a
	return nil
}

func (r *Registry) Has(name string) bool {
	_, ok := r.actions[name]
	return ok
}

func (r *Registry) Schemas() []llm.ToolSchema {
	out := make([]llm.ToolSchema, 0, len(r.actions))
	for _, a := range r.actions {
		out = append(out, llm.ToolSchema{
			Name: a.Name(), Description: a.Description(), Parameters: a.ParametersJSON(),
		})
	}
	return out
}

func (r *Registry) Execute(ctx context.Context, name string, args json.RawMessage) (Result, error) {
	a, ok := r.actions[name]
	if !ok {
		return Result{}, fmt.Errorf("unknown action: %s", name)
	}
	return a.Execute(ctx, args)
}
```

- [ ] **Step 3: 跑测试 + Commit**

```bash
go test ./internal/agent/action/... -race
git add internal/agent/action
git commit -m "feat(action): Registry + Action 接口"
```

注：本 Registry 必须支持中间件链（`Use(mw ...ActionMiddleware)`）；具体三个 middleware 实现在 T22.5。Registry 内部数据结构：保留原 actions map，加 `middlewares []ActionMiddleware`；`Execute` 时 `wrapped := finalExecutor; for i := len(mw)-1; i >=0; i-- { wrapped = mw[i](wrapped) }; wrapped(ctx, name, args)`。

---

## Task 22.5: internal/agent/action/middleware — 三层 Action 中间件（借鉴黑客松共识 D + E + C）

**Files:**
- Create: `internal/agent/action/middleware/result_compress.go`
- Create: `internal/agent/action/middleware/loop_detect.go`
- Create: `internal/agent/action/middleware/done_validate.go`
- Create: 三份对应 `*_test.go`
- Create: `internal/agent/action/done_validator.go`（DoneValidator interface + 默认 AlwaysOK 实现）

**关键点：**
- `result_compress`：输入 result.Output（[]byte），>2KB 落盘 `engagement-store/<eid>/<action>-<seq>.txt`，返回值替换为 `{path, size, sha256, snippet[:400]}` JSON
- `loop_detect`：内部 ring buffer 维护 sliding window N=10；hash key = sha256(name + canonicalArgs + callerSkill + stepIdx%10)；连续 ≥3 次同 hash → 抛 `runtime.ErrLoopDetectorAbort`
- `done_validate`：仅当 action="done" 时拦截；从 ctx 取当前 Skill 的 `DoneValidator`，调 `CanDone(state, args)`；不通过抛 `runtime.ErrDoneNotReady{Missing: missing}`

- [ ] **Step 1: 写 done_validator.go interface + 测试**

```go
package action

import "context"

type DoneValidator interface {
	CanDone(ctx context.Context, args []byte) (ok bool, missing []string)
}

type AlwaysOK struct{}

func (AlwaysOK) CanDone(context.Context, []byte) (bool, []string) { return true, nil }
```

- [ ] **Step 2: 写 result_compress 测试 + 实现**

测试：
- result.Output 长度 1KB → 不落盘，原样返回
- result.Output 长度 5KB → 落盘到临时目录，返回 JSON 含 path/size/sha256/snippet 字段
- 落盘失败 → 返回原始 result + warning 日志（不阻断主流程）

实现签名：
```go
func ResultCompress(engagementID string, baseDir string) ActionMiddleware { ... }
```

- [ ] **Step 3: 写 loop_detect 测试 + 实现**

测试：
- 同 hash 连续 2 次 → 不抛
- 同 hash 连续 3 次 → 抛 `runtime.ErrLoopDetectorAbort`
- 不同 hash 交替 → 不抛

- [ ] **Step 4: 写 done_validate 测试 + 实现**

测试：
- action != "done" → 直接放行
- action == "done" + Validator.CanDone → 放行
- action == "done" + !CanDone → 抛 `runtime.ErrDoneNotReady{Missing: [...]}`

- [ ] **Step 5: Commit**

```bash
go test ./internal/agent/action/... -race
git add internal/agent/action
git commit -m "feat(action/middleware): result_compress + loop_detect + done_validate（借鉴黑客松共识 C/D/E）"
```

---

## Task 23: internal/agent/runtime — ReAct + Observer hook + Budget（Reflexion 删，由 T23.5 Observer 替代）

**Files:**
- Create: `internal/agent/runtime/budget.go`
- Create: `internal/agent/runtime/runtime.go`
- Create: `internal/agent/runtime/runtime_test.go`

- [ ] **Step 1: 写失败测试 `internal/agent/runtime/runtime_test.go`**

```go
package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/agent/action"
	"github.com/V3teran/liusha/internal/agent/llm"
)

type scriptedGen struct {
	turns  []llm.Result
	cursor int
}

func (s *scriptedGen) Provider() string { return "scripted" }
func (s *scriptedGen) Model() string    { return "x" }
func (s *scriptedGen) Generate(_ context.Context, _ []llm.Message, _ []llm.ToolSchema) (llm.Result, error) {
	r := s.turns[s.cursor]
	s.cursor++
	return r, nil
}

type captureAction struct {
	name   string
	called int
	res    action.Result
}

func (a *captureAction) Name() string                    { return a.name }
func (a *captureAction) Description() string             { return a.name }
func (a *captureAction) ParametersJSON() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (a *captureAction) Execute(_ context.Context, _ json.RawMessage) (action.Result, error) {
	a.called++
	return a.res, nil
}

func TestRun_StopsOnDone(t *testing.T) {
	gen := &scriptedGen{turns: []llm.Result{
		{ToolCalls: []llm.ToolCall{{ID: "1", Name: "done", Arguments: json.RawMessage(`{"reason":"ok"}`)}}, FinishReason: "tool_calls"},
	}}
	reg := action.NewRegistry()
	doneAct := &captureAction{name: "done", res: action.Result{Done: true}}
	_ = reg.Register(doneAct)

	out, err := Run(context.Background(), Config{
		LLM: gen, Actions: reg, Budget: Budget{MaxSteps: 5},
		SystemPrompt: "you are a sniffer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.TerminateBy != "done" || doneAct.called != 1 {
		t.Fatalf("unexpected: %+v done=%d", out, doneAct.called)
	}
}

func TestRun_StopsOnMaxSteps(t *testing.T) {
	loopCall := llm.Result{
		ToolCalls:    []llm.ToolCall{{ID: "x", Name: "noop", Arguments: json.RawMessage(`{}`)}},
		FinishReason: "tool_calls",
	}
	gen := &scriptedGen{turns: []llm.Result{loopCall, loopCall, loopCall}}
	reg := action.NewRegistry()
	_ = reg.Register(&captureAction{name: "noop"})

	out, err := Run(context.Background(), Config{
		LLM: gen, Actions: reg, Budget: Budget{MaxSteps: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.TerminateBy != "max_steps" || out.TotalSteps != 2 {
		t.Fatalf("unexpected: %+v", out)
	}
}

// fakeObserver 用于驱动 Observer 路径测试
type fakeObserver struct {
	verdicts []Verdict
	calls    int
}

func (f *fakeObserver) Evaluate(ctx context.Context, window []StepRecord) Verdict {
	v := f.verdicts[f.calls%len(f.verdicts)]
	f.calls++
	return v
}

func TestRun_ObserverAbortsLowValue(t *testing.T) {
	// 6 步循环，每 5 步触发一次 Observer；第一次返回 abort_low_value，应当 break
	noop := llm.Result{
		ToolCalls:    []llm.ToolCall{{ID: "n", Name: "noop", Arguments: json.RawMessage(`{}`)}},
		FinishReason: "tool_calls",
	}
	gen := &scriptedGen{turns: []llm.Result{noop, noop, noop, noop, noop, noop, noop}}
	reg := action.NewRegistry()
	_ = reg.Register(&captureAction{name: "noop"})

	obs := &fakeObserver{verdicts: []Verdict{{Decision: VerdictAbort}}}
	out, err := Run(context.Background(), Config{
		LLM: gen, Actions: reg,
		Budget:   Budget{MaxSteps: 30},
		Observer: obs, ObserverEverySteps: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.TerminateBy != "observer_abort" {
		t.Fatalf("expected terminate_by=observer_abort, got %q", out.TerminateBy)
	}
	if obs.calls != 1 {
		t.Fatalf("expected observer 1 call, got %d", obs.calls)
	}
}

func TestRun_ObserverInjectsHint(t *testing.T) {
	// Observer 返回 steer_with_hint，runtime 计数 +1 并继续；最终 done
	noop := llm.Result{
		ToolCalls:    []llm.ToolCall{{ID: "n", Name: "noop", Arguments: json.RawMessage(`{}`)}},
		FinishReason: "tool_calls",
	}
	terminate := llm.Result{
		ToolCalls:    []llm.ToolCall{{ID: "d", Name: "done", Arguments: json.RawMessage(`{}`)}},
		FinishReason: "tool_calls",
	}
	gen := &scriptedGen{turns: []llm.Result{noop, noop, noop, noop, noop, terminate}}
	reg := action.NewRegistry()
	_ = reg.Register(&captureAction{name: "noop"})
	_ = reg.Register(&captureAction{name: "done", res: action.Result{Done: true}})

	obs := &fakeObserver{verdicts: []Verdict{{Decision: VerdictSteer, Hint: "改向 X"}}}
	out, err := Run(context.Background(), Config{
		LLM: gen, Actions: reg,
		Budget:   Budget{MaxSteps: 30},
		Observer: obs, ObserverEverySteps: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.TerminateBy != "done" {
		t.Fatalf("expected done, got %q", out.TerminateBy)
	}
	if out.ObserverHints != 1 {
		t.Fatalf("expected hints=1, got %d", out.ObserverHints)
	}
}

func TestRun_LoopDetectorAbort(t *testing.T) {
	// action middleware（T22.5）抛 ErrLoopDetectorAbort，runtime 应 break with loop_detector_abort
	repeat := llm.Result{
		ToolCalls:    []llm.ToolCall{{ID: "r", Name: "noop", Arguments: json.RawMessage(`{"x":1}`)}},
		FinishReason: "tool_calls",
	}
	gen := &scriptedGen{turns: []llm.Result{repeat}}
	reg := action.NewRegistry()
	_ = reg.Register(&captureAction{name: "noop", err: ErrLoopDetectorAbort})

	out, err := Run(context.Background(), Config{LLM: gen, Actions: reg, Budget: Budget{MaxSteps: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if out.TerminateBy != "loop_detector_abort" {
		t.Fatalf("expected loop_detector_abort, got %q", out.TerminateBy)
	}
}

func TestRun_DoneValidateRejectsThenForce(t *testing.T) {
	// done 前 3 次被 done_validate middleware 拒绝（ErrDoneNotReady），runtime 注入 user msg；第 4 次强制放行
	doneCall := llm.Result{
		ToolCalls:    []llm.ToolCall{{ID: "d", Name: "done", Arguments: json.RawMessage(`{}`)}},
		FinishReason: "tool_calls",
	}
	gen := &scriptedGen{turns: []llm.Result{doneCall, doneCall, doneCall, doneCall}}
	reg := action.NewRegistry()
	_ = reg.Register(&captureAction{name: "done", err: ErrDoneNotReady{Missing: []string{"replay_multi_identity"}}})

	out, err := Run(context.Background(), Config{LLM: gen, Actions: reg, Budget: Budget{MaxSteps: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if out.TerminateBy != "done_force" {
		t.Fatalf("expected done_force, got %q", out.TerminateBy)
	}
	if out.DoneForceCount != 1 {
		t.Fatalf("expected force=1, got %d", out.DoneForceCount)
	}
}
```

- [ ] **Step 2: 实现 `internal/agent/runtime/budget.go`**

```go
package runtime

type Budget struct {
	MaxSteps        int
	MaxTokens       int
	WatchdogSeconds int
}
```

- [ ] **Step 3: 实现 `internal/agent/runtime/observer.go`（Observer 接口 + 类型）**

```go
package runtime

import "context"

const (
	VerdictKeepGoing = "keep_going"
	VerdictSteer     = "steer_with_hint"
	VerdictAbort     = "abort_low_value"
)

type Verdict struct {
	Decision string
	Hint     string
}

// StepRecord 是滑动窗喂给 Observer 的最近 N 步记录
type StepRecord struct {
	StepIdx    int
	ActionName string
	Args       []byte
	ObsSummary string // 工具结果的精简摘要（≤ 200 字），由 result_compress middleware 提供
}

type Observer interface {
	Evaluate(ctx context.Context, window []StepRecord) Verdict
}

// NoopObserver 永远 keep_going，便于在不需 Observer 的场景注入
type NoopObserver struct{}

func (NoopObserver) Evaluate(context.Context, []StepRecord) Verdict {
	return Verdict{Decision: VerdictKeepGoing}
}
```

- [ ] **Step 4: 实现 `internal/agent/runtime/errors.go`（中间件抛出的特殊错误）**

```go
package runtime

import (
	"errors"
	"fmt"
	"strings"
)

// ErrLoopDetectorAbort 由 loop_detect middleware 在连续 3 次同 hash 时抛出
var ErrLoopDetectorAbort = errors.New("loop detector: same action+args repeated, aborting")

// ErrDoneNotReady 由 done_validate middleware 在 done 前置条件不满足时抛出
type ErrDoneNotReady struct {
	Missing []string
}

func (e ErrDoneNotReady) Error() string {
	return fmt.Sprintf("done not ready: missing %s", strings.Join(e.Missing, ", "))
}

func IsDoneNotReady(err error) (ErrDoneNotReady, bool) {
	var e ErrDoneNotReady
	if errors.As(err, &e) {
		return e, true
	}
	return ErrDoneNotReady{}, false
}
```

- [ ] **Step 5: 实现 `internal/agent/runtime/runtime.go`**

```go
package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/agent/action"
	"github.com/V3teran/liusha/internal/agent/llm"
)

const (
	doneForceMaxRejects = 3 // done 被拒 3 次后强制放行，避免死循环
)

type Config struct {
	LLM                llm.Generator
	Actions            *action.Registry
	Budget             Budget
	SystemPrompt       string
	UserPrompt         string
	OnAbort            func(ctx context.Context) (bool, error)
	Observer           Observer // 默认 NoopObserver
	ObserverEverySteps int      // 默认 5；<=0 视为不触发
}

type Outcome struct {
	TerminateBy    string
	TotalSteps     int
	TotalUsage     llm.Usage
	ObserverHints  int // Observer 注入 steer hint 的次数
	DoneForceCount int // done 被强制放行的次数（仅 0 或 1，便于断言）
}

func Run(ctx context.Context, cfg Config) (Outcome, error) {
	if cfg.LLM == nil {
		return Outcome{}, errors.New("LLM generator nil")
	}
	if cfg.Actions == nil {
		return Outcome{}, errors.New("Actions registry nil")
	}
	if cfg.Budget.MaxSteps <= 0 {
		cfg.Budget.MaxSteps = 30
	}
	if cfg.Budget.WatchdogSeconds <= 0 {
		cfg.Budget.WatchdogSeconds = 60
	}
	if cfg.Observer == nil {
		cfg.Observer = NoopObserver{}
	}
	if cfg.ObserverEverySteps <= 0 {
		cfg.ObserverEverySteps = 5
	}

	msgs := []llm.Message{}
	if cfg.SystemPrompt != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleSystem, Content: cfg.SystemPrompt})
	}
	if cfg.UserPrompt != "" {
		msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: cfg.UserPrompt})
	}

	out := Outcome{}
	window := make([]StepRecord, 0, cfg.ObserverEverySteps)
	doneRejectCount := 0
	for {
		if out.TotalSteps >= cfg.Budget.MaxSteps {
			out.TerminateBy = "max_steps"
			return out, nil
		}
		if cfg.Budget.MaxTokens > 0 && out.TotalUsage.InTokens+out.TotalUsage.OutTokens >= cfg.Budget.MaxTokens {
			out.TerminateBy = "max_tokens"
			return out, nil
		}
		if cfg.OnAbort != nil {
			if abort, err := cfg.OnAbort(ctx); err == nil && abort {
				out.TerminateBy = "aborted"
				return out, nil
			}
		}

		// Observer hook：每 N 步触发，开始时不算（step > 0）
		if out.TotalSteps > 0 && out.TotalSteps%cfg.ObserverEverySteps == 0 {
			v := cfg.Observer.Evaluate(ctx, window)
			switch v.Decision {
			case VerdictAbort:
				out.TerminateBy = "observer_abort"
				return out, nil
			case VerdictSteer:
				if v.Hint != "" {
					msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: "提示：" + v.Hint})
					out.ObserverHints++
				}
			}
		}

		stepCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.Budget.WatchdogSeconds)*time.Second)
		res, err := cfg.LLM.Generate(stepCtx, msgs, cfg.Actions.Schemas())
		cancel()
		if err != nil {
			return out, fmt.Errorf("step %d generate: %w", out.TotalSteps+1, err)
		}
		out.TotalSteps++
		out.TotalUsage = out.TotalUsage.Add(res.Usage)

		if len(res.ToolCalls) == 0 {
			msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: res.Content})
			out.TerminateBy = "no_tool_call"
			return out, nil
		}
		msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, ToolCalls: res.ToolCalls, Content: res.Content})

		var sawDone bool
		for _, tc := range res.ToolCalls {
			tcRes, execErr := cfg.Actions.Execute(ctx, tc.Name, tc.Arguments)

			// LoopDetector middleware 抛错：直接终止
			if errors.Is(execErr, ErrLoopDetectorAbort) {
				out.TerminateBy = "loop_detector_abort"
				return out, nil
			}

			// DoneValidator middleware 抛错：注入 user msg 让 LLM 继续；超过 maxRejects 强制放行
			if e, ok := IsDoneNotReady(execErr); ok {
				doneRejectCount++
				if doneRejectCount >= doneForceMaxRejects {
					out.TerminateBy = "done_force"
					out.DoneForceCount = 1
					return out, nil
				}
				msgs = append(msgs, llm.Message{
					Role:    llm.RoleUser,
					Content: fmt.Sprintf("你声称完成但未达终止条件 [missing: %v]，继续工作。", e.Missing),
				})
				continue
			}

			obs := tcRes.Output
			if execErr != nil {
				obs = []byte(fmt.Sprintf(`{"error":%q}`, execErr.Error()))
			}
			msgs = append(msgs, llm.Message{
				Role: llm.RoleTool, ToolCallID: tc.ID, Name: tc.Name, Content: string(obs),
			})
			if tcRes.Done || tc.Name == "done" {
				sawDone = true
			}

			// 喂给 Observer 的滑动窗
			rec := StepRecord{
				StepIdx:    out.TotalSteps,
				ActionName: tc.Name,
				Args:       tc.Arguments,
				ObsSummary: tcRes.Summary, // 由 result_compress middleware 提供
			}
			window = append(window, rec)
			if len(window) > cfg.ObserverEverySteps*2 {
				window = window[len(window)-cfg.ObserverEverySteps*2:]
			}
		}

		if sawDone {
			out.TerminateBy = "done"
			return out, nil
		}
	}
}
```

- [ ] **Step 6: 跑测试 + Commit**

```bash
go test ./internal/agent/runtime/... -race
git add internal/agent/runtime
git commit -m "feat(runtime): ReAct + Observer hook + LoopDetector + DoneValidate（替代 Reflexion）"
```

---

## Task 23.5: internal/agent/runtime — Observer LLM 实现 + Distill Hook（借鉴黑客松共识 A + 创新 8）

**Files:**
- Create: `internal/agent/runtime/observer_llm.go`（具体的 LLM-based Observer）
- Create: `internal/agent/runtime/observer_llm_test.go`
- Create: `internal/agent/runtime/distill.go`（finding.OnSaved 钩子函数）
- Create: `internal/agent/runtime/distill_test.go`

**关键点：**
- `LLMObserver` 实现 `Observer` interface；持 `llm.Generator`（应来自 `Router.For("observer")` light_provider）+ engagement Store
- Evaluate 把 window + ReadState 输出拼成紧凑 prompt，要求 LLM 返回 `{"decision":"keep_going|steer_with_hint|abort_low_value","hint":"..."}` 严格 JSON
- LLM 返回非法 JSON 或解析失败 → 回退到 `{Decision: VerdictKeepGoing}`（不阻塞主流程）
- `NewDistillHook` 返回 `func(eid string, f finding.Finding)` 闭包；finding store 启动时 `finds.OnSaved(hook)` 注册；闭包内调 light LLM 总结 + `engs.AppendHint`

- [ ] **Step 1: 写 observer_llm 测试**

```go
func TestLLMObserver_KeepGoing_OnInvalidJSON(t *testing.T) {
    gen := &mockGen{out: "not json"}
    obs := NewLLMObserver(gen, nil, "eid")
    v := obs.Evaluate(context.Background(), nil)
    if v.Decision != VerdictKeepGoing {
        t.Fatalf("expected keep_going, got %s", v.Decision)
    }
}
func TestLLMObserver_AbortDecision(t *testing.T) {
    gen := &mockGen{out: `{"decision":"abort_low_value","hint":""}`}
    obs := NewLLMObserver(gen, nil, "eid")
    v := obs.Evaluate(context.Background(), []StepRecord{{ActionName: "noop"}})
    if v.Decision != VerdictAbort {
        t.Fatalf("expected abort, got %s", v.Decision)
    }
}
```

- [ ] **Step 2: 实现 observer_llm.go**

接口要点：
```go
func NewLLMObserver(g llm.Generator, store *engagement.Store, engagementID string) *LLMObserver
```

- [ ] **Step 3: 写 distill 测试**

mock finding.Store 触发 OnSaved；断言 `engagement.AppendHint` 被调一次，hint.from_skill = finding.kind 对应 skill。

- [ ] **Step 4: 实现 distill.go**

```go
func NewDistillHook(g llm.Generator, store *engagement.Store) func(string, finding.Finding) {
    return func(eid string, f finding.Finding) {
        // 异步执行避免阻塞 finding.Save
        go func() {
            ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
            defer cancel()
            // 拼 prompt → LLM → 解析 → AppendHint
        }()
    }
}
```

- [ ] **Step 5: Commit**

```bash
go test ./internal/agent/runtime/... -race
git add internal/agent/runtime
git commit -m "feat(runtime): LLMObserver + Distill hook（借鉴黑客松共识 A + 创新 8）"
```

---

## Task 24: internal/agent/actions — 通用 Actions（read_state + write_fact/idea/hint + write_finding + write_graph + load_skill + spawn_subtask + done(系统校验)）

> 注：原计划 7 个 Action 中的 `read_memory / write_memory` 已删，按黑客松借鉴改为 `read_state / write_fact / write_idea / write_hint` 四个。`done` 改系统校验式（由 T22.5 done_validate middleware 拦截）。`write_finding` 写库后调 finding.OnSaved 触发 T23.5 distill。详见本文件顶部"借鉴增量"。

—— 原 Task 24 内容如下，implementer 按上述差异调整 ——


**Files:**
- Create: `internal/agent/actions/done.go`
- Create: `internal/agent/actions/memory.go`
- Modify: `internal/engagement/store.go`（追加 GetMemory / AppendMemory）
- Create: `internal/agent/actions/finding.go`
- Create: `internal/agent/actions/graph.go`
- Create: `internal/agent/actions/skill.go`
- Create: `internal/agent/actions/spawn.go`
- Create: `internal/agent/actions/actions_test.go`

- [ ] **Step 1: 实现 `internal/agent/actions/done.go`**

```go
package actions

import (
	"context"
	"encoding/json"

	"github.com/V3teran/liusha/internal/agent/action"
)

type Done struct{}

func (Done) Name() string        { return "done" }
func (Done) Description() string { return "终止当前任务，args 中带 reason/summary" }
func (Done) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"reason":{"type":"string"},"summary":{"type":"string"}}}`)
}
func (Done) Execute(_ context.Context, args json.RawMessage) (action.Result, error) {
	return action.Result{Done: true, Output: args}, nil
}
```

- [ ] **Step 2: Edit-append 到 `internal/engagement/store.go` 末尾**

```go
func (s *Store) GetMemory(ctx context.Context, id string) ([]byte, error) {
	var b []byte
	if err := s.pool.QueryRow(ctx, `SELECT memory FROM engagement WHERE id=$1`, id).Scan(&b); err != nil {
		return nil, fmt.Errorf("get memory: %w", err)
	}
	if len(b) == 0 {
		return []byte("{}"), nil
	}
	return b, nil
}

func (s *Store) AppendMemory(ctx context.Context, id string, patch []byte) error {
	if len(patch) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE engagement SET memory = memory || $1::jsonb, last_activity_at=now() WHERE id=$2`, patch, id)
	return err
}
```

- [ ] **Step 3: 实现 `internal/agent/actions/memory.go`**

```go
package actions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/agent/action"
)

// MemoryStore 是 engagement memory 三层（facts / ideas / hints）的访问接口
// 由 internal/engagement.Store 实现（plan 1 part2 T6 任务）
type MemoryStore interface {
	ReadState(ctx context.Context, engagementID string) ([]byte, error) // 返回 {facts, ideas, hints} 合并后的 JSON
	AppendFact(ctx context.Context, engagementID string, entry []byte) error
	AppendIdea(ctx context.Context, engagementID string, entry []byte) error
	AppendHint(ctx context.Context, engagementID string, entry []byte) error
}

// ReadState — 一次读取 memory 三层 + Observer 摘要
type ReadState struct {
	Store        MemoryStore
	EngagementID string
}

func (a *ReadState) Name() string        { return "read_state" }
func (a *ReadState) Description() string { return "读 engagement memory 三层（facts/ideas/hints）合并后的 JSON" }
func (a *ReadState) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (a *ReadState) Execute(ctx context.Context, _ json.RawMessage) (action.Result, error) {
	state, err := a.Store.ReadState(ctx, a.EngagementID)
	if err != nil {
		return action.Result{}, fmt.Errorf("read state: %w", err)
	}
	return action.Result{Output: state}, nil
}

// WriteFact — 追加一条事实（evidence 或 boundary）到 memory_facts
type WriteFact struct {
	Store        MemoryStore
	EngagementID string
}

func (a *WriteFact) Name() string        { return "write_fact" }
func (a *WriteFact) Description() string { return "追加一条事实（category=evidence|boundary）到 memory_facts，只追加不修改" }
func (a *WriteFact) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
	  "type":"object",
	  "properties":{
	    "category":{"type":"string","enum":["evidence","boundary"]},
	    "content":{"type":"string"}
	  },
	  "required":["category","content"]
	}`)
}
func (a *WriteFact) Execute(ctx context.Context, args json.RawMessage) (action.Result, error) {
	var p struct {
		Category string `json:"category"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return action.Result{}, fmt.Errorf("parse args: %w", err)
	}
	if p.Category != "evidence" && p.Category != "boundary" {
		return action.Result{}, fmt.Errorf("invalid category %q, want evidence|boundary", p.Category)
	}
	entry, _ := json.Marshal(map[string]any{"category": p.Category, "content": p.Content})
	if err := a.Store.AppendFact(ctx, a.EngagementID, entry); err != nil {
		return action.Result{}, fmt.Errorf("append fact: %w", err)
	}
	return action.Result{Output: json.RawMessage(`{"ok":true}`)}, nil
}

// WriteIdea — 追加/更新一条假设到 memory_ideas（status: pending|testing|verified|failed）
type WriteIdea struct {
	Store        MemoryStore
	EngagementID string
}

func (a *WriteIdea) Name() string        { return "write_idea" }
func (a *WriteIdea) Description() string { return "追加/更新一条假设到 memory_ideas，status ∈ pending|testing|verified|failed" }
func (a *WriteIdea) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
	  "type":"object",
	  "properties":{
	    "direction":{"type":"string"},
	    "status":{"type":"string","enum":["pending","testing","verified","failed"]}
	  },
	  "required":["direction","status"]
	}`)
}
func (a *WriteIdea) Execute(ctx context.Context, args json.RawMessage) (action.Result, error) {
	var p struct {
		Direction string `json:"direction"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return action.Result{}, fmt.Errorf("parse args: %w", err)
	}
	switch p.Status {
	case "pending", "testing", "verified", "failed":
	default:
		return action.Result{}, fmt.Errorf("invalid status %q", p.Status)
	}
	entry, _ := json.Marshal(map[string]any{"direction": p.Direction, "status": p.Status})
	if err := a.Store.AppendIdea(ctx, a.EngagementID, entry); err != nil {
		return action.Result{}, fmt.Errorf("append idea: %w", err)
	}
	return action.Result{Output: json.RawMessage(`{"ok":true}`)}, nil
}

// WriteHint — 追加一条提示到 memory_hints（系统态：Observer / DoneValidator / Distill 也用此 action）
type WriteHint struct {
	Store        MemoryStore
	EngagementID string
}

func (a *WriteHint) Name() string        { return "write_hint" }
func (a *WriteHint) Description() string { return "追加一条提示到 memory_hints（priority 1-10，越大越优先）" }
func (a *WriteHint) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
	  "type":"object",
	  "properties":{
	    "from_skill":{"type":"string"},
	    "content":{"type":"string"},
	    "priority":{"type":"integer","minimum":1,"maximum":10}
	  },
	  "required":["from_skill","content"]
	}`)
}
func (a *WriteHint) Execute(ctx context.Context, args json.RawMessage) (action.Result, error) {
	var p struct {
		FromSkill string `json:"from_skill"`
		Content   string `json:"content"`
		Priority  int    `json:"priority"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return action.Result{}, fmt.Errorf("parse args: %w", err)
	}
	if p.Priority == 0 {
		p.Priority = 5
	}
	entry, _ := json.Marshal(map[string]any{
		"from_skill": p.FromSkill, "content": p.Content, "priority": p.Priority,
	})
	if err := a.Store.AppendHint(ctx, a.EngagementID, entry); err != nil {
		return action.Result{}, fmt.Errorf("append hint: %w", err)
	}
	return action.Result{Output: json.RawMessage(`{"ok":true}`)}, nil
}
```

- [ ] **Step 4: 实现 `internal/agent/actions/finding.go`**

```go
package actions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/agent/action"
	"github.com/V3teran/liusha/internal/finding"
)

type WriteFinding struct {
	Store        *finding.Store
	EngagementID string
	TaskID       string
}

func (a *WriteFinding) Name() string { return "write_finding" }
func (a *WriteFinding) Description() string {
	return "写或合并一条漏洞 finding。dedup_key 必填，evidence 用增量 jsonb。"
}
func (a *WriteFinding) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
	  "type":"object",
	  "properties": {
	    "kind":{"type":"string"},
	    "severity":{"type":"string","enum":["info","low","medium","high","critical"]},
	    "title":{"type":"string"},
	    "target":{"type":"object"},
	    "evidence":{"type":"object"},
	    "payload":{"type":"object"},
	    "tool":{"type":"string"},
	    "confidence":{"type":"string","enum":["unverified","verified","rejected"]},
	    "dedup_key":{"type":"string"}
	  },
	  "required":["kind","severity","title","dedup_key"]
	}`)
}

func (a *WriteFinding) Execute(ctx context.Context, args json.RawMessage) (action.Result, error) {
	var in struct {
		Kind       string          `json:"kind"`
		Severity   string          `json:"severity"`
		Title      string          `json:"title"`
		Target     json.RawMessage `json:"target"`
		Evidence   json.RawMessage `json:"evidence"`
		Payload    json.RawMessage `json:"payload"`
		Tool       string          `json:"tool"`
		Confidence string          `json:"confidence"`
		DedupKey   string          `json:"dedup_key"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return action.Result{}, fmt.Errorf("parse args: %w", err)
	}
	if in.DedupKey == "" {
		return action.Result{}, fmt.Errorf("dedup_key required")
	}
	tid := a.TaskID
	id, err := a.Store.Upsert(ctx, finding.Finding{
		EngagementID: a.EngagementID,
		TaskID:       &tid,
		Kind:         in.Kind,
		Severity:     finding.Severity(in.Severity),
		Title:        in.Title,
		Target:       in.Target,
		Evidence:     in.Evidence,
		Payload:      in.Payload,
		Tool:         in.Tool,
		Confidence:   finding.Confidence(in.Confidence),
		DedupKey:     in.DedupKey,
	})
	if err != nil {
		return action.Result{}, err
	}
	out, _ := json.Marshal(map[string]string{"id": id, "dedup_key": in.DedupKey})
	return action.Result{Output: out}, nil
}
```

- [ ] **Step 5: 实现 `internal/agent/actions/graph.go`**

```go
package actions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/agent/action"
	"github.com/V3teran/liusha/internal/graph"
)

type WriteGraph struct {
	Store        *graph.Store
	EngagementID string
}

func (a *WriteGraph) Name() string        { return "write_graph" }
func (a *WriteGraph) Description() string { return "写一组 node 与/或 edge（去重 upsert）" }
func (a *WriteGraph) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
	  "type":"object",
	  "properties":{
	    "nodes":{"type":"array","items":{
	      "type":"object",
	      "properties":{"kind":{"type":"string"},"dedup_key":{"type":"string"},"payload":{"type":"object"}},
	      "required":["kind","dedup_key"]}},
	    "edges":{"type":"array","items":{
	      "type":"object",
	      "properties":{"from":{"type":"string"},"to":{"type":"string"},"kind":{"type":"string"},"payload":{"type":"object"}},
	      "required":["from","to","kind"]}}
	  }
	}`)
}

func (a *WriteGraph) Execute(ctx context.Context, args json.RawMessage) (action.Result, error) {
	var in struct {
		Nodes []struct {
			Kind     string          `json:"kind"`
			DedupKey string          `json:"dedup_key"`
			Payload  json.RawMessage `json:"payload"`
		} `json:"nodes"`
		Edges []struct {
			From    string          `json:"from"`
			To      string          `json:"to"`
			Kind    string          `json:"kind"`
			Payload json.RawMessage `json:"payload"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return action.Result{}, fmt.Errorf("parse args: %w", err)
	}
	nodeIDs := map[string]string{}
	for _, n := range in.Nodes {
		id, err := a.Store.UpsertNode(ctx, a.EngagementID, n.Kind, n.DedupKey, n.Payload)
		if err != nil {
			return action.Result{}, err
		}
		nodeIDs[n.DedupKey] = id
	}
	for _, e := range in.Edges {
		from, ok := nodeIDs[e.From]
		if !ok {
			from = e.From
		}
		to, ok := nodeIDs[e.To]
		if !ok {
			to = e.To
		}
		if _, err := a.Store.UpsertEdge(ctx, a.EngagementID, from, to, e.Kind, e.Payload); err != nil {
			return action.Result{}, err
		}
	}
	out, _ := json.Marshal(map[string]any{"nodes": len(in.Nodes), "edges": len(in.Edges)})
	return action.Result{Output: out}, nil
}
```

- [ ] **Step 6: 实现 `internal/agent/actions/skill.go`**

```go
package actions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/agent/action"
)

type SkillLoader interface {
	Load(ctx context.Context, path string) (string, error)
}

type LoadSkill struct {
	Loader SkillLoader
	Append func(systemBody string)
}

func (a *LoadSkill) Name() string        { return "load_skill" }
func (a *LoadSkill) Description() string { return "把 skill 正文拼到 system prompt 末尾（仅当前任务生效）" }
func (a *LoadSkill) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
}

func (a *LoadSkill) Execute(ctx context.Context, args json.RawMessage) (action.Result, error) {
	var in struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return action.Result{}, err
	}
	body, err := a.Loader.Load(ctx, in.Path)
	if err != nil {
		return action.Result{}, fmt.Errorf("load skill %s: %w", in.Path, err)
	}
	if a.Append != nil {
		a.Append(body)
	}
	return action.Result{Output: json.RawMessage(`{"loaded":true}`)}, nil
}
```

- [ ] **Step 7: 实现 `internal/agent/actions/spawn.go`**

```go
package actions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/agent/action"
)

type Spawner interface {
	Spawn(ctx context.Context, parentTaskID string, skill string, input json.RawMessage, budget json.RawMessage) (string, error)
}

type SpawnSubtask struct {
	Engine       Spawner
	ParentTaskID string
}

func (a *SpawnSubtask) Name() string { return "spawn_subtask" }
func (a *SpawnSubtask) Description() string {
	return "派生一个同角色子任务（带 skill）。子任务异步运行，立刻返回 task_id。"
}
func (a *SpawnSubtask) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
	  "type":"object",
	  "properties":{
	    "skill":{"type":"string"},
	    "input":{"type":"object"},
	    "budget":{"type":"object","properties":{"max_steps":{"type":"integer"},"max_tokens":{"type":"integer"}}}
	  },
	  "required":["skill"]
	}`)
}

func (a *SpawnSubtask) Execute(ctx context.Context, args json.RawMessage) (action.Result, error) {
	var in struct {
		Skill  string          `json:"skill"`
		Input  json.RawMessage `json:"input"`
		Budget json.RawMessage `json:"budget"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return action.Result{}, err
	}
	id, err := a.Engine.Spawn(ctx, a.ParentTaskID, in.Skill, in.Input, in.Budget)
	if err != nil {
		return action.Result{}, fmt.Errorf("spawn: %w", err)
	}
	out, _ := json.Marshal(map[string]string{"task_id": id})
	return action.Result{Output: out}, nil
}
```

- [ ] **Step 8: 写 `internal/agent/actions/actions_test.go`**

```go
package actions

import (
	"context"
	"encoding/json"
	"testing"
)

type fakeMem struct {
	state []byte
	facts [][]byte
	ideas [][]byte
	hints [][]byte
}

func (f *fakeMem) ReadState(_ context.Context, _ string) ([]byte, error) { return f.state, nil }
func (f *fakeMem) AppendFact(_ context.Context, _ string, e []byte) error {
	f.facts = append(f.facts, append([]byte(nil), e...))
	return nil
}
func (f *fakeMem) AppendIdea(_ context.Context, _ string, e []byte) error {
	f.ideas = append(f.ideas, append([]byte(nil), e...))
	return nil
}
func (f *fakeMem) AppendHint(_ context.Context, _ string, e []byte) error {
	f.hints = append(f.hints, append([]byte(nil), e...))
	return nil
}

func TestDone(t *testing.T) {
	r, err := Done{}.Execute(context.Background(), json.RawMessage(`{"reason":"ok"}`))
	if err != nil || !r.Done {
		t.Fatalf("unexpected: %+v err=%v", r, err)
	}
}

func TestReadState(t *testing.T) {
	m := &fakeMem{state: []byte(`{"facts":{"evidence":[]},"ideas":{"hypotheses":[]},"hints":{"hints":[]}}`)}
	rd := &ReadState{Store: m, EngagementID: "e"}
	got, err := rd.Execute(context.Background(), nil)
	if err != nil || string(got.Output) == "" {
		t.Fatalf("read: %s err=%v", string(got.Output), err)
	}
}

func TestWriteFact(t *testing.T) {
	m := &fakeMem{}
	wr := &WriteFact{Store: m, EngagementID: "e"}
	_, err := wr.Execute(context.Background(), json.RawMessage(`{"category":"evidence","content":"endpoint X 401"}`))
	if err != nil || len(m.facts) != 1 {
		t.Fatalf("facts=%v err=%v", m.facts, err)
	}
}

func TestWriteFact_RejectInvalidCategory(t *testing.T) {
	m := &fakeMem{}
	wr := &WriteFact{Store: m, EngagementID: "e"}
	_, err := wr.Execute(context.Background(), json.RawMessage(`{"category":"junk","content":"x"}`))
	if err == nil {
		t.Fatal("expected error for invalid category")
	}
}

func TestWriteIdea(t *testing.T) {
	m := &fakeMem{}
	wr := &WriteIdea{Store: m, EngagementID: "e"}
	_, err := wr.Execute(context.Background(), json.RawMessage(`{"direction":"GET /admin","status":"testing"}`))
	if err != nil || len(m.ideas) != 1 {
		t.Fatalf("ideas=%v err=%v", m.ideas, err)
	}
}

func TestWriteHint(t *testing.T) {
	m := &fakeMem{}
	wr := &WriteHint{Store: m, EngagementID: "e"}
	_, err := wr.Execute(context.Background(), json.RawMessage(`{"from_skill":"vuln/web/bac","content":"hint","priority":7}`))
	if err != nil || len(m.hints) != 1 {
		t.Fatalf("hints=%v err=%v", m.hints, err)
	}
}
```

- [ ] **Step 9: 跑测试 + Commit**

```bash
go test ./internal/agent/actions/... -race
go test -tags=integration ./internal/engagement/... -race -count=1  # 验证 ReadState/Append* 三方法不破环境
git add internal/agent/actions internal/engagement/store.go
git commit -m "feat(actions): done(系统校验)/read_state/write_fact/write_idea/write_hint/write_finding/write_graph/load_skill/spawn_subtask"
```

---

## Task 25: internal/skill — Card + Loader + bac SKILL.md 占位

**Files:**
- Create: `internal/skill/card.go`
- Create: `internal/skill/loader.go`
- Create: `internal/skill/loader_test.go`
- Create: `skills/vuln/web/bac/SKILL.md`

- [ ] **Step 1: 写失败测试**

```go
package skill

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoader_Load(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "vuln/web/bac"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `---
name: vuln/web/bac
description: BAC
applies_to:
  - role: sniffer
budget:
  max_steps: 10
  max_tokens: 15000
required_actions: [fetch_credentials, replay_multi_identity]
---
正文：步骤指引...`
	if err := os.WriteFile(filepath.Join(dir, "vuln/web/bac/SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	l := NewLoader(dir)
	c, err := l.LoadCard(context.Background(), "vuln/web/bac")
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "vuln/web/bac" || c.Budget.MaxSteps != 10 || len(c.RequiredActions) != 2 {
		t.Fatalf("card=%+v", c)
	}
	if c.Body == "" {
		t.Fatal("body empty")
	}
}
```

- [ ] **Step 2: 实现 `internal/skill/card.go`**

```go
package skill

type AppliesTo struct {
	Role string `yaml:"role"`
}

type Budget struct {
	MaxSteps  int `yaml:"max_steps"`
	MaxTokens int `yaml:"max_tokens"`
}

type Card struct {
	Name            string      `yaml:"name"`
	Description     string      `yaml:"description"`
	AppliesTo       []AppliesTo `yaml:"applies_to"`
	Budget          Budget      `yaml:"budget"`
	RequiredActions []string    `yaml:"required_actions"`
	Body            string      `yaml:"-"`
}
```

- [ ] **Step 3: 实现 `internal/skill/loader.go`**

```go
package skill

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Loader struct{ root string }

func NewLoader(root string) *Loader { return &Loader{root: root} }

func (l *Loader) Load(ctx context.Context, path string) (string, error) {
	c, err := l.LoadCard(ctx, path)
	if err != nil {
		return "", err
	}
	return c.Body, nil
}

func (l *Loader) LoadCard(_ context.Context, path string) (Card, error) {
	full := filepath.Join(l.root, path, "SKILL.md")
	raw, err := os.ReadFile(full)
	if err != nil {
		return Card{}, fmt.Errorf("read %s: %w", full, err)
	}
	front, body, err := splitFrontmatter(raw)
	if err != nil {
		return Card{}, fmt.Errorf("parse %s: %w", full, err)
	}
	var c Card
	if err := yaml.Unmarshal(front, &c); err != nil {
		return Card{}, fmt.Errorf("yaml %s: %w", full, err)
	}
	c.Body = string(body)
	return c, nil
}

var delim = []byte("---")

func splitFrontmatter(raw []byte) ([]byte, []byte, error) {
	r := bytes.TrimLeft(raw, "\n\r\t ")
	if !bytes.HasPrefix(r, delim) {
		return nil, nil, fmt.Errorf("missing frontmatter")
	}
	r = r[len(delim):]
	idx := bytes.Index(r, append([]byte("\n"), delim...))
	if idx < 0 {
		return nil, nil, fmt.Errorf("frontmatter not closed")
	}
	front := r[:idx]
	body := r[idx+len(delim)+1:]
	return front, bytes.TrimLeft(body, "\n\r"), nil
}
```

- [ ] **Step 4: `skills/vuln/web/bac/SKILL.md` 占位**

```markdown
---
name: vuln/web/bac
description: BAC（未授权 / 垂直越权 / 水平越权）
applies_to:
  - role: sniffer
budget:
  max_steps: 10
  max_tokens: 15000
required_actions:
  - fetch_credentials
  - replay_multi_identity
  - heuristic_check
  - compute_similarity
  - write_finding
  - write_graph
  - done
---
（plan 2 填正文：步骤指引、判定逻辑、写 finding 的 dedup_key 模板）
```

- [ ] **Step 5: 跑测试 + Commit**

```bash
go test ./internal/skill/... -race
git add internal/skill skills/vuln/web/bac/SKILL.md
git commit -m "feat(skill): Card + frontmatter Loader + bac SKILL.md 占位"
```

---

## Task 26: internal/worker — Asynq client + Mux

**Files:**
- Create: `internal/worker/queues.go`
- Create: `internal/worker/client.go`
- Create: `internal/worker/handler.go`
- Create: `internal/worker/worker_test.go`

- [ ] **Step 1: 写失败测试**

```go
package worker

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
)

func TestClient_EnqueueRoutesQueue(t *testing.T) {
	mr := miniredis.RunT(t)
	c := NewClient(asynq.RedisClientOpt{Addr: mr.Addr()})
	defer c.Close()

	id, q, err := c.Enqueue(context.Background(), RoleSniffer, json.RawMessage(`{"task_id":"t1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if q != "agent:sniffer" || id == "" {
		t.Fatalf("id=%q q=%q", id, q)
	}
}
```

- [ ] **Step 2: 实现 `internal/worker/queues.go`**

```go
package worker

type Role string

const (
	RoleSniffer  Role = "sniffer"
	RoleOperator Role = "operator"

	QueueSniffer  = "agent:sniffer"
	QueueOperator = "agent:operator"

	TaskTypeRun = "agent:run"
)

func (r Role) Queue() string {
	if r == RoleOperator {
		return QueueOperator
	}
	return QueueSniffer
}
```

- [ ] **Step 3: 实现 `internal/worker/client.go`**

```go
package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
)

type Client struct{ c *asynq.Client }

func NewClient(opt asynq.RedisClientOpt) *Client {
	return &Client{c: asynq.NewClient(opt)}
}

func (c *Client) Close() error { return c.c.Close() }

func (c *Client) Enqueue(ctx context.Context, role Role, payload json.RawMessage) (string, string, error) {
	t := asynq.NewTask(TaskTypeRun, payload, asynq.Queue(role.Queue()))
	info, err := c.c.EnqueueContext(ctx, t)
	if err != nil {
		return "", "", fmt.Errorf("enqueue %s: %w", role, err)
	}
	return info.ID, info.Queue, nil
}
```

- [ ] **Step 4: 实现 `internal/worker/handler.go`**

```go
package worker

import (
	"context"
	"encoding/json"

	"github.com/hibiken/asynq"
)

type Payload struct {
	TaskID       string          `json:"task_id"`
	EngagementID string          `json:"engagement_id"`
	Role         Role            `json:"role"`
	Skill        string          `json:"skill,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
}

type RoleHandler func(ctx context.Context, p Payload) error

type Mux struct {
	handlers map[Role]RoleHandler
}

func NewMux() *Mux { return &Mux{handlers: map[Role]RoleHandler{}} }

func (m *Mux) Register(role Role, h RoleHandler) { m.handlers[role] = h }

func (m *Mux) AsynqMux() *asynq.ServeMux {
	mux := asynq.NewServeMux()
	mux.HandleFunc(TaskTypeRun, func(ctx context.Context, t *asynq.Task) error {
		var p Payload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return err
		}
		h, ok := m.handlers[p.Role]
		if !ok {
			return asynq.SkipRetry
		}
		return h(ctx, p)
	})
	return mux
}
```

- [ ] **Step 5: 跑测试 + Commit**

```bash
go test ./internal/worker/... -race
git add internal/worker
git commit -m "feat(worker): Asynq Client + Mux（按 role 路由 queue）"
```

---

## Task 27: internal/httpapi — Gin auth + healthz + credential + abort

**Files:**
- Create: `internal/httpapi/middleware.go`
- Create: `internal/httpapi/handlers.go`
- Create: `internal/httpapi/server.go`
- Create: `internal/httpapi/handlers_test.go`

- [ ] **Step 1: 写失败测试**

```go
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/V3teran/liusha/internal/credential"
)

type fakeCred struct {
	saved   map[string][]credential.Identity
	listed  map[string][]credential.Identity
	deleted []string
}

func (f *fakeCred) BatchSave(_ context.Context, byHost map[string][]credential.Identity, _ int) error {
	if f.saved == nil {
		f.saved = map[string][]credential.Identity{}
	}
	for k, v := range byHost {
		f.saved[k] = v
	}
	return nil
}
func (f *fakeCred) GetIdentitiesByHost(_ context.Context, host string) ([]credential.Identity, error) {
	return f.listed[host], nil
}
func (f *fakeCred) Delete(_ context.Context, host string) error {
	f.deleted = append(f.deleted, host)
	return nil
}

type fakeAbort struct{ aborted []string }

func (f *fakeAbort) Abort(_ context.Context, id string) error {
	f.aborted = append(f.aborted, id)
	return nil
}

func TestAuth_RejectsMissingHeader(t *testing.T) {
	srv := httptest.NewServer(NewServer(Deps{APIKey: "k"}))
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/credential?host=h")
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestHealthz_NoAuth(t *testing.T) {
	srv := httptest.NewServer(NewServer(Deps{APIKey: "k"}))
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/healthz")
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestCredentialBatch(t *testing.T) {
	fc := &fakeCred{}
	srv := httptest.NewServer(NewServer(Deps{APIKey: "k", Credentials: fc}))
	defer srv.Close()
	body, _ := json.Marshal(BatchSaveRequest{
		TTLSeconds: 0,
		Credentials: map[string][]credential.Identity{
			"vulnapp": {{Name: "admin", Role: "admin"}},
		},
	})
	req, _ := http.NewRequest("POST", srv.URL+"/credential/batch", bytes.NewReader(body))
	req.Header.Set("X-API-Key", "k")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("status=%d err=%v", resp.StatusCode, err)
	}
	if _, ok := fc.saved["vulnapp"]; !ok {
		t.Fatal("not saved")
	}
}

func TestEngagementAbort(t *testing.T) {
	fa := &fakeAbort{}
	srv := httptest.NewServer(NewServer(Deps{APIKey: "k", Engagements: fa}))
	defer srv.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/engagement/eid-1/abort", nil)
	req.Header.Set("X-API-Key", "k")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 200 || len(fa.aborted) != 1 || fa.aborted[0] != "eid-1" {
		t.Fatalf("status=%d aborted=%v", resp.StatusCode, fa.aborted)
	}
}
```

- [ ] **Step 2: 实现 `internal/httpapi/middleware.go`**

```go
package httpapi

import (
	"strings"

	"github.com/gin-gonic/gin"
)

func RequireAPIKey(expected string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasSuffix(c.FullPath(), "/healthz") {
			c.Next()
			return
		}
		got := c.GetHeader("X-API-Key")
		if expected == "" || got != expected {
			c.AbortWithStatusJSON(401, gin.H{"error": "invalid api key"})
			return
		}
		c.Next()
	}
}
```

- [ ] **Step 3: 实现 `internal/httpapi/handlers.go`**

```go
package httpapi

import (
	"context"

	"github.com/V3teran/liusha/internal/credential"
	"github.com/gin-gonic/gin"
)

type CredentialsAPI interface {
	BatchSave(ctx context.Context, byHost map[string][]credential.Identity, ttlSeconds int) error
	GetIdentitiesByHost(ctx context.Context, host string) ([]credential.Identity, error)
	Delete(ctx context.Context, host string) error
}

type EngagementsAPI interface {
	Abort(ctx context.Context, id string) error
}

type BatchSaveRequest struct {
	TTLSeconds  int                              `json:"ttl_seconds"`
	Credentials map[string][]credential.Identity `json:"credentials"`
}

func batchSaveHandler(api CredentialsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req BatchSaveRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		if err := api.BatchSave(c.Request.Context(), req.Credentials, req.TTLSeconds); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	}
}

func listCredentialHandler(api CredentialsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		host := c.Query("host")
		if host == "" {
			c.JSON(400, gin.H{"error": "host required"})
			return
		}
		ids, err := api.GetIdentitiesByHost(c.Request.Context(), host)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"identities": ids})
	}
}

func deleteCredentialHandler(api CredentialsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		host := c.Query("host")
		if host == "" {
			c.JSON(400, gin.H{"error": "host required"})
			return
		}
		if err := api.Delete(c.Request.Context(), host); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	}
}

func abortHandler(api EngagementsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if err := api.Abort(c.Request.Context(), id); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"ok": true})
	}
}
```

- [ ] **Step 4: 实现 `internal/httpapi/server.go`**

```go
package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type Deps struct {
	APIKey      string
	Credentials CredentialsAPI
	Engagements EngagementsAPI
}

func NewServer(d Deps) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(RequireAPIKey(d.APIKey))

	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	if d.Credentials != nil {
		r.POST("/credential/batch", batchSaveHandler(d.Credentials))
		r.GET("/credential", listCredentialHandler(d.Credentials))
		r.DELETE("/credential", deleteCredentialHandler(d.Credentials))
	}
	if d.Engagements != nil {
		r.POST("/engagement/:id/abort", abortHandler(d.Engagements))
	}
	return r
}
```

- [ ] **Step 5: 跑测试 + Commit**

```bash
go test ./internal/httpapi/... -race
git add internal/httpapi
git commit -m "feat(httpapi): X-API-Key 中间件 + healthz + credential CRUD + engagement abort"
```

---

## Task 28: cmd/api — main 装配

**Files:**
- Create: `cmd/api/main.go`
- Create: `cmd/api/Dockerfile`

- [ ] **Step 1: 实现 `cmd/api/main.go`**

```go
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/logx"
)

func main() {
	logger := logx.New("api")
	ctx := context.Background()

	cfg, err := config.Load(envOr("LIUSHA_CONFIG", "./config/config.yaml"))
	if err != nil {
		logger.Fatal().Err(err).Msg("load config")
	}
	pool, err := db.NewPgPool(ctx, os.Getenv("LIUSHA_POSTGRES_DSN"), cfg.Postgres.MaxConns, cfg.Postgres.MinConns)
	if err != nil {
		logger.Fatal().Err(err).Msg("pg")
	}
	defer pool.Close()
	rdb, err := db.NewRedis(ctx, os.Getenv("LIUSHA_REDIS_ADDR"))
	if err != nil {
		logger.Fatal().Err(err).Msg("redis")
	}
	defer rdb.Close()

	credAPI := credential.NewRedis(rdb)
	engAPI := engagement.NewStore(pool)

	srv := &http.Server{
		Addr: envOr("LIUSHA_API_ADDR", "0.0.0.0:8080"),
		Handler: httpapi.NewServer(httpapi.Deps{
			APIKey:      os.Getenv("LIUSHA_API_KEY"),
			Credentials: credAPI,
			Engagements: engAPI,
		}),
		ReadTimeout:  time.Duration(cfg.API.ReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(cfg.API.WriteTimeoutSeconds) * time.Second,
	}

	go func() {
		logger.Info().Str("addr", srv.Addr).Msg("api listening")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("api serve")
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	logger.Info().Msg("api shutdown")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 2: `cmd/api/Dockerfile`**

```dockerfile
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
ENV CGO_ENABLED=0
RUN go build -mod=vendor -o /out/api ./cmd/api

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=build /out/api /app/api
COPY config /app/config
EXPOSE 8080
ENTRYPOINT ["/app/api"]
```

- [ ] **Step 3: 构建 + Commit**

```bash
go build ./cmd/api
docker build -f cmd/api/Dockerfile -t liusha/api .
git add cmd/api
git commit -m "feat(cmd/api): main 装配 + Dockerfile"
```

---

## Task 29: ~~cmd/proxy~~（已删除）

> 原方案是 Go 自写 cmd/proxy（基于 goproxy）。最终决定用 ProjectDiscovery 的 **proxify** 容器作为 sniffer 来源（spec §2 / §7.4）。
>
> Plan 1 不动 proxy 实体——proxify 作为外部 docker image 在 Plan 2 docker-compose 里加入；JSONL → liusha 的 consumer 装在 Plan 2 T6（`internal/proxify_consumer/`，由 agent-worker 进程内 goroutine 启动）。

跳过此 task；进入 T30。

---

## Task 30: cmd/agent-worker — main 装配（Asynq + role 分发，仅 sniffer）

**Files:**
- Create: `cmd/agent-worker/main.go`
- Create: `cmd/agent-worker/Dockerfile`

- [ ] **Step 1: 实现 `cmd/agent-worker/main.go`**

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/V3teran/liusha/internal/agent/action"
	"github.com/V3teran/liusha/internal/agent/action/middleware"
	"github.com/V3teran/liusha/internal/agent/actions"
	"github.com/V3teran/liusha/internal/agent/llm"
	"github.com/V3teran/liusha/internal/agent/runtime"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/graph"
	"github.com/V3teran/liusha/internal/llmcall"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/observability"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"

	"github.com/hibiken/asynq"
)

const snifferSystemPrompt = `你是 sniffer 角色。每个任务对应一个 traffic_window。
你能用 read_state / write_fact / write_idea / write_hint / write_finding / write_graph / load_skill / spawn_subtask / done(reason)。
plan 2 会扩展 read_window / fetch_credentials / replay_multi_identity / heuristic_check / compute_similarity 等。
done 会被系统校验，调用前请确认目标已达成（或写明 reason 表示主动跳过）。`

func main() {
	logger := logx.New("agent-worker")
	ctx := context.Background()

	cfg, err := config.Load(envOr("LIUSHA_CONFIG", "./config/config.yaml"))
	if err != nil {
		logger.Fatal().Err(err).Msg("config")
	}

	pool, err := db.NewPgPool(ctx, os.Getenv("LIUSHA_POSTGRES_DSN"), cfg.Postgres.MaxConns, cfg.Postgres.MinConns)
	if err != nil {
		logger.Fatal().Err(err).Msg("pg")
	}
	defer pool.Close()

	tasks := task.NewStore(pool)
	engs := engagement.NewStore(pool)
	finds := finding.NewStore(pool)
	graphs := graph.NewStore(pool)
	calls := llmcall.NewStore(pool)
	skillLoader := skill.NewLoader(cfg.Skills.Root)

	// T21.5：构建 LLM Router（按 cfg.LLM.Routes 路由 + retry/fallback）
	router, err := llm.NewRouter(ctx, cfg, calls, observability.DefaultPricing)
	if err != nil {
		logger.Fatal().Err(err).Msg("llm router")
	}

	// T23.5：Distill — finding 写库后，调 light_provider 总结成 hint 写入 memory_hints
	finds.OnSaved(runtime.NewDistillHook(router.For("distill"), engs))

	mux := worker.NewMux()
	h := snifferHandler{
		tasks: tasks, engagements: engs, findings: finds, graphs: graphs, calls: calls,
		cfg: cfg, pricing: observability.DefaultPricing,
		router:              router,
		snifferSystemPrompt: snifferSystemPrompt,
		budget: runtime.Budget{
			MaxSteps: cfg.LLM.MaxSteps, MaxTokens: 50_000, WatchdogSeconds: 60,
		},
	}
	_ = skillLoader // 用于 plan 2 BAC skill 加载，plan 1 仅占位
	mux.Register(worker.RoleSniffer, h.handle)

	srv := asynq.NewServer(asynq.RedisClientOpt{Addr: os.Getenv("LIUSHA_REDIS_ADDR")},
		asynq.Config{Concurrency: 4, Queues: map[string]int{
			worker.QueueSniffer: 5, worker.QueueOperator: 1,
		}})

	hs := &http.Server{Addr: ":9090"}
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	go hs.ListenAndServe()
	defer hs.Close()

	go func() {
		if err := srv.Run(mux.AsynqMux()); err != nil {
			logger.Fatal().Err(err).Msg("asynq run")
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	srv.Shutdown()
	hs.Shutdown(context.Background())
	logger.Info().Msg("agent-worker shutdown")
}

type snifferHandler struct {
	tasks               *task.Store
	engagements         *engagement.Store
	findings            *finding.Store
	graphs              *graph.Store
	calls               *llmcall.Store
	cfg                 config.Config
	pricing             llm.PricingProvider
	router              *llm.Router // T21.5：多 provider 路由（react.main / observer / distill / compaction）
	snifferSystemPrompt string
	budget              runtime.Budget
}

func (h snifferHandler) handle(ctx context.Context, p worker.Payload) error {
	if err := h.tasks.SetRunning(ctx, p.TaskID); err != nil {
		return err
	}

	reg := action.NewRegistry()
	_ = reg.Register(actions.Done{})
	_ = reg.Register(&actions.ReadState{Store: h.engagements, EngagementID: p.EngagementID})
	_ = reg.Register(&actions.WriteFact{Store: h.engagements, EngagementID: p.EngagementID})
	_ = reg.Register(&actions.WriteIdea{Store: h.engagements, EngagementID: p.EngagementID})
	_ = reg.Register(&actions.WriteHint{Store: h.engagements, EngagementID: p.EngagementID})
	_ = reg.Register(&actions.WriteFinding{Store: h.findings, EngagementID: p.EngagementID, TaskID: p.TaskID})
	_ = reg.Register(&actions.WriteGraph{Store: h.graphs, EngagementID: p.EngagementID})

	// T22.5：套上中间件链（result_compress / loop_detect / done_validate）
	reg.Use(
		middleware.ResultCompress(p.EngagementID),
		middleware.LoopDetect(),
		middleware.DoneValidate(skillLoader.DoneValidatorFor(p.SkillName)),
	)

	// 每个 task 一个全新 Generator（tools 一次绑定，避免跨 goroutine 竞争）
	provider, err := llm.BuildProvider(ctx, h.cfg, h.cfg.LLM.DefaultProvider, reg.Schemas())
	if err != nil {
		_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
		return err
	}

	tid, eid := p.TaskID, p.EngagementID
	gen := llm.Instrument(provider, h.calls, llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "react.main"}, h.pricing)

	// T23.5：Observer 走 light_provider；Distill 在 finding.OnSaved 已注册
	obsLLM := h.router.For("observer")
	observer := runtime.NewLLMObserver(obsLLM, h.engagements, eid)

	out, err := runtime.Run(ctx, runtime.Config{
		LLM: gen, Actions: reg, Budget: h.budget,
		SystemPrompt:       h.snifferSystemPrompt,
		UserPrompt:         string(p.Input),
		Observer:           observer,
		ObserverEverySteps: 5,
		OnAbort: func(c context.Context) (bool, error) {
			e, err := h.engagements.GetByID(c, eid)
			if err != nil {
				return false, err
			}
			return e.Status != engagement.StatusActive, nil
		},
	})
	if err != nil {
		_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
		return err
	}
	res, _ := json.Marshal(map[string]any{
		"terminate_by":     out.TerminateBy,
		"total_steps":      out.TotalSteps,
		"total_in":         out.TotalUsage.InTokens,
		"total_out":        out.TotalUsage.OutTokens,
		"total_cached":     out.TotalUsage.CachedTokens,
		"observer_hints":   out.ObserverHints,
		"done_force_count": out.DoneForceCount,
	})
	return h.tasks.SetDone(ctx, p.TaskID, res)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 2: `cmd/agent-worker/Dockerfile`**

```dockerfile
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
ENV CGO_ENABLED=0
RUN go build -mod=vendor -o /out/agent-worker ./cmd/agent-worker

FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=build /out/agent-worker /app/agent-worker
COPY config /app/config
COPY skills /app/skills
EXPOSE 9090
ENTRYPOINT ["/app/agent-worker"]
```

- [ ] **Step 3: 构建 + Commit**

```bash
go build ./cmd/agent-worker
docker build -f cmd/agent-worker/Dockerfile -t liusha/agent-worker .
git add cmd/agent-worker
git commit -m "feat(cmd/agent-worker): main 装配（Asynq mux + sniffer role + ReAct 跑通）"
```

---

## Task 31: cmd/vulnapp — BAC e2e 靶机

**Files:**
- Create: `cmd/vulnapp/main.go`
- Create: `cmd/vulnapp/Dockerfile`

**职责：** Plan 2 e2e 靶机。8 个接口（spec §10.2 表）。监听 `:8001`，zerolog JSON。

- [ ] **Step 1: 实现 `cmd/vulnapp/main.go`**

```go
package main

import (
	"net/http"

	"github.com/V3teran/liusha/internal/logx"
	"github.com/gin-gonic/gin"
)

var sessions = map[string]string{
	"admin_sess_a1b2c3":   "admin",
	"test_sess_d4e5f6":    "test",
	"m233241_sess_g7h8i9": "m233241",
}

var fakeOrders = map[string]any{
	"7": map[string]any{"id": 7, "owner": "test", "amount": 100},
	"9": map[string]any{"id": 9, "owner": "m233241", "amount": 80},
}

func main() {
	logger := logx.New("vulnapp")
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.POST("/login", func(c *gin.Context) {
		var req struct {
			Name string `json:"name"`
		}
		_ = c.ShouldBindJSON(&req)
		var sess string
		switch req.Name {
		case "admin":
			sess = "admin_sess_a1b2c3"
		case "test":
			sess = "test_sess_d4e5f6"
		case "m233241":
			sess = "m233241_sess_g7h8i9"
		default:
			c.JSON(401, gin.H{"error": "unknown user"})
			return
		}
		c.Header("Set-Cookie", "session="+sess+"; Path=/")
		c.JSON(200, gin.H{"ok": true, "user": req.Name})
	})

	authed := r.Group("/api")
	authed.Use(func(c *gin.Context) {
		c.Set("user", whoami(c.Request))
		c.Next()
	})

	authed.GET("/profile", func(c *gin.Context) {
		c.JSON(200, gin.H{"me": c.GetString("user")})
	})
	authed.GET("/user/info", func(c *gin.Context) {
		c.JSON(200, gin.H{"uid": c.Query("uid"), "name": "u" + c.Query("uid")})
	})
	authed.GET("/order/:oid", func(c *gin.Context) {
		v, ok := fakeOrders[c.Param("oid")]
		if !ok {
			c.JSON(404, gin.H{"error": "not found"})
			return
		}
		c.JSON(200, v)
	})
	authed.POST("/order/cancel", func(c *gin.Context) {
		var req struct {
			OrderID string `json:"order_id"`
		}
		_ = c.ShouldBindJSON(&req)
		c.JSON(200, gin.H{"cancelled": req.OrderID})
	})
	authed.GET("/admin/users", func(c *gin.Context) {
		c.JSON(200, gin.H{"users": []string{"admin", "test", "m233241"}})
	})
	authed.POST("/admin/user/delete", func(c *gin.Context) {
		var req struct {
			UID string `json:"uid"`
		}
		_ = c.ShouldBindJSON(&req)
		c.JSON(200, gin.H{"deleted": req.UID, "by": c.GetString("user")})
	})

	logger.Info().Str("addr", ":8001").Msg("vulnapp listening")
	_ = r.Run(":8001")
}

func whoami(r *http.Request) string {
	c, err := r.Cookie("session")
	if err != nil {
		return "anonymous"
	}
	if u, ok := sessions[c.Value]; ok {
		return u
	}
	return "anonymous"
}
```

- [ ] **Step 2: `cmd/vulnapp/Dockerfile`**

```dockerfile
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY . .
ENV CGO_ENABLED=0
RUN go build -mod=vendor -o /out/vulnapp ./cmd/vulnapp

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/vulnapp /app/vulnapp
EXPOSE 8001
ENTRYPOINT ["/app/vulnapp"]
```

- [ ] **Step 3: 构建 + 自测 + Commit**

```bash
go build ./cmd/vulnapp
go run ./cmd/vulnapp &
PID=$!
sleep 1
curl -s -X POST localhost:8001/login -H "content-type: application/json" -d '{"name":"admin"}'
curl -s -b "session=admin_sess_a1b2c3" localhost:8001/api/profile
kill $PID
git add cmd/vulnapp
git commit -m "feat(cmd/vulnapp): BAC e2e 靶机（8 路由 + 固定 session + zerolog）"
```

---

## Task 32: Smoke — compose up + healthz + go test 全绿

- [ ] **Step 1: 启动栈**

```bash
make up
make migrate
LIUSHA_DEEPSEEK_API_KEY=changeme docker compose -f deployments/docker-compose.yml --profile agent up -d --build
```

- [ ] **Step 2: healthz 检查**

```bash
curl -s -H "X-API-Key: changeme-dev-key" http://localhost:8080/healthz | grep '"ok":true'
curl -s http://localhost:9090/healthz | grep '"ok":true'
```

- [ ] **Step 3: 全部单测**

```bash
go test ./... -race -short
```

预期：所有 PASS。

- [ ] **Step 4: 全部集成测试**

```bash
go test -tags=integration ./internal/... -race -count=1
```

预期：所有 PASS（首次跑会拉镜像，~1min+）。

- [ ] **Step 5: 静态检查**

```bash
go vet ./...
gofmt -l . | tee /dev/stderr | (! read)
```

预期：无输出。

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "chore(plan-1): smoke 通过（compose up + healthz + go test 全绿）"
```

---

## Self-Review

| spec 区域 | 覆盖任务 |
|---|---|
| §3.1 Postgres schema（8 表 + UNIQUE） | T4 |
| §3.2 Redis 数据 | T13 凭证；其他在 plan 2 |
| §3.3 凭证 JSON | T13 |
| §4 AgentRuntime ReAct + Observer + Budget | T22 + T22.5（中间件）+ T23 + T23.5（Observer/Distill） |
| §5 角色 / Skill（含 cognitive_map + done_validator） | T25 + T30 注册 sniffer；BAC SKILL.md 占位 |
| §6.1 共享 Actions（read_state / write_fact/idea/hint / write_finding / write_graph / load_skill / spawn_subtask / done(系统校验)） | T24 + T30 装配 |
| §6.5 Action 中间件（result_compress / loop_detect / done_validate） | T22.5 |
| §7.1-7.3 通用 lib（credential / replay / heuristic） | T13 / T14 / T15 |
| §7.4 流量过滤 | proxify DSL（外部容器，T16 已删） |
| §8 LLM multi-provider + Pricing + Instrument | T17 / T18 / T19 / T20 / T21 |
| §8.4 多模型路由 + §8.5 错误码退避 | T21.5（Router + retry） |
| §8.6 经验自蒸馏 | T23.5 distill（finding.OnSaved hook） |
| §9 HTTP API | T27 + T28 |
| §10 e2e 准备 | T31 vulnapp；proxify + BAC sniffer 在 plan 2 |
| §11 成功指标 | T32 部分（go test + 单 Runtime grep）；BAC finding 在 plan 2 |

**已删任务**：
- T16 internal/proxy_filter（用 proxify DSL 替代）
- T29 cmd/proxy（用 proxify 容器替代）

**已确认：** 所有 task 步骤都给完整代码或具体命令；无 "TBD" / "similar to" 等占位。前后引用名一致（`engagement.NewStore`、`runtime.Run`、`llm.Instrument`、`llm.BuildProvider`、`worker.NewMux`、`actions.Done`、`actions.WriteFinding` 等）。

**Plan 2 接力点：**
- proxify 容器加进 docker-compose（plan 2 T8）
- `internal/proxify_consumer/` 装在 agent-worker 进程内 goroutine（plan 2 T6）
- T30 sniffer 只注册 7 个共享 action（done / read_state / write_fact / write_idea / write_hint / write_finding / write_graph）→ plan 2 补 `read_window` / BAC 4 个工具
- T25 BAC SKILL.md 占位 → plan 2 写正文步骤指引

---

> Plan 2（proxify + BAC e2e）见 `2026-04-28-liusha-v1-plan-2-proxy-bac.md`。
