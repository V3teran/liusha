# 多轮对话 Plan 1：动作续接 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让用户在同一对话里追加指令，agent 在**同一 active_scan**（共享持久化黑板当记忆）上接着扫描；并能停止正在跑的扫描。

**Architecture:** 一对话=一 active_scan。追加消息 → 若 scan 空闲（completed/aborted）则**重开**为 active + 入队新 agent run（brief=新消息，黑板由 BuildUserPrompt 自动注入已有 finding/notes）；若正在跑则返回 busy（队列留 Plan 2）。停止=abort active_scan，现有 watchAbortActive 轮询到非 active 即 cancel。

**Tech Stack:** Go + gin + pgx（后端）；Vue3 + Vite + TS（前端 liusha-ui）。

**关键事实（实现前必读）：**
- `internal/conversation/store.go`：`GetConversation(ctx,id)→(Conversation,error)`、`AppendMessage(ctx,convID,role,kind,content,metadata)→(Message,error)`、`ListMessages(ctx,convID,afterSeq,limit)`。`Conversation` 有 `ScanID string`（关联的 active_scan）。
- `internal/activescan/store.go`：`GetByID`、`Abort(ctx,id,errMsg)`、`Complete(ctx,id)`。Status：`active`/`aborted`/`completed`。**无 Reopen**（本计划 Task 1 加）。
- `internal/activescan/model.go`：`StatusActive/StatusAborted/StatusCompleted Status`（type Status string）。
- 动作重跑靠黑板：`internal/builder/hunter/user_prompt.go` 的 `BuildUserPrompt` 注入"该 host 已有 finding（限本 owner）"+ notes/lesson —— 同 owner 新 run 自动看到先前产出，无需重塞 LLM 上下文。
- `cmd/api/main.go` 的 `activeScanAdapter`（约 290 行起）有字段 `activeScans/tasks/enq/audit/conversations/roles`，方法 `createScan(ctx,brief,convID,scenarioID)→(scanID,hunterID,error)` 负责建 active_scan + hunter run + 入 asynq（`worker.RoleHunter`，`worker.Payload{HunterID,OwnerType:owner.Active,OwnerID,ConversationID,ScenarioID,Input}`，`asynq.MaxRetry(0)`）。
- httpapi 路由注册在 `internal/httpapi/server.go`，`Conversations`/`Chat` 等接口经 `Deps` 注入；对话路由在 `if d.Conversations != nil { ... }` 块内。
- 全局鉴权 `RequireAPIKey`（auth.go）已覆盖所有非 /healthz、/dev-config.json 路由。
- 前端 liusha-ui：`src/api/client.ts`（`startChat`/`listMessages` 等，带 X-API-Key）、`src/App.vue`（`open(convID)` 切换+订阅）、`src/components/Composer.vue`（输入+`startChat`→emit started）、`src/components/ConversationList.vue`。

---

## Part A — 后端（liusha Go 仓）

> 全部 `cwd = /Users/Xlbula/workspace/programs/go/liusha`，命令前置 `export GOPROXY=https://goproxy.cn,direct`。

### Task A1: activescan.Store.Reopen（completed/aborted → active）

**Files:**
- Modify: `internal/activescan/store.go`
- Test: `internal/activescan/store_integration_test.go`（追加；若用 build tag/需 DB，按现有测试同款方式）

`Reopen` 把一个已终态（completed/aborted）的 scan 状态置回 active，清空 ended_at/error_message，供动作续接重跑用。

- [ ] **Step 1: 看现有 Complete/Abort 实现对齐写法**

Run: `sed -n '88,125p' internal/activescan/store.go`
Expected: 看到 Abort/Complete 的 UPDATE 语句风格（表名 active_scan、字段 status/ended_at/error_message）。

- [ ] **Step 2: 写 Reopen 方法**

在 `internal/activescan/store.go` 末尾加：

```go
// Reopen 把已终态（completed/aborted）的 scan 置回 active，清 ended_at/error_message。
// 用于多轮对话的动作续接：同一 scan 上重跑 agent，复用 owner 作用域黑板。
func (s *Store) Reopen(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE active_scan SET
			status='active',
			ended_at=NULL,
			error_message=''
		WHERE id=$1`, id)
	if err != nil {
		return fmt.Errorf("reopen active_scan %s: %w", id, err)
	}
	return nil
}
```

> 注：确认文件已 import `fmt`（Abort/Complete 用了 `fmt.Errorf`）。字段名以 Step 1 看到的实际 schema 为准（若 error_message 列不接空串而是 NULL，用 `error_message=NULL`）。

- [ ] **Step 3: 编译 + 现有测试不挂**

Run: `go build ./internal/activescan/ && go test ./internal/activescan/ 2>&1 | tail -3`
Expected: BUILD ok；测试 PASS 或 skip（集成测试可能需 DB，无 DB 时 skip 属正常）。

- [ ] **Step 4: 提交**

```bash
git add internal/activescan/store.go internal/activescan/store_integration_test.go
git commit -m "feat(activescan): Reopen 方法（终态→active，供动作续接重跑）"
```

---

### Task A2: activeScanAdapter.FollowUpScan（重开 + 在同一 scan 入队新 run）

**Files:**
- Modify: `cmd/api/main.go`（`activeScanAdapter` 加方法；可能新增 `activeScans` 接口的 Reopen）

把 createScan 里"入 asynq 队列"那段复用：FollowUpScan 不新建 active_scan，而是 Reopen 现有 scan + 建 hunter run + 入队（brief=追加消息）。

- [ ] **Step 1: 看 createScan 全文确认入队片段**

Run: `sed -n '315,384p' cmd/api/main.go`
Expected: 看到 `a.activeScans.Create` → `a.tasks.Create(... Role:"orchestrator" ...)` → `a.enq.Enqueue(... worker.Payload{...} ..., asynq.MaxRetry(0))`。

- [ ] **Step 2: 确认 activeScans 字段类型有 Reopen**

`activeScanAdapter.activeScans` 是 `*activescan.Store`（Task A1 已加 Reopen）。若它在 main.go 里是经某接口注入，给该接口补 `Reopen(ctx, id string) error`。Run: `grep -n 'activeScans ' cmd/api/main.go`

- [ ] **Step 3: 加 FollowUpScan 方法**

在 `cmd/api/main.go` 的 `activeScanAdapter` 方法区（createScan 之后）加：

```go
// FollowUpScan 在已有 active_scan 上发起一次续接 run（多轮动作）：重开 scan + 建 hunter run +
// 入队（brief=追加消息）。复用 owner 作用域黑板——新 orchestrator 经 BuildUserPrompt 看到先前 finding/notes。
// 入队 Payload 与 createScan 同构，仅 OwnerID 复用传入 scanID、不新建 active_scan。
func (a *activeScanAdapter) FollowUpScan(ctx context.Context, scanID, conversationID, scenarioID, brief string) (string, error) {
	if err := a.activeScans.Reopen(ctx, scanID); err != nil {
		return "", fmt.Errorf("reopen scan: %w", err)
	}
	body, err := json.Marshal(map[string]string{"brief": brief})
	if err != nil {
		return "", fmt.Errorf("marshal brief: %w", err)
	}
	payloadInput, err := json.Marshal(map[string]any{"mode": "active", "entrypoint": json.RawMessage(body)})
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	tid, err := a.tasks.Create(ctx, hunter.NewParams{
		OwnerType: owner.Active,
		OwnerID:   scanID,
		Role:      "orchestrator",
		Input:     payloadInput,
	})
	if err != nil {
		return "", fmt.Errorf("create hunter run: %w", err)
	}
	if _, _, err := a.enq.Enqueue(ctx, worker.RoleHunter, worker.Payload{
		HunterID:       tid,
		OwnerType:      owner.Active,
		OwnerID:        scanID,
		ConversationID: conversationID,
		ScenarioID:     scenarioID,
		Input:          payloadInput,
	}, asynq.MaxRetry(0)); err != nil {
		return "", fmt.Errorf("enqueue followup: %w", err)
	}
	return tid, nil
}
```

- [ ] **Step 4: 编译**

Run: `go build ./cmd/api/ 2>&1 | head; echo done`
Expected: 编译通过（若 activeScans 是接口需补 Reopen，按报错补上）。

- [ ] **Step 5: 提交**

```bash
git add cmd/api/main.go
git commit -m "feat(api): activeScanAdapter.FollowUpScan（同一 scan 重开+入队续接 run）"
```

---

### Task A3: POST /conversations/:id/messages 端点（追加消息 → 动作续接）

**Files:**
- Modify: `internal/httpapi/conversation_handler.go`（加 handler + 接口）
- Modify: `internal/httpapi/server.go`（注册路由 + Deps）
- Modify: `cmd/api/main.go`（注入 adapter）
- Test: `internal/httpapi/conversation_handler_test.go`

语义：追加 user 消息 → 若关联 scan 处于 active（正在跑）→ 409 busy（队列留 Plan 2）；否则触发 FollowUpScan。

- [ ] **Step 1: 写失败测试**

`internal/httpapi/conversation_handler_test.go` 追加：

```go
type fakeFollowUp struct {
	convID, scanID, status string
	calledBrief            string
}

func (f *fakeFollowUp) GetConversationScan(_ context.Context, convID string) (scanID, scanStatus string, err error) {
	if convID != f.convID {
		return "", "", errNotFound
	}
	return f.scanID, f.status, nil
}
func (f *fakeFollowUp) AppendUserMessage(_ context.Context, _, _ string) error { return nil }
func (f *fakeFollowUp) FollowUpScan(_ context.Context, scanID, convID, scenarioID, brief string) (string, error) {
	f.calledBrief = brief
	return "hunter-1", nil
}

var errNotFound = fmt.Errorf("not found")

func TestFollowUpHandler_IdleScan_TriggersRun(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fu := &fakeFollowUp{convID: "c1", scanID: "s1", status: "completed"}
	r := gin.New()
	r.POST("/conversations/:id/messages", followUpHandler(fu))

	req := httptest.NewRequest("POST", "/conversations/c1/messages", strings.NewReader(`{"content":"深挖那个 IDOR"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
	}
	if fu.calledBrief != "深挖那个 IDOR" {
		t.Errorf("brief 应透传给 FollowUpScan，得 %q", fu.calledBrief)
	}
}

func TestFollowUpHandler_BusyScan_409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fu := &fakeFollowUp{convID: "c1", scanID: "s1", status: "active"}
	r := gin.New()
	r.POST("/conversations/:id/messages", followUpHandler(fu))
	req := httptest.NewRequest("POST", "/conversations/c1/messages", strings.NewReader(`{"content":"再测下"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 409 {
		t.Errorf("scan 正在跑应 409 busy，得 %d", w.Code)
	}
}
```

- [ ] **Step 2: 跑确认失败**

Run: `go test ./internal/httpapi/ -run TestFollowUpHandler -v`
Expected: FAIL（followUpHandler/FollowUpAPI 未定义）

- [ ] **Step 3: 加接口 + handler（conversation_handler.go）**

```go
// FollowUpAPI 是多轮动作续接的窄接口（cmd/api 注入 adapter 实现）。
type FollowUpAPI interface {
	// GetConversationScan 返回对话关联的 scanID 与该 scan 当前状态。
	GetConversationScan(ctx context.Context, convID string) (scanID, scanStatus string, err error)
	// AppendUserMessage 把用户追加消息落库（KindMessage / RoleUser）。
	AppendUserMessage(ctx context.Context, convID, content string) error
	// FollowUpScan 在已有 scan 上重开+入队续接 run，返回 hunterID。
	FollowUpScan(ctx context.Context, scanID, convID, scenarioID, brief string) (string, error)
}

// FollowUpRequest 是 POST /conversations/:id/messages 请求体。
type FollowUpRequest struct {
	Content string `json:"content"`
}

// followUpHandler 处理追加消息：落消息 → scan 空闲则触发续接 run，正在跑则 409 busy（队列 Plan 2）。
func followUpHandler(api FollowUpAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Param("id")
		var req FollowUpRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.Content == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "content 不能为空"})
			return
		}
		scanID, status, err := api.GetConversationScan(c.Request.Context(), convID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "conversation 不存在"})
			return
		}
		if err := api.AppendUserMessage(c.Request.Context(), convID, req.Content); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// scan 正在跑 → 拒绝（队列留 Plan 2）；前端可提示"停止当前扫描后再发"。
		if status == "active" {
			c.JSON(http.StatusConflict, gin.H{"error": "扫描进行中，停止后再发", "busy": true})
			return
		}
		if _, err := api.FollowUpScan(c.Request.Context(), scanID, convID, "", req.Content); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"intent": "action", "scan_id": scanID})
	}
}
```

- [ ] **Step 4: 注册路由（server.go）**

在 `if d.Conversations != nil { ... }` 块内（messages GET 附近）加（用新的 `d.FollowUp` 字段，nil 时不注册）：

```go
		if d.FollowUp != nil {
			r.POST("/conversations/:id/messages", followUpHandler(d.FollowUp))
		}
```

并在 `Deps` struct 加字段：

```go
	// FollowUp 为 nil 时 POST /conversations/:id/messages 不注册（多轮动作续接）。
	FollowUp FollowUpAPI
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/httpapi/ -run TestFollowUpHandler -v`
Expected: PASS

- [ ] **Step 6: cmd/api 实现接口 + 注入**

`activeScanAdapter` 加两个方法（FollowUpScan 已在 A2 加；补 GetConversationScan + AppendUserMessage）满足 `FollowUpAPI`：

```go
// GetConversationScan 满足 httpapi.FollowUpAPI：查对话关联 scan + 其状态。
func (a *activeScanAdapter) GetConversationScan(ctx context.Context, convID string) (string, string, error) {
	conv, err := a.conversations.GetConversation(ctx, convID)
	if err != nil {
		return "", "", err
	}
	if conv.ScanID == "" {
		return "", "", fmt.Errorf("conversation 无关联 scan")
	}
	sc, err := a.activeScans.GetByID(ctx, conv.ScanID)
	if err != nil {
		return "", "", err
	}
	return conv.ScanID, string(sc.Status), nil
}

// AppendUserMessage 满足 httpapi.FollowUpAPI：落用户追加消息。
func (a *activeScanAdapter) AppendUserMessage(ctx context.Context, convID, content string) error {
	_, err := a.conversations.AppendMessage(ctx, convID, conversation.RoleUser, conversation.KindMessage, content, nil)
	return err
}
```

在 `httpapi.Deps{...}` 字面量加：`FollowUp: activeAdapter,`

- [ ] **Step 7: 编译 + 全测**

Run: `go build ./... && go test ./internal/httpapi/ 2>&1 | tail -2`
Expected: BUILD ok + PASS

- [ ] **Step 8: 提交**

```bash
git add internal/httpapi/conversation_handler.go internal/httpapi/conversation_handler_test.go internal/httpapi/server.go cmd/api/main.go
git commit -m "feat(api): POST /conversations/:id/messages 动作续接（空闲触发重跑，忙则 409）"
```

---

### Task A4: POST /conversations/:id/abort 端点（停止扫描）

**Files:**
- Modify: `internal/httpapi/conversation_handler.go`（加 handler）
- Modify: `internal/httpapi/server.go`（注册）
- Modify: `cmd/api/main.go`（adapter 加 AbortConversationScan）
- Test: `internal/httpapi/conversation_handler_test.go`

停止=abort 对话关联的 active_scan；scanner 的 watchAbortActive 轮询到非 active 即 cancel 当前 run。

- [ ] **Step 1: 写失败测试**

```go
type abortFn func(context.Context, string) error

func (f abortFn) AbortConversationScan(ctx context.Context, convID string) error { return f(ctx, convID) }

func TestAbortHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	called := ""
	r := gin.New()
	r.POST("/conversations/:id/abort", abortConversationHandler(abortFn(func(_ context.Context, convID string) error {
		called = convID
		return nil
	})))
	req := httptest.NewRequest("POST", "/conversations/c1/abort", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 || called != "c1" {
		t.Errorf("abort 应调用并 200，得 code=%d called=%q", w.Code, called)
	}
}
```

- [ ] **Step 2: 跑确认失败**

Run: `go test ./internal/httpapi/ -run TestAbortHandler -v`
Expected: FAIL（未定义）

- [ ] **Step 3: 加接口 + handler**

```go
// AbortAPI 停止对话关联扫描（cmd/api 注入）。
type AbortAPI interface {
	AbortConversationScan(ctx context.Context, convID string) error
}

// abortConversationHandler 处理 POST /conversations/:id/abort：停掉对话关联的 active_scan。
func abortConversationHandler(api AbortAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := api.AbortConversationScan(c.Request.Context(), c.Param("id")); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"aborted": true})
	}
}
```

- [ ] **Step 4: 注册路由（server.go，FollowUp 块附近）**

```go
		if d.Abort != nil {
			r.POST("/conversations/:id/abort", abortConversationHandler(d.Abort))
		}
```

`Deps` 加：`Abort AbortAPI`

- [ ] **Step 5: cmd/api adapter 实现 + 注入**

```go
// AbortConversationScan 满足 httpapi.AbortAPI：abort 对话关联的 active_scan。
func (a *activeScanAdapter) AbortConversationScan(ctx context.Context, convID string) error {
	conv, err := a.conversations.GetConversation(ctx, convID)
	if err != nil {
		return err
	}
	if conv.ScanID == "" {
		return fmt.Errorf("conversation 无关联 scan")
	}
	return a.activeScans.Abort(ctx, conv.ScanID, "用户停止")
}
```

`Deps{...}` 加：`Abort: activeAdapter,`

- [ ] **Step 6: 跑测试 + 编译**

Run: `go test ./internal/httpapi/ -run TestAbortHandler -v && go build ./...`
Expected: PASS + BUILD ok

- [ ] **Step 7: 提交**

```bash
git add internal/httpapi/conversation_handler.go internal/httpapi/conversation_handler_test.go internal/httpapi/server.go cmd/api/main.go
git commit -m "feat(api): POST /conversations/:id/abort 停止对话关联扫描"
```

---

## Part B — 前端（liusha-ui 仓）

> `cwd = /Users/Xlbula/workspace/programs/typescript/liusha-ui`，pnpm。

### Task B1: client 加 followUp / abortScan

**Files:**
- Modify: `src/api/client.ts`
- Test: `src/api/client.test.ts`（已存在，追加）

- [ ] **Step 1: 写失败测试（追加到 src/api/client.test.ts）**

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { followUp, abortScan, setApiKey } from './client'

describe('多轮 client', () => {
  beforeEach(() => { setApiKey('k'); vi.restoreAllMocks() })

  it('followUp POST 到 /conversations/:id/messages 带 content', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({ intent: 'action', scan_id: 's1' }) })
    vi.stubGlobal('fetch', fetchMock)
    const r = await followUp('c1', '深挖')
    expect(fetchMock).toHaveBeenCalledWith('/api/conversations/c1/messages', expect.objectContaining({ method: 'POST' }))
    expect(r.intent).toBe('action')
  })

  it('abortScan POST 到 /conversations/:id/abort', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({ aborted: true }) })
    vi.stubGlobal('fetch', fetchMock)
    await abortScan('c1')
    expect(fetchMock).toHaveBeenCalledWith('/api/conversations/c1/abort', expect.objectContaining({ method: 'POST' }))
  })
})
```

- [ ] **Step 2: 跑确认失败**

Run: `pnpm test src/api/client.test.ts`
Expected: FAIL（followUp/abortScan 未导出）

- [ ] **Step 3: 实现（src/api/client.ts 追加）**

```ts
// 多轮：往已有对话追加动作消息。busy(409) 时抛错带 busy 标记，前端提示停止后再发。
export async function followUp(convID: string, content: string): Promise<{ intent: string; scan_id?: string }> {
  const res = await fetch(`/api/conversations/${convID}/messages`, {
    method: 'POST',
    headers: { 'X-API-Key': getApiKey(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ content }),
  })
  if (res.status === 409) {
    const err = new Error('扫描进行中') as Error & { busy?: boolean }
    err.busy = true
    throw err
  }
  if (!res.ok) throw new Error(`POST messages → ${res.status}`)
  return res.json()
}

// 停止对话关联的扫描。
export async function abortScan(convID: string): Promise<void> {
  const res = await fetch(`/api/conversations/${convID}/abort`, {
    method: 'POST',
    headers: { 'X-API-Key': getApiKey() },
  })
  if (!res.ok) throw new Error(`POST abort → ${res.status}`)
}
```

- [ ] **Step 4: 跑确认通过**

Run: `pnpm test src/api/client.test.ts`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add src/api/client.ts src/api/client.test.ts
git commit -m "feat(api): followUp + abortScan 客户端"
```

---

### Task B2: App 装配多轮（追加 vs 新建、停止、当前对话态）

**Files:**
- Modify: `src/App.vue`、`src/components/Composer.vue`、`src/components/ConversationList.vue`

交互：有当前对话时 Composer 发送走 `followUp`；「+ 新对话」按钮清空当前对话回到新建模式；「停止扫描」按钮调 abortScan。

- [ ] **Step 1: Composer 支持两种模式**

`src/components/Composer.vue` 改为：有 `convId` prop 时走追加、否则走新建：

```vue
<script setup lang="ts">
import { ref } from 'vue'
import { startChat, followUp } from '../api/client'
import RolePicker from './RolePicker.vue'
const props = defineProps<{ convId?: string }>()
const brief = ref('')
const roleID = ref('')
const busyMsg = ref('')
const emit = defineEmits<{ started: [convID: string]; appended: [] }>()
async function send() {
  if (!brief.value.trim()) return
  busyMsg.value = ''
  try {
    if (props.convId) {
      await followUp(props.convId, brief.value)
      brief.value = ''
      emit('appended')
    } else {
      const { conversation_id } = await startChat(brief.value, roleID.value)
      brief.value = ''
      emit('started', conversation_id)
    }
  } catch (e) {
    const err = e as Error & { busy?: boolean }
    busyMsg.value = err.busy ? '扫描进行中，先点停止再发' : '发送失败'
  }
}
</script>
<template>
  <div class="composer">
    <RolePicker v-if="!convId" v-model="roleID" />
    <textarea v-model="brief" :placeholder="convId ? '追加指令（在同一目标上继续扫描）…' : '描述要扫的目标 / 任务…'" @keydown.meta.enter="send" />
    <button @click="send">{{ convId ? '追加' : '发起' }}</button>
    <span v-if="busyMsg" class="busy">{{ busyMsg }}</span>
  </div>
</template>
```

- [ ] **Step 2: App 传 convId + 新对话/停止**

`src/App.vue`：

```vue
<script setup lang="ts">
import { ref } from 'vue'
import { getApiKey, listMessages, abortScan } from './api/client'
import { useConversationStore } from './stores/conversation'
import { openEventStream, type StreamHandle } from './composables/useEventStream'
import ApiKeyGate from './components/ApiKeyGate.vue'
import ConversationList from './components/ConversationList.vue'
import Composer from './components/Composer.vue'
import ChatThread from './components/ChatThread.vue'

const ready = ref(!!getApiKey())
const store = useConversationStore()
const currentConv = ref<string>('')
let handle: StreamHandle | null = null

async function open(convID: string) {
  handle?.close()
  store.reset()
  currentConv.value = convID
  for (const m of await listMessages(convID)) store.ingest(m)
  handle = openEventStream(convID, store)
}
function newConversation() {
  handle?.close()
  store.reset()
  currentConv.value = ''
}
async function stop() {
  if (currentConv.value) await abortScan(currentConv.value)
}
</script>
<template>
  <ApiKeyGate v-if="!ready" @ready="ready = true" />
  <div v-else class="app">
    <ConversationList @select="open" @new="newConversation" />
    <main>
      <div class="thread-head" v-if="currentConv">
        <button @click="stop">停止扫描</button>
      </div>
      <ChatThread />
      <Composer :conv-id="currentConv || undefined" @started="open" @appended="() => {}" />
    </main>
  </div>
</template>
```

- [ ] **Step 3: ConversationList 加「+ 新对话」按钮**

`src/components/ConversationList.vue`：

```vue
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { listConversations } from '../api/client'
import type { Conversation } from '../api/types'
const items = ref<Conversation[]>([])
const emit = defineEmits<{ select: [convID: string]; new: [] }>()
async function refresh() { items.value = await listConversations() }
onMounted(refresh)
defineExpose({ refresh })
</script>
<template>
  <aside class="conv-list">
    <button class="new-conv" @click="emit('new')">+ 新对话</button>
    <button class="refresh" @click="refresh">↻</button>
    <ul>
      <li v-for="c in items" :key="c.ID" @click="emit('select', c.ID)">
        {{ c.Title || c.ID.slice(0, 8) }}
      </li>
    </ul>
  </aside>
</template>
```

- [ ] **Step 4: 测试 + 构建**

Run: `pnpm test && pnpm build`
Expected: 全 PASS（既有测试不挂）+ dist/ 产出。
注：ChatThread.test 等不受影响；Composer 现多了可选 convId prop，既有挂载无 prop 仍走新建分支。

- [ ] **Step 5: 提交**

```bash
git add src/
git commit -m "feat(ui): 多轮装配（Composer 追加/新建、新对话按钮、停止扫描）"
```

---

## 验收（人工，两仓起来后）

1. Go 仓重启服务（`./scripts/dev/run-svc.sh`，已含 cookie secret）。
2. liusha-ui `VITE_API_TARGET=http://localhost:8090 pnpm dev`，开 5173 登入。
3. 选场景发起一次扫描 → 等它跑完（orchestrator done）。
4. 在**同一对话**输入框追加"深挖刚才那个漏洞" → 应触发**同一 scan** 上的新 run（DB：`SELECT count(*) FROM hunter WHERE owner_id=<scanID>` 增加；active_scan 状态回 active 再 completed）；新 run 经 BuildUserPrompt 能看到上一轮 finding。
5. 扫描进行中再追加 → 前端提示"扫描进行中，先点停止再发"；点「停止扫描」→ 当前 run 被 cancel（active_scan→aborted）。
6. 点「+ 新对话」→ 清空，回新建模式。

## 测试清单映射

- activescan.Reopen → Task A1
- 同一 scan 续接入队 → Task A2 + 验收步骤 4
- 追加端点（空闲触发 / 忙 409）→ Task A3
- 停止扫描 → Task A4 + 验收步骤 5
- 前端 followUp/abort 客户端 → Task B1
- 前端追加/新建/停止/新对话 → Task B2

## 本计划非目标（留 Plan 2）

- 意图路由（action/qa 分流）+ 问答路径（读黑板回答）
- 动作排队（忙时自动 pending + 当前 run 完成自动调度）——本计划忙时直接 409
- 对话历史 user 消息注入 orchestrator（本计划 brief 仅新消息，靠黑板当记忆）
