# 多轮对话 Plan 2a：问答 + 意图路由 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让用户在对话里追加消息时，系统用便宜 LLM 判意图：「问答」（解释/总结已挖结果）读黑板回答，不触发扫描；「动作」走 Plan 1 的续接重跑。

**Architecture:** api 进程新建 llm.Router（light provider）。POST /conversations/:id/messages 收到消息 → light LLM 分类 action/qa（模糊偏 qa）→ qa 路径读该 scan 的 finding（owner 黑板）+ light LLM 生成回答 → 落 assistant 消息 + publish SSE（前端 AssistantText 卡片自动渲染）；action 路径走 Plan 1 FollowUpScan（忙则 409，队列留 Plan 2b）。

**Tech Stack:** Go + gin + internal/llm（非流式 Generate）。

**关键事实（实现前必读）：**
- `internal/llm`：`NewRouter(NewFactory(cfg))→*Router`；`router.For(ctx, role)→(Generator, error)`；`Generator.Generate(ctx, []llm.Message, []llm.ToolSchema)→(llm.Result, error)`，`Result.Content` 是文本回复。role 用 `"inspector"`（config `agents.inspector: light_provider` → 便宜模型；复用现成路由，零 config 改动）。
- `llm.Message{Role, Content}`，Role 常量 `llm.RoleSystem/RoleUser/RoleAssistant`。Generate 的 tools 传 `nil`（QA/分类不用工具）。
- 参考 `cmd/scanner/main.go:162`：`llm.NewRouterWithOptions(llm.NewFactory(cfg), llm.RetryOptionsFromConfig(cfg.LLM.Retry))`。
- `internal/finding/store.go`：`ListByOwner(ctx, ownerType, ownerID string)→([]VulnFinding, error)`。VulnFinding 字段名以 `internal/finding/model.go` 实际为准（Severity/Title 等）。ownerType 用 `owner.Active`（`internal/owner`）。
- `internal/scanstream`：`NewPublisher(rdb)→*Publisher`；`Publisher.Publish(ctx, conversationID string, payload []byte)→error`。payload 是 `conversation.Message` 的 JSON（前端 SSE 帧）。
- `internal/conversation`：`AppendMessage(ctx, convID, role, kind, content, metadata)→(Message, error)`；`RoleAssistant`/`KindMessage` 常量；`GetConversation(ctx,id)→Conversation{TaskID}`。
- Plan 1 已有：`internal/httpapi/conversation_handler.go` 的 `FollowUpAPI`（GetConversationScan/AppendUserMessage/FollowUpScan）+ `followUpHandler`，`cmd/api` 的 `activeScanAdapter`（字段 `conversations *conversation.Store`、`activeScans *activescan.Store`；需新增 `router`、`findings`、`publisher`）。
- 前端 liusha-ui：assistant 的 KindMessage 消息已由 `src/components/MessageItem.vue` → `AssistantText.vue` 渲染（阶段D）。QA 回答经 SSE 落 assistant 消息 → ChatThread 自动显示，前端基本无需新组件。

---

## Part A — 后端（liusha Go 仓）

> `cwd = /Users/Xlbula/workspace/programs/go/liusha`，命令前置 `export GOPROXY=https://goproxy.cn,direct`。

### Task A1: cmd/api 构造 llm.Router + adapter 新增依赖字段

**Files:**
- Modify: `cmd/api/main.go`

api 进程此前无 LLM client；本任务构造 router + finding store + publisher，注入 activeScanAdapter，供后续 QA/分类用。

- [ ] **Step 1: 看 activeScanAdapter struct 字段 + main 装配处**

Run: `grep -n 'type activeScanAdapter struct\|activeScans \|conversations \|llm\.\|scanstream\|finding\.' cmd/api/main.go | head`
确认现有字段 + 是否已构造 finding.Store / redis client（publisher 需 rdb）。

- [ ] **Step 2: adapter struct 加字段**

`activeScanAdapter` struct 加：

```go
	router    *llm.Router           // 多轮：意图分类 + 问答（light provider）
	findings  *finding.Store        // 问答读 owner 黑板 finding
	publisher *scanstream.Publisher // 问答回答 publish SSE
```

- [ ] **Step 3: main 构造并注入**

在装配 activeAdapter 处，构造并赋值（router 参考 scanner；finding.Store/publisher 若 main 已有 pool/rdb 直接用）：

```go
	router := llm.NewRouterWithOptions(llm.NewFactory(cfg), llm.RetryOptionsFromConfig(cfg.LLM.Retry))
	findingStore := finding.NewStore(pool)
	publisher := scanstream.NewPublisher(rdb)
```

并在 `&activeScanAdapter{...}` 字面量里加：`router: router, findings: findingStore, publisher: publisher,`

> 注：`pool`（pgxpool）、`rdb`（redis）main 里应已有（GetConversation/EventStream 用了）。`finding.NewStore` 签名以实际为准（`grep -n 'func NewStore' internal/finding/store.go`）。补 import：`internal/llm`、`internal/finding`、`internal/scanstream`（scanstream 可能已 import）。

- [ ] **Step 4: 编译**

Run: `go build ./cmd/api/ 2>&1 | head; echo "BUILD($?)"; gofmt -l cmd/api/main.go`
Expected: BUILD ok + gofmt 无输出

- [ ] **Step 5: 提交**

```bash
git add cmd/api/main.go
git commit -m "feat(api): 构造 llm.Router + finding store + publisher（多轮问答依赖）"
```

---

### Task A2: 意图分类器（light LLM，模糊偏 qa）

**Files:**
- Create: `internal/intent/classify.go`
- Test: `internal/intent/classify_test.go`

纯函数 + 注入 Generator：给 light LLM 一段判定 prompt，输出 `action`/`qa`；解析非这两词或调用失败 → 默认 `qa`（便宜、安全）。

- [ ] **Step 1: 写失败测试 `internal/intent/classify_test.go`**

```go
package intent

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/llm"
)

type fakeGen struct {
	reply string
	err   error
}

func (f fakeGen) Generate(_ context.Context, _ []llm.Message, _ []llm.ToolSchema) (llm.Result, error) {
	return llm.Result{Content: f.reply}, f.err
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name, reply string
		want        Intent
	}{
		{"明确 action", "action", IntentAction},
		{"明确 qa", "qa", IntentQA},
		{"带空格大小写", "  ACTION\n", IntentAction},
		{"模糊输出偏 qa", "我觉得是问答吧", IntentQA},
		{"空输出偏 qa", "", IntentQA},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Classify(context.Background(), fakeGen{reply: c.reply}, "用户消息")
			if got != c.want {
				t.Errorf("reply=%q want %v got %v", c.reply, c.want, got)
			}
		})
	}
}

func TestClassify_ErrorFallsBackQA(t *testing.T) {
	got := Classify(context.Background(), fakeGen{err: context.DeadlineExceeded}, "x")
	if got != IntentQA {
		t.Errorf("调用失败应默认 qa，得 %v", got)
	}
}
```

- [ ] **Step 2: 跑确认失败**

Run: `go test ./internal/intent/ -v`
Expected: FAIL（包不存在）

- [ ] **Step 3: 实现 `internal/intent/classify.go`**

```go
// Package intent 用便宜 LLM 判定用户对话消息的意图：动作（触发扫描）还是问答（读结果回答）。
package intent

import (
	"context"
	"strings"

	"github.com/V3teran/liusha/internal/llm"
)

// Intent 是消息意图。
type Intent string

const (
	IntentAction Intent = "action" // 触发/继续扫描
	IntentQA     Intent = "qa"     // 就已有结果提问
)

// gen 是 Classify 依赖的最小 LLM 接口（llm.Generator 满足）。
type gen interface {
	Generate(ctx context.Context, msgs []llm.Message, tools []llm.ToolSchema) (llm.Result, error)
}

const systemPrompt = `你是渗透测试对话的意图分类器。判断用户最新消息是想"让 agent 执行/继续扫描动作"（action），还是"就已挖到的结果提问/解释/总结"（qa）。
只输出一个词：action 或 qa。无法确定时输出 qa。`

// Classify 调 light LLM 分类；解析不出 action/qa 或调用失败 → 默认 qa（便宜、安全：
// 避免把提问误判成动作而白烧一次扫描）。
func Classify(ctx context.Context, g gen, userMessage string) Intent {
	res, err := g.Generate(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: userMessage},
	}, nil)
	if err != nil {
		return IntentQA
	}
	switch strings.ToLower(strings.TrimSpace(res.Content)) {
	case "action":
		return IntentAction
	case "qa":
		return IntentQA
	default:
		return IntentQA
	}
}
```

- [ ] **Step 4: 跑确认通过**

Run: `go test ./internal/intent/ -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/intent/
git commit -m "feat(intent): light LLM 意图分类（action/qa，模糊偏 qa）"
```

---

### Task A3: 问答服务（读黑板 finding → light LLM → assistant 消息 + SSE）

**Files:**
- Create: `internal/qa/answer.go`
- Test: `internal/qa/answer_test.go`

注入最小接口（finding 读 / LLM / 消息落库 / publish），组装黑板摘要 prompt → 生成回答 → 落 assistant 消息 + publish。

- [ ] **Step 1: 写失败测试 `internal/qa/answer_test.go`**

```go
package qa

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/llm"
)

type fakeDeps struct {
	findings  string
	genReply  string
	appended  string
	published bool
}

func (f *fakeDeps) FindingsSummary(_ context.Context, _, _ string) (string, error) { return f.findings, nil }
func (f *fakeDeps) Generate(_ context.Context, _ []llm.Message, _ []llm.ToolSchema) (llm.Result, error) {
	return llm.Result{Content: f.genReply}, nil
}
func (f *fakeDeps) AppendAssistant(_ context.Context, _, content string) ([]byte, error) {
	f.appended = content
	return json.Marshal(map[string]string{"content": content})
}
func (f *fakeDeps) Publish(_ context.Context, _ string, _ []byte) error { f.published = true; return nil }

func TestAnswer(t *testing.T) {
	d := &fakeDeps{findings: "1. [high] SQLi at /login", genReply: "那个 SQLi 在登录框，参数 user 未过滤。"}
	svc := New(d)
	err := svc.Answer(context.Background(), "conv1", "scan1", "解释下那个 SQLi")
	if err != nil {
		t.Fatal(err)
	}
	if d.appended != "那个 SQLi 在登录框，参数 user 未过滤。" {
		t.Errorf("应落 assistant 回答，得 %q", d.appended)
	}
	if !d.published {
		t.Error("应 publish SSE")
	}
}
```

- [ ] **Step 2: 跑确认失败**

Run: `go test ./internal/qa/ -v`
Expected: FAIL（包不存在）

- [ ] **Step 3: 实现 `internal/qa/answer.go`**

```go
// Package qa 实现多轮对话的问答路径：读 owner 黑板 finding，用便宜 LLM 就已挖结果回答，
// 不触发扫描。回答落 assistant 消息并 publish SSE（前端实时显示）。
package qa

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/llm"
)

// Deps 是问答所需的最小依赖（cmd/api 注入实现）。
type Deps interface {
	// FindingsSummary 返回该 (conversationID, taskID) 关联 owner 的 finding 文本摘要。
	FindingsSummary(ctx context.Context, conversationID, taskID string) (string, error)
	// Generate 调便宜 LLM。
	Generate(ctx context.Context, msgs []llm.Message, tools []llm.ToolSchema) (llm.Result, error)
	// AppendAssistant 落 assistant 消息（KindMessage），返回该消息的 SSE JSON payload。
	AppendAssistant(ctx context.Context, conversationID, content string) ([]byte, error)
	// Publish 把消息 payload 推 SSE。
	Publish(ctx context.Context, conversationID string, payload []byte) error
}

// Service 问答服务。
type Service struct{ deps Deps }

// New 构造问答服务。
func New(deps Deps) *Service { return &Service{deps: deps} }

const systemPrompt = `你是渗透测试助手。**只依据下面已挖到的 finding 回答用户问题**，不要编造未列出的漏洞。
若 finding 为空或不足以回答，如实说明"目前还没挖到相关结果"。回答简洁中文。`

// Answer 读黑板 → LLM 生成回答 → 落 assistant 消息 + publish SSE。
func (s *Service) Answer(ctx context.Context, conversationID, taskID, question string) error {
	findings, err := s.deps.FindingsSummary(ctx, conversationID, taskID)
	if err != nil {
		return fmt.Errorf("读 finding: %w", err)
	}
	res, err := s.deps.Generate(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: "已挖到的 finding：\n" + findings + "\n\n用户问题：" + question},
	}, nil)
	if err != nil {
		return fmt.Errorf("生成回答: %w", err)
	}
	payload, err := s.deps.AppendAssistant(ctx, conversationID, res.Content)
	if err != nil {
		return fmt.Errorf("落 assistant 消息: %w", err)
	}
	// publish best-effort：已落 PG，重连补历史可见，失败不返回错误。
	_ = s.deps.Publish(ctx, conversationID, payload)
	return nil
}
```

- [ ] **Step 4: 跑确认通过**

Run: `go test ./internal/qa/ -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/qa/
git commit -m "feat(qa): 问答服务（读黑板 finding → light LLM → assistant 消息 + SSE）"
```

---

### Task A4: followUpHandler 按意图分流 + adapter 实现 qa 依赖

**Files:**
- Modify: `internal/httpapi/conversation_handler.go`（FollowUpAPI 收敛为 HandleMessage）
- Modify: `cmd/api/main.go`（adapter 实现意图分类 + qa.Deps + 分流）
- Test: `internal/httpapi/conversation_handler_test.go`

把 Plan 1 的"空闲 action / 忙 409"改成：先判意图——qa 任何时候都答（含扫描进行中）；action 空闲触发续接、忙 409（队列 Plan 2b）。分类+qa+分流逻辑放 adapter 的 `HandleMessage`，handler 只调它拿 intent/busy。

- [ ] **Step 1: 写失败测试（改 conversation_handler_test.go）**

把 `fakeFollowUp` 改为实现新接口 `HandleMessage`，并替换 Plan 1 的旧测试为新分流测试：

```go
func (f *fakeFollowUp) HandleMessage(_ context.Context, convID, content string) (string, bool, error) {
	f.calledBrief = content
	return f.handleIntent, f.handleBusy, nil
}

func TestFollowUpHandler_QA(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fu := &fakeFollowUp{convID: "c1", handleIntent: "qa"}
	r := gin.New()
	r.POST("/conversations/:id/messages", followUpHandler(fu))
	req := httptest.NewRequest("POST", "/conversations/c1/messages", strings.NewReader(`{"content":"解释下那个洞"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "qa") {
		t.Errorf("qa 应 200 且含 intent qa，得 code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestFollowUpHandler_ActionIdle_200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fu := &fakeFollowUp{convID: "c1", handleIntent: "action", handleBusy: false}
	r := gin.New()
	r.POST("/conversations/:id/messages", followUpHandler(fu))
	req := httptest.NewRequest("POST", "/conversations/c1/messages", strings.NewReader(`{"content":"再扫"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("action 空闲应 200，得 %d", w.Code)
	}
}

func TestFollowUpHandler_ActionBusy_409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fu := &fakeFollowUp{convID: "c1", handleIntent: "action", handleBusy: true}
	r := gin.New()
	r.POST("/conversations/:id/messages", followUpHandler(fu))
	req := httptest.NewRequest("POST", "/conversations/c1/messages", strings.NewReader(`{"content":"再扫"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 409 {
		t.Errorf("action 忙应 409，得 %d", w.Code)
	}
}
```

并把 `fakeFollowUp` struct 加字段 `handleIntent string`、`handleBusy bool`，**删掉** Plan 1 的旧方法（GetConversationScan/AppendUserMessage/FollowUpScan）与旧测试（`TestFollowUpHandler_IdleScan_TriggersRun`/`_BusyScan_409`，被上面新测试取代）。

- [ ] **Step 2: 跑确认失败**

Run: `go test ./internal/httpapi/ -run TestFollowUpHandler -v`
Expected: FAIL（HandleMessage 未定义）

- [ ] **Step 3: 收敛 FollowUpAPI + 改 handler（conversation_handler.go）**

把 Plan 1 的 `FollowUpAPI`（三方法）整段替换为：

```go
// FollowUpAPI 处理对话追加消息：内部判意图（action/qa）+ 落消息 + 分流。
// 返回 intent（"action"|"qa"）、busy（action 但扫描进行中 → 应排队/拒绝）、err。
type FollowUpAPI interface {
	HandleMessage(ctx context.Context, convID, content string) (intent string, busy bool, err error)
}
```

把 `followUpHandler` 整段替换为：

```go
func followUpHandler(api FollowUpAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Param("id")
		var req FollowUpRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Content == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "content 不能为空"})
			return
		}
		intent, busy, err := api.HandleMessage(c.Request.Context(), convID, req.Content)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if busy {
			c.JSON(http.StatusConflict, gin.H{"error": "扫描进行中，停止后再发", "busy": true, "intent": intent})
			return
		}
		c.JSON(http.StatusOK, gin.H{"intent": intent})
	}
}
```

`FollowUpRequest` 保留不变。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/httpapi/ -run TestFollowUpHandler -v`
Expected: PASS

- [ ] **Step 5: adapter 实现 HandleMessage + qa.Deps（cmd/api/main.go）**

把 Plan 1 的 `GetConversationScan`/`AppendUserMessage` 方法替换为下面这组（`FollowUpScan`、`AbortConversationScan` 保留）：

```go
// HandleMessage 满足 httpapi.FollowUpAPI：落 user 消息 → 判意图 → qa 答 / action 续接。
func (a *activeScanAdapter) HandleMessage(ctx context.Context, convID, content string) (string, bool, error) {
	conv, err := a.conversations.GetConversation(ctx, convID)
	if err != nil {
		return "", false, err
	}
	if _, err := a.conversations.AppendMessage(ctx, convID, conversation.RoleUser, conversation.KindMessage, content, nil); err != nil {
		return "", false, err
	}
	g, err := a.router.For(ctx, "inspector") // light provider
	if err != nil {
		return "", false, err
	}
	switch intent.Classify(ctx, g, content) {
	case intent.IntentAction:
		sc, err := a.activeScans.GetByID(ctx, conv.TaskID)
		if err != nil {
			return "", false, err
		}
		if sc.Status == activescan.StatusActive {
			return "action", true, nil // 忙：队列留 Plan 2b
		}
		if _, err := a.FollowUpScan(ctx, conv.TaskID, convID, "", content); err != nil {
			return "", false, err
		}
		return "action", false, nil
	default: // qa
		if err := qa.New(a).Answer(ctx, convID, conv.TaskID, content); err != nil {
			return "", false, err
		}
		return "qa", false, nil
	}
}

// ---- qa.Deps 实现 ----

// FindingsSummary 满足 qa.Deps：把 owner 黑板 finding 渲染成文本摘要。
func (a *activeScanAdapter) FindingsSummary(ctx context.Context, _, taskID string) (string, error) {
	fs, err := a.findings.ListByOwner(ctx, owner.Active, taskID)
	if err != nil {
		return "", err
	}
	if len(fs) == 0 {
		return "（暂无 finding）", nil
	}
	var b strings.Builder
	for i, f := range fs {
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, f.Severity, f.Title)
	}
	return b.String(), nil
}

// Generate 满足 qa.Deps：调 light provider。
func (a *activeScanAdapter) Generate(ctx context.Context, msgs []llm.Message, tools []llm.ToolSchema) (llm.Result, error) {
	g, err := a.router.For(ctx, "inspector")
	if err != nil {
		return llm.Result{}, err
	}
	return g.Generate(ctx, msgs, tools)
}

// AppendAssistant 满足 qa.Deps：落 assistant 消息，返回 SSE payload。
func (a *activeScanAdapter) AppendAssistant(ctx context.Context, convID, content string) ([]byte, error) {
	msg, err := a.conversations.AppendMessage(ctx, convID, conversation.RoleAssistant, conversation.KindMessage, content, nil)
	if err != nil {
		return nil, err
	}
	return json.Marshal(msg)
}

// Publish 满足 qa.Deps：推 SSE。
func (a *activeScanAdapter) Publish(ctx context.Context, convID string, payload []byte) error {
	return a.publisher.Publish(ctx, convID, payload)
}
```

> 注：补 import `internal/intent`、`internal/qa`、`internal/llm`（owner/activescan/conversation/json/fmt/strings 多已有）。`f.Severity`/`f.Title` 字段名以 `internal/finding/model.go` 实际为准（先 `grep -n 'Severity\|Title' internal/finding/model.go`）。

- [ ] **Step 6: 编译 + 全测 + vet**

Run: `go build ./... && go test ./internal/httpapi/ ./internal/intent/ ./internal/qa/ 2>&1 | tail -4 && go vet ./...`
Expected: BUILD ok + PASS + vet 干净。`gofmt -l` 改过文件无输出。

- [ ] **Step 7: 提交**

```bash
git add internal/httpapi/ cmd/api/main.go
git commit -m "feat(api): 追加消息按意图分流（qa 读黑板答 / action 续接，忙409）"
```

---

## Part B — 前端（liusha-ui 仓）

> `cwd = /Users/Xlbula/workspace/programs/typescript/liusha-ui`，pnpm。

### Task B1: followUp 返回意图提示

**Files:**
- Modify: `src/components/Composer.vue`

QA 回答经 SSE 落 assistant 消息 → ChatThread 的 AssistantText 已自动渲染（阶段D）。本任务仅让 Composer 发送后按 intent 给个轻提示（"正在回答…" / "已触发扫描"）。

- [ ] **Step 1: Composer 发送后按 intent 提示**

`src/components/Composer.vue` 的 `send()` 里，追加分支用 followUp 的返回（把 Plan 1 的 `await followUp(...)` 改为接收返回值）：

```ts
    if (props.convId) {
      const r = await followUp(props.convId, brief.value)
      brief.value = ''
      busyMsg.value = r.intent === 'qa' ? '正在回答…' : '已触发扫描'
      emit('appended')
    } else {
```

> followUp 已声明返回 `{ intent: string; scan_id?: string }`（Plan 1 B1），类型够用，无需改 client.ts。busyMsg 复用现有 ref（提示文案，下次输入自然清空）。

- [ ] **Step 2: 测试 + 构建**

Run: `pnpm test && pnpm build`
Expected: 全 PASS（既有 27 测试不挂）+ dist 产出。

- [ ] **Step 3: 提交**

```bash
git add src/components/Composer.vue
git commit -m "feat(ui): 追加消息按意图提示（问答/触发扫描）"
```

---

## 验收（人工，两仓起来后）

1. Go 仓重启（`./scripts/dev/run-svc.sh`）+ 前端 `pnpm dev`，5173 登入。
2. 发起扫描 → 跑完出 finding。
3. 同对话追加**问句**"解释下那个 SQLi" → 几秒后对话里出现 assistant 回答（不新建 run；DB：agent 行数不增；message 多一条 assistant KindMessage）。
4. 同对话追加**动作**"再深挖那个上传点" → 触发同 scan 新 run（agent 行数+1，active_scan 回 active）。
5. 扫描进行中追加动作 → 前端提示"扫描进行中"（409）；追加问句仍能答（qa 不受扫描状态限制）。

## 测试清单映射

- 意图分类（action/qa/模糊→qa/失败→qa）→ Task A2
- 问答读黑板 + 生成 + 落消息 + publish → Task A3
- 端点按意图分流（qa 200 / action 空闲 200 / action 忙 409）→ Task A4
- api LLM/finding/publisher 装配 → Task A1
- 前端意图提示 → Task B1

## 本计划非目标（留 Plan 2b）

- 动作排队（忙时存 pending + 当前 run done 自动调度）——本计划忙时仍 409
- QA 流式 token（本计划非流式，单次 Generate 出整段回答；llm.Generator 现为非流式）
- QA 读 notes（本计划只读 finding；notes 需 host 维度，留后续）
