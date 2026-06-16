# liusha-ui 对话式前端 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给 liusha 做一个独立 Vue3 对话式前端（选场景 → 自然语言发起扫描 → SSE 实时看 agent 跑命令/出 finding），并在 Go 后端加极小的 cookie 鉴权让 SSE 流可认证。

**Architecture:** 两个仓。(1) liusha Go 仓加「短时效签名 cookie」：`POST /chat` 下发，`/conversations/:id/stream` 用它鉴权（解决 EventSource 不能带 header）。(2) 新建独立 `liusha-ui` Vue3+Vite+TS 仓，靠 Vite proxy（dev）/反代（prod）收敛到同源，原生 `EventSource(withCredentials)` 订阅流。

**Tech Stack:** 后端 Go + gin + crypto/hmac；前端 Vue 3 + Vite + TypeScript + Pinia + Vitest + pnpm。

**关键事实（实现前必读）：**
- `internal/conversation.Message` 与 `internal/einoagent.ScanEvent` **都没有 json tag** → Go 默认序列化为**大写首字母**键。SSE `data:` 帧 JSON 形如：
  ```json
  {"Seq":42,"ID":"u","ConversationID":"c","Role":"tool","Kind":"event","Content":"...","Metadata":{"Kind":"tool_result","ToolName":"run_command","Args":"","Result":"...","DurationMs":1234,"Err":""},"CreatedAt":"2026-06-10T..."}
  ```
  前端 TS 类型必须用这些**大写键**，否则解析全空。
- `Message.Kind`：`"message"`（普通对话）/ `"event"`（agent 过程事件）。
- `Message.Role`：`"user"|"assistant"|"system"|"tool"`。
- `Metadata`（仅 KindEvent 非空）是 `ScanEvent`：`Kind`=`"tool_call"|"tool_result"`，含 `ToolName/Args/Result/DurationMs/Err`。
- `RequireAPIKey` 是**全局** gin 中间件（`internal/httpapi/server.go:50` `r.Use`），会在 handler 之前 401 掉无 header 的请求 → SSE 的 cookie 鉴权**必须改这个中间件**，不能只改 handler。
- 后端鉴权头是 `X-API-Key`（`internal/httpapi/auth.go`）。

---

## Part A — 后端 cookie 鉴权（liusha Go 仓）

> 全部 `cwd = /Users/Xlbula/workspace/programs/go/liusha`，命令前置 `export GOPROXY=https://goproxy.cn,direct`。

### Task A1: 短时效签名 cookie 的签发/校验

**Files:**
- Create: `internal/httpapi/stream_cookie.go`
- Test: `internal/httpapi/stream_cookie_test.go`

Token 格式：`{convID}.{expUnix}.{hexHMAC(convID + "." + expUnix)}`。绑定 convID（泄露的 cookie 只能流这一个对话）+ 过期时间。HMAC-SHA256，密钥由 caller 注入。

- [ ] **Step 1: 写失败测试**

```go
package httpapi

import (
	"testing"
	"time"
)

func TestStreamCookie_SignVerify_RoundTrip(t *testing.T) {
	secret := []byte("test-secret-32-bytes-long-xxxxxx")
	tok := signStreamToken(secret, "conv-123", time.Now().Add(30*time.Minute))
	if tok == "" {
		t.Fatal("signStreamToken 返回空")
	}
	if !verifyStreamToken(secret, tok, "conv-123") {
		t.Error("同 convID 同密钥应校验通过")
	}
}

func TestStreamCookie_Reject(t *testing.T) {
	secret := []byte("test-secret-32-bytes-long-xxxxxx")
	valid := signStreamToken(secret, "conv-123", time.Now().Add(30*time.Minute))

	cases := map[string]bool{
		"错 convID":   verifyStreamToken(secret, valid, "conv-999"),
		"错密钥":        verifyStreamToken([]byte("wrong-secret-32-bytes-long-yyyy"), valid, "conv-123"),
		"篡改 token":   verifyStreamToken(secret, valid+"x", "conv-123"),
		"过期": verifyStreamToken(secret, signStreamToken(secret, "conv-123", time.Now().Add(-time.Minute)), "conv-123"),
		"空 token":     verifyStreamToken(secret, "", "conv-123"),
		"垃圾格式":     verifyStreamToken(secret, "not.a.valid.token", "conv-123"),
	}
	for name, got := range cases {
		if got {
			t.Errorf("%s 应校验失败，却通过", name)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/httpapi/ -run TestStreamCookie -v`
Expected: FAIL（`undefined: signStreamToken`）

- [ ] **Step 3: 实现**

```go
// stream_cookie.go：SSE stream 端点的短时效签名 cookie。
//
// EventSource 不能带自定义 header，故 POST /chat 下发此 cookie，stream 端点据此鉴权。
// token = {convID}.{expUnix}.{hexHMAC}，绑 convID（泄露只影响单对话）+ 过期时间。
package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// streamCookieName 是 SSE 鉴权 cookie 名。
const streamCookieName = "liusha_stream"

// signStreamToken 签发绑定 convID + 过期时间的 token。
func signStreamToken(secret []byte, convID string, exp time.Time) string {
	if len(secret) == 0 || convID == "" {
		return ""
	}
	payload := convID + "." + strconv.FormatInt(exp.Unix(), 10)
	return payload + "." + hexHMAC(secret, payload)
}

// verifyStreamToken 校验 token 的签名、过期、convID 绑定。
func verifyStreamToken(secret []byte, token, convID string) bool {
	if len(secret) == 0 || token == "" {
		return false
	}
	i := strings.LastIndex(token, ".")
	if i < 0 {
		return false
	}
	payload, sig := token[:i], token[i+1:]
	// 常量时间比签名。
	if subtle.ConstantTimeCompare([]byte(sig), []byte(hexHMAC(secret, payload))) != 1 {
		return false
	}
	parts := strings.Split(payload, ".")
	if len(parts) != 2 || parts[0] != convID {
		return false
	}
	expUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > expUnix {
		return false
	}
	return true
}

func hexHMAC(secret []byte, payload string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/httpapi/ -run TestStreamCookie -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/httpapi/stream_cookie.go internal/httpapi/stream_cookie_test.go
git commit -m "feat(httpapi): SSE stream 短时效签名 cookie 签发/校验"
```

---

### Task A2: RequireAPIKey 对 stream 路径加 cookie 旁路

**Files:**
- Modify: `internal/httpapi/auth.go`（`RequireAPIKey` 签名 + 逻辑）
- Modify: `internal/httpapi/server.go:50`（调用点传 secret）
- Test: `internal/httpapi/auth_test.go`（新增 cookie 旁路用例）

中间件改成接受 `streamSecret []byte`。当 `FullPath == "/conversations/:id/stream"` 且请求带合法 `liusha_stream` cookie（convID 对得上 URL param）→ 放行；否则回退 header 校验。

- [ ] **Step 1: 写失败测试**

```go
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRequireAPIKey_StreamCookieBypass(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := []byte("secret-32-bytes-long-aaaaaaaaaaaa")
	r := gin.New()
	r.Use(RequireAPIKey("the-key", secret))
	r.GET("/conversations/:id/stream", func(c *gin.Context) { c.String(200, "ok") })

	// 合法 cookie（无 header）→ 放行
	tok := signStreamToken(secret, "conv-1", time.Now().Add(time.Minute))
	req := httptest.NewRequest("GET", "/conversations/conv-1/stream", nil)
	req.AddCookie(&http.Cookie{Name: streamCookieName, Value: tok})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("合法 cookie 应放行，得 %d", w.Code)
	}

	// 错 convID 的 cookie + 无 header → 401
	req2 := httptest.NewRequest("GET", "/conversations/conv-2/stream", nil)
	req2.AddCookie(&http.Cookie{Name: streamCookieName, Value: tok}) // tok 绑 conv-1
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != 401 {
		t.Errorf("convID 不匹配的 cookie 应 401，得 %d", w2.Code)
	}

	// 无 cookie 无 header → 401
	req3 := httptest.NewRequest("GET", "/conversations/conv-1/stream", nil)
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	if w3.Code != 401 {
		t.Errorf("无凭证应 401，得 %d", w3.Code)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/httpapi/ -run TestRequireAPIKey_StreamCookieBypass -v`
Expected: FAIL（`RequireAPIKey` 现签名只收 1 参，编译错）

- [ ] **Step 3: 改 auth.go**

把 `func RequireAPIKey(expected string) gin.HandlerFunc` 改为下面版本（新增 streamSecret 参数 + cookie 旁路块，插在 `got := c.GetHeader(...)` 之前）：

```go
func RequireAPIKey(expected string, streamSecret []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		fp := c.FullPath()
		path := c.Request.URL.Path
		if strings.HasSuffix(fp, "/healthz") ||
			strings.HasPrefix(fp, "/viewer/") ||
			fp == "/viewer-config.json" ||
			path == "/viewer-config.json" ||
			path == "/favicon.ico" {
			c.Next()
			return
		}
		// SSE stream：EventSource 不能带 header，改用 liusha_stream cookie 鉴权。
		// 仅此路径接受 cookie；cookie 绑 convID，须与 URL param 一致。
		if fp == "/conversations/:id/stream" && len(streamSecret) > 0 {
			if ck, err := c.Cookie(streamCookieName); err == nil &&
				verifyStreamToken(streamSecret, ck, c.Param("id")) {
				c.Next()
				return
			}
		}
		got := c.GetHeader("X-API-Key")
		if expected == "" || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			c.AbortWithStatusJSON(401, gin.H{"error": "invalid api key"})
			return
		}
		c.Next()
	}
}
```

- [ ] **Step 4: 改 server.go 调用点**

`internal/httpapi/server.go:50`，把 `r.Use(RequireAPIKey(d.APIKey))` 改为：

```go
	r.Use(RequireAPIKey(d.APIKey, d.StreamCookieSecret))
```

（`d.StreamCookieSecret` 字段在 Task A3 加；本步先改调用，A3 补字段后整体编译通过——若想本任务独立编译，可同时执行 A3 Step 3 的 Deps 字段新增。）

- [ ] **Step 5: 加 Deps 字段（与 A3 共用，先加以便编译）**

`internal/httpapi/server.go` 的 `Deps` struct 末尾加：

```go
	// StreamCookieSecret 给 SSE stream cookie 签名/校验；空则 stream 仅接受 X-API-Key header。
	// 由 cmd/api 读 LIUSHA_STREAM_COOKIE_SECRET 注入。
	StreamCookieSecret []byte
```

- [ ] **Step 6: 跑测试确认通过 + 全包编译**

Run: `go test ./internal/httpapi/ -run TestRequireAPIKey -v && go build ./...`
Expected: PASS + BUILD ok（注意：现有 auth_test.go 里其它 `RequireAPIKey("...")` 单参调用要补第二参 `nil`——一并改掉）

- [ ] **Step 7: 提交**

```bash
git add internal/httpapi/auth.go internal/httpapi/server.go internal/httpapi/auth_test.go
git commit -m "feat(httpapi): RequireAPIKey 对 SSE stream 加 cookie 旁路鉴权"
```

---

### Task A3: POST /chat 下发 cookie + cmd/api 注入密钥（fail-fast）

**Files:**
- Modify: `internal/httpapi/conversation_handler.go`（`chatHandler` 签名 + 下发 cookie）
- Modify: `internal/httpapi/server.go:80`（调用点传 secret）
- Modify: `cmd/api/main.go`（读 env + fail-fast + 注入 Deps）
- Test: `internal/httpapi/conversation_handler_test.go`（断言 Set-Cookie）

- [ ] **Step 1: 写失败测试**

```go
package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type fakeChat struct{}

func (fakeChat) StartChatScan(_ context.Context, brief, roleID string) (string, string, error) {
	return "conv-abc", "scan-xyz", nil
}

func TestChatHandler_SetsStreamCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := []byte("secret-32-bytes-long-bbbbbbbbbbbb")
	r := gin.New()
	r.POST("/chat", chatHandler(fakeChat{}, secret))

	req := httptest.NewRequest("POST", "/chat", strings.NewReader(`{"brief":"扫这个","role_id":"web-pentest"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var ck *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == streamCookieName {
			ck = c
		}
	}
	if ck == nil {
		t.Fatal("响应未 Set-Cookie liusha_stream")
	}
	if !ck.HttpOnly {
		t.Error("cookie 应 HttpOnly")
	}
	// cookie 绑定返回的 convID，应能校验通过
	if !verifyStreamToken(secret, ck.Value, "conv-abc") {
		t.Error("cookie 值应是 conv-abc 的合法 token")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/httpapi/ -run TestChatHandler_SetsStreamCookie -v`
Expected: FAIL（`chatHandler` 现签名只收 1 参）

- [ ] **Step 3: 改 chatHandler**

`internal/httpapi/conversation_handler.go`，把 `func chatHandler(api ChatAPI) gin.HandlerFunc` 改为下面版本（新增 secret 参；StartChatScan 成功后、写 JSON 前下发 cookie）：

```go
func chatHandler(api ChatAPI, streamSecret []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req ChatRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid json"})
			return
		}
		if req.Brief == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "brief 不能为空"})
			return
		}
		convID, scanID, err := api.StartChatScan(c.Request.Context(), req.Brief, req.RoleID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// 下发 SSE 鉴权 cookie（30min，HttpOnly+SameSite=Lax；同源部署故不需 SameSite=None）。
		// Secure 由 c.SetCookie 第 6 参控制；dev http 同源下设 false 也能种，prod 反代 https 应 true。
		if len(streamSecret) > 0 {
			const ttl = 30 * 60 // 秒
			tok := signStreamToken(streamSecret, convID, time.Now().Add(ttl*time.Second))
			c.SetSameSite(http.SameSiteLaxMode)
			c.SetCookie(streamCookieName, tok, ttl, "/conversations", "", false, true)
		}
		c.JSON(http.StatusOK, ChatResponse{ConversationID: convID, ScanID: scanID})
	}
}
```

> 注：`time` 已在该文件 import（streamHandler 用了 `time.Time{}`）。`http` 同理。无需新增 import。

- [ ] **Step 4: 改 server.go 调用点**

`internal/httpapi/server.go:80`，`r.POST("/chat", chatHandler(d.Chat))` 改为：

```go
		r.POST("/chat", chatHandler(d.Chat, d.StreamCookieSecret))
```

- [ ] **Step 5: cmd/api 读 env + fail-fast + 注入**

在 `cmd/api/main.go` 装配 `httpapi.Deps` 处（搜 `Chat:` 注入行附近），先读 env：

```go
	// SSE stream cookie 密钥：对话功能开启时必填（EventSource 鉴权用），缺失 fail-fast。
	streamSecret := []byte(os.Getenv("LIUSHA_STREAM_COOKIE_SECRET"))
	if len(streamSecret) == 0 {
		logger.Fatal().Msg("LIUSHA_STREAM_COOKIE_SECRET 未配置——SSE stream cookie 鉴权需要它（fail-fast）")
	}
```

并在 `httpapi.Deps{...}` 字面量里加一行：

```go
			StreamCookieSecret: streamSecret,
```

> 若 `os` 未 import 则补；`logger` 是已有 zerolog 实例。

- [ ] **Step 6: 跑测试 + 编译 + 启动冒烟**

Run:
```bash
go test ./internal/httpapi/ -run 'TestChatHandler|TestRequireAPIKey|TestStreamCookie' -v && go build ./...
```
Expected: PASS + BUILD ok

- [ ] **Step 7: 提交**

```bash
git add internal/httpapi/conversation_handler.go internal/httpapi/server.go internal/httpapi/conversation_handler_test.go cmd/api/main.go
git commit -m "feat(httpapi): POST /chat 下发 SSE 鉴权 cookie + cmd/api 注入密钥(fail-fast)"
```

---

## Part B — liusha-ui 前端新仓（Vue3+Vite+TS）

> 全部 `cwd = /Users/Xlbula/workspace/programs/typescript/liusha-ui`（Task B1 创建）。
> 包管理用 `pnpm`。

### Task B1: 脚手架 + Vite proxy + Vitest

**Files:**
- Create: 整个 Vite 工程（`pnpm create vite`）
- Create: `vite.config.ts`（proxy + vitest）
- Create: `.gitignore`、`README.md`

- [ ] **Step 1: 创建工程**

Run:
```bash
mkdir -p /Users/Xlbula/workspace/programs/typescript/liusha-ui
cd /Users/Xlbula/workspace/programs/typescript/liusha-ui
pnpm create vite . --template vue-ts
pnpm add pinia
pnpm add -D vitest @vue/test-utils jsdom
```
Expected: 生成 `src/`、`package.json`、`tsconfig.json`

- [ ] **Step 2: 写 vite.config.ts（proxy 收敛同源 + vitest）**

```ts
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// dev：/api/* 与 SSE 代理到 Go API（默认 localhost:8080），浏览器视角同源 →
// cookie 走 SameSite=Lax、无需 CORS（见 spec §3）。
export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        rewrite: (p) => p.replace(/^\/api/, ''),
      },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
  },
})
```

- [ ] **Step 3: 加 test script**

`package.json` 的 `scripts` 加：`"test": "vitest run"`、`"test:watch": "vitest"`。

- [ ] **Step 4: 冒烟**

Run: `pnpm test --reporter=verbose; pnpm build`
Expected: vitest 报"no test files"（正常）；build 产出 `dist/`

- [ ] **Step 5: 初始化 git + 提交**

```bash
cd /Users/Xlbula/workspace/programs/typescript/liusha-ui
git init && git add -A
git commit -m "chore: 脚手架 Vue3+Vite+TS + Vitest + dev proxy"
```

---

### Task B2: 后端镜像类型 + API 客户端

**Files:**
- Create: `src/api/types.ts`（镜像 Go 大写键）
- Create: `src/api/client.ts`（fetch 包装，带 X-API-Key）
- Test: `src/api/types.test.ts`

- [ ] **Step 1: 写失败测试（验证类型守卫解析大写键帧）**

```ts
import { describe, it, expect } from 'vitest'
import { isEventMessage, parseScanEvent } from './types'

describe('SSE 帧解析（Go 大写键）', () => {
  const frame = {
    Seq: 42, ID: 'm1', ConversationID: 'c1', Role: 'tool', Kind: 'event',
    Content: 'done', Metadata: { Kind: 'tool_result', ToolName: 'run_command', Args: '', Result: 'ok', DurationMs: 12, Err: '' },
    CreatedAt: '2026-06-10T00:00:00Z',
  }
  it('识别 event 消息', () => {
    expect(isEventMessage(frame as any)).toBe(true)
  })
  it('解析 ScanEvent metadata', () => {
    const ev = parseScanEvent(frame as any)
    expect(ev?.ToolName).toBe('run_command')
    expect(ev?.Kind).toBe('tool_result')
  })
  it('KindMessage 无 metadata 返回 null', () => {
    const m = { ...frame, Kind: 'message', Metadata: null }
    expect(parseScanEvent(m as any)).toBeNull()
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `pnpm test src/api/types.test.ts`
Expected: FAIL（模块不存在）

- [ ] **Step 3: 实现 types.ts**

```ts
// 后端 wire 类型镜像。注意：Go 端 Message/ScanEvent 无 json tag → 键是大写首字母。
export type MessageKind = 'message' | 'event'
export type MessageRole = 'user' | 'assistant' | 'system' | 'tool'
export type ScanEventKind = 'tool_call' | 'tool_result'

export interface ScanEvent {
  Kind: ScanEventKind
  ToolName: string
  Args: string
  Result: string
  DurationMs: number
  Err: string
}

export interface Message {
  Seq: number
  ID: string
  ConversationID: string
  Role: MessageRole
  Kind: MessageKind
  Content: string
  Metadata: ScanEvent | null
  CreatedAt: string
}

export interface Conversation {
  ID: string
  Title: string
  ScanID: string
  RoleID: string
  Status: string
  CreatedAt: string
  UpdatedAt: string
}

export interface Role {
  id: string
  name: string
  description: string
  mode: string
}

export function isEventMessage(m: Message): boolean {
  return m.Kind === 'event'
}

export function parseScanEvent(m: Message): ScanEvent | null {
  if (m.Kind !== 'event' || !m.Metadata) return null
  return m.Metadata
}
```

- [ ] **Step 4: 实现 client.ts**

```ts
import type { Conversation, Message, Role } from './types'

// API key 存 sessionStorage（仅本浏览器会话），所有非 SSE 请求带 X-API-Key header。
const KEY_STORAGE = 'liusha_api_key'

export function setApiKey(k: string) { sessionStorage.setItem(KEY_STORAGE, k) }
export function getApiKey(): string { return sessionStorage.getItem(KEY_STORAGE) ?? '' }

async function get<T>(path: string): Promise<T> {
  const res = await fetch('/api' + path, { headers: { 'X-API-Key': getApiKey() } })
  if (!res.ok) throw new Error(`GET ${path} → ${res.status}`)
  return res.json()
}

export async function listRoles(): Promise<Role[]> {
  return (await get<{ roles: Role[] }>('/roles')).roles
}
export async function listConversations(): Promise<Conversation[]> {
  return (await get<{ conversations: Conversation[] }>('/conversations')).conversations
}
export async function listMessages(convID: string, afterSeq = 0): Promise<Message[]> {
  return (await get<{ messages: Message[] }>(`/conversations/${convID}/messages?after_seq=${afterSeq}`)).messages
}

// 发起对话扫描；成功后后端 Set-Cookie liusha_stream（SSE 鉴权用）。
export async function startChat(brief: string, roleID: string): Promise<{ conversation_id: string; scan_id: string }> {
  const res = await fetch('/api/chat', {
    method: 'POST',
    headers: { 'X-API-Key': getApiKey(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ brief, role_id: roleID }),
  })
  if (!res.ok) throw new Error(`POST /chat → ${res.status}`)
  return res.json()
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `pnpm test src/api/types.test.ts`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add src/api/
git commit -m "feat(api): 后端镜像类型 + fetch 客户端"
```

---

### Task B3: 消息 store（按 seq 有序去重）

**Files:**
- Create: `src/stores/conversation.ts`
- Test: `src/stores/conversation.test.ts`

- [ ] **Step 1: 写失败测试**

```ts
import { setActivePinia, createPinia } from 'pinia'
import { beforeEach, describe, it, expect } from 'vitest'
import { useConversationStore } from './conversation'
import type { Message } from '../api/types'

const msg = (seq: number): Message => ({
  Seq: seq, ID: `m${seq}`, ConversationID: 'c1', Role: 'tool', Kind: 'event',
  Content: '', Metadata: null, CreatedAt: '2026-06-10T00:00:00Z',
})

describe('conversation store', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('按 seq 升序插入', () => {
    const s = useConversationStore()
    s.ingest(msg(3)); s.ingest(msg(1)); s.ingest(msg(2))
    expect(s.messages.map((m) => m.Seq)).toEqual([1, 2, 3])
  })

  it('seq 去重（重连补历史与实时重叠）', () => {
    const s = useConversationStore()
    s.ingest(msg(1)); s.ingest(msg(1))
    expect(s.messages).toHaveLength(1)
  })

  it('lastSeq 反映最大 seq', () => {
    const s = useConversationStore()
    s.ingest(msg(5)); s.ingest(msg(2))
    expect(s.lastSeq).toBe(5)
  })

  it('reset 清空', () => {
    const s = useConversationStore()
    s.ingest(msg(1)); s.reset()
    expect(s.messages).toHaveLength(0)
    expect(s.lastSeq).toBe(0)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `pnpm test src/stores/conversation.test.ts`
Expected: FAIL（模块不存在）

- [ ] **Step 3: 实现**

```ts
import { defineStore } from 'pinia'
import type { Message } from '../api/types'

// 按 seq 有序去重持有当前对话的消息。SSE 补历史 + 实时可能重叠，靠 seq 去重。
export const useConversationStore = defineStore('conversation', {
  state: () => ({
    messages: [] as Message[],
    seqSet: new Set<number>(),
    lastSeq: 0,
  }),
  actions: {
    ingest(m: Message) {
      if (this.seqSet.has(m.Seq)) return
      this.seqSet.add(m.Seq)
      // 二分插入保持升序（事件多数尾部追加，但补历史可能乱序到达）。
      let lo = 0, hi = this.messages.length
      while (lo < hi) {
        const mid = (lo + hi) >> 1
        if (this.messages[mid].Seq < m.Seq) lo = mid + 1
        else hi = mid
      }
      this.messages.splice(lo, 0, m)
      if (m.Seq > this.lastSeq) this.lastSeq = m.Seq
    },
    reset() {
      this.messages = []
      this.seqSet = new Set()
      this.lastSeq = 0
    },
  },
})
```

- [ ] **Step 4: 跑测试确认通过**

Run: `pnpm test src/stores/conversation.test.ts`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add src/stores/
git commit -m "feat(store): 对话消息 store（seq 有序去重）"
```

---

### Task B4: useEventStream composable（EventSource + withCredentials）

**Files:**
- Create: `src/composables/useEventStream.ts`
- Test: `src/composables/useEventStream.test.ts`

原生 EventSource 自带重连 + Last-Event-ID；`withCredentials:true` 自动带 liusha_stream cookie。composable 把每帧 JSON.parse 成 Message 灌进 store。

- [ ] **Step 1: 写失败测试（mock EventSource）**

```ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { openEventStream } from './useEventStream'
import { useConversationStore } from '../stores/conversation'

class MockES {
  static last: MockES
  url: string
  withCredentials: boolean
  onmessage: ((e: { data: string; lastEventId: string }) => void) | null = null
  closed = false
  constructor(url: string, init?: { withCredentials?: boolean }) {
    this.url = url
    this.withCredentials = !!init?.withCredentials
    MockES.last = this
  }
  close() { this.closed = true }
}

describe('useEventStream', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.stubGlobal('EventSource', MockES as any)
  })

  it('用 withCredentials 订阅正确 URL，帧灌进 store', () => {
    const store = useConversationStore()
    const handle = openEventStream('conv-1', store)
    expect(MockES.last.url).toContain('/api/conversations/conv-1/stream')
    expect(MockES.last.withCredentials).toBe(true)

    MockES.last.onmessage!({
      data: JSON.stringify({ Seq: 1, ID: 'm1', ConversationID: 'conv-1', Role: 'tool', Kind: 'event', Content: 'x', Metadata: null, CreatedAt: '' }),
      lastEventId: '1',
    })
    expect(store.messages).toHaveLength(1)
    handle.close()
    expect(MockES.last.closed).toBe(true)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `pnpm test src/composables/useEventStream.test.ts`
Expected: FAIL（模块不存在）

- [ ] **Step 3: 实现**

```ts
import type { Message } from '../api/types'
import type { useConversationStore } from '../stores/conversation'

type Store = ReturnType<typeof useConversationStore>

export interface StreamHandle {
  close(): void
}

// 订阅某对话的 SSE 流。EventSource 自带重连 + Last-Event-ID 续传；
// withCredentials 让浏览器自动带 liusha_stream cookie（同源部署）。
export function openEventStream(convID: string, store: Store): StreamHandle {
  const es = new EventSource(`/api/conversations/${convID}/stream`, { withCredentials: true })
  es.onmessage = (e: MessageEvent) => {
    try {
      const m = JSON.parse(e.data) as Message
      store.ingest(m)
    } catch {
      // 坏帧忽略（不该发生；后端帧是 json.Marshal）
    }
  }
  return { close: () => es.close() }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `pnpm test src/composables/useEventStream.test.ts`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add src/composables/
git commit -m "feat(composable): useEventStream 订阅 SSE（withCredentials + 灌 store）"
```

---

### Task B5: 事件卡片组件 + 映射

**Files:**
- Create: `src/components/cards/UserBubble.vue`、`AssistantText.vue`、`ToolCallCard.vue`、`ToolResultCard.vue`、`FindingCard.vue`
- Create: `src/components/MessageItem.vue`（按 Kind/Role/Metadata 分发到卡片）
- Test: `src/components/MessageItem.test.ts`

- [ ] **Step 1: 写失败测试（映射）**

```ts
import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import MessageItem from './MessageItem.vue'
import type { Message } from '../api/types'

const base = { ID: 'm', ConversationID: 'c', CreatedAt: '', Content: '' }

function mk(over: Partial<Message>): Message {
  return { Seq: 1, Role: 'tool', Kind: 'event', Metadata: null, ...base, ...over } as Message
}

describe('MessageItem 分发', () => {
  it('user message → UserBubble', () => {
    const w = mount(MessageItem, { props: { msg: mk({ Role: 'user', Kind: 'message', Content: '扫这个' }) } })
    expect(w.find('[data-card="user"]').exists()).toBe(true)
  })
  it('tool_call → ToolCallCard 含工具名', () => {
    const w = mount(MessageItem, { props: { msg: mk({ Metadata: { Kind: 'tool_call', ToolName: 'run_command', Args: 'ls', Result: '', DurationMs: 0, Err: '' } }) } })
    expect(w.find('[data-card="tool-call"]').text()).toContain('run_command')
  })
  it('tool_result+Err → 错误态', () => {
    const w = mount(MessageItem, { props: { msg: mk({ Metadata: { Kind: 'tool_result', ToolName: 'x', Args: '', Result: '', DurationMs: 5, Err: 'boom' } }) } })
    expect(w.find('[data-card="tool-result"][data-error="true"]').exists()).toBe(true)
  })
  it('write_finding 结果 → FindingCard', () => {
    const w = mount(MessageItem, { props: { msg: mk({ Metadata: { Kind: 'tool_result', ToolName: 'write_finding', Args: '', Result: 'SQLi at /login', DurationMs: 3, Err: '' } }) } })
    expect(w.find('[data-card="finding"]').exists()).toBe(true)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `pnpm test src/components/MessageItem.test.ts`
Expected: FAIL（组件不存在）

- [ ] **Step 3: 实现卡片组件**

`src/components/cards/UserBubble.vue`：
```vue
<script setup lang="ts">
defineProps<{ content: string }>()
</script>
<template>
  <div class="card user" data-card="user">{{ content }}</div>
</template>
```

`src/components/cards/AssistantText.vue`：
```vue
<script setup lang="ts">
defineProps<{ content: string }>()
</script>
<template>
  <div class="card assistant" data-card="assistant">{{ content }}</div>
</template>
```

`src/components/cards/ToolCallCard.vue`：
```vue
<script setup lang="ts">
import { ref } from 'vue'
defineProps<{ tool: string; args: string }>()
const open = ref(false)
</script>
<template>
  <div class="card tool-call" data-card="tool-call">
    <button class="tool-head" @click="open = !open">▸ 调用 <code>{{ tool }}</code></button>
    <pre v-if="open && args" class="args">{{ args }}</pre>
  </div>
</template>
```

`src/components/cards/ToolResultCard.vue`：
```vue
<script setup lang="ts">
defineProps<{ tool: string; result: string; durationMs: number; err: string }>()
</script>
<template>
  <div class="card tool-result" data-card="tool-result" :data-error="!!err">
    <span class="meta"><code>{{ tool }}</code> · {{ durationMs }}ms</span>
    <pre v-if="err" class="err">{{ err }}</pre>
    <pre v-else class="out">{{ result }}</pre>
  </div>
</template>
```

`src/components/cards/FindingCard.vue`：
```vue
<script setup lang="ts">
defineProps<{ result: string }>()
</script>
<template>
  <div class="card finding" data-card="finding">
    <span class="badge">FINDING</span>
    <span class="text">{{ result }}</span>
  </div>
</template>
```

- [ ] **Step 4: 实现 MessageItem.vue 分发**

```vue
<script setup lang="ts">
import { computed } from 'vue'
import type { Message } from '../api/types'
import UserBubble from './cards/UserBubble.vue'
import AssistantText from './cards/AssistantText.vue'
import ToolCallCard from './cards/ToolCallCard.vue'
import ToolResultCard from './cards/ToolResultCard.vue'
import FindingCard from './cards/FindingCard.vue'

const props = defineProps<{ msg: Message }>()

// 卡片类型判定：普通消息看 Role；事件看 Metadata.Kind；write_finding 结果走 FindingCard。
const kind = computed(() => {
  const m = props.msg
  if (m.Kind === 'message') return m.Role === 'user' ? 'user' : 'assistant'
  const ev = m.Metadata
  if (!ev) return 'assistant'
  if (ev.Kind === 'tool_call') return 'tool-call'
  if (ev.ToolName === 'write_finding' && !ev.Err) return 'finding'
  return 'tool-result'
})
</script>
<template>
  <UserBubble v-if="kind === 'user'" :content="msg.Content" />
  <AssistantText v-else-if="kind === 'assistant'" :content="msg.Content" />
  <ToolCallCard v-else-if="kind === 'tool-call'" :tool="msg.Metadata!.ToolName" :args="msg.Metadata!.Args" />
  <FindingCard v-else-if="kind === 'finding'" :result="msg.Metadata!.Result" />
  <ToolResultCard
    v-else
    :tool="msg.Metadata!.ToolName"
    :result="msg.Metadata!.Result"
    :duration-ms="msg.Metadata!.DurationMs"
    :err="msg.Metadata!.Err"
  />
</template>
```

- [ ] **Step 5: 跑测试确认通过**

Run: `pnpm test src/components/MessageItem.test.ts`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add src/components/
git commit -m "feat(ui): 事件卡片组件 + MessageItem 分发映射"
```

---

### Task B6: 容器组件 + App 装配

**Files:**
- Create: `src/components/ApiKeyGate.vue`、`RolePicker.vue`、`ConversationList.vue`、`Composer.vue`、`ChatThread.vue`
- Modify: `src/App.vue`、`src/main.ts`（挂 Pinia）
- Test: `src/components/ChatThread.test.ts`

- [ ] **Step 1: 写失败测试（ChatThread 渲染 store 消息）**

```ts
import { describe, it, expect, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import ChatThread from './ChatThread.vue'
import { useConversationStore } from '../stores/conversation'

describe('ChatThread', () => {
  beforeEach(() => setActivePinia(createPinia()))
  it('渲染 store 里每条消息为一个 MessageItem', () => {
    const s = useConversationStore()
    s.ingest({ Seq: 1, ID: 'm1', ConversationID: 'c', Role: 'user', Kind: 'message', Content: 'hi', Metadata: null, CreatedAt: '' })
    s.ingest({ Seq: 2, ID: 'm2', ConversationID: 'c', Role: 'tool', Kind: 'event', Content: '', Metadata: { Kind: 'tool_call', ToolName: 'run_command', Args: '', Result: '', DurationMs: 0, Err: '' }, CreatedAt: '' })
    const w = mount(ChatThread)
    expect(w.findAll('[data-card]')).toHaveLength(2)
  })
})
```

- [ ] **Step 2: 跑测试确认失败**

Run: `pnpm test src/components/ChatThread.test.ts`
Expected: FAIL（组件不存在）

- [ ] **Step 3: 实现 ChatThread.vue**

```vue
<script setup lang="ts">
import { useConversationStore } from '../stores/conversation'
import MessageItem from './MessageItem.vue'
const store = useConversationStore()
</script>
<template>
  <div class="thread">
    <MessageItem v-for="m in store.messages" :key="m.Seq" :msg="m" />
  </div>
</template>
```

- [ ] **Step 4: 实现 ApiKeyGate.vue**

```vue
<script setup lang="ts">
import { ref } from 'vue'
import { getApiKey, setApiKey } from '../api/client'
const emit = defineEmits<{ ready: [] }>()
const key = ref(getApiKey())
function save() { setApiKey(key.value); if (key.value) emit('ready') }
</script>
<template>
  <div class="gate">
    <input v-model="key" type="password" placeholder="X-API-Key" @keyup.enter="save" />
    <button @click="save">进入</button>
  </div>
</template>
```

- [ ] **Step 5: 实现 RolePicker.vue**

```vue
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { listRoles } from '../api/client'
import type { Role } from '../api/types'
const roles = ref<Role[]>([])
const model = defineModel<string>()
onMounted(async () => { roles.value = await listRoles(); if (!model.value && roles.value[0]) model.value = roles.value[0].id })
</script>
<template>
  <select v-model="model">
    <option v-for="r in roles" :key="r.id" :value="r.id">{{ r.name }}（{{ r.mode }}）</option>
  </select>
</template>
```

- [ ] **Step 6: 实现 Composer.vue**

```vue
<script setup lang="ts">
import { ref } from 'vue'
import { startChat } from '../api/client'
import RolePicker from './RolePicker.vue'
const brief = ref('')
const roleID = ref('')
const emit = defineEmits<{ started: [convID: string] }>()
async function send() {
  if (!brief.value.trim()) return
  const { conversation_id } = await startChat(brief.value, roleID.value)
  brief.value = ''
  emit('started', conversation_id)
}
</script>
<template>
  <div class="composer">
    <RolePicker v-model="roleID" />
    <textarea v-model="brief" placeholder="描述要扫的目标 / 任务…" @keydown.meta.enter="send" />
    <button @click="send">发起</button>
  </div>
</template>
```

- [ ] **Step 7: 实现 ConversationList.vue**

```vue
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { listConversations } from '../api/client'
import type { Conversation } from '../api/types'
const items = ref<Conversation[]>([])
const emit = defineEmits<{ select: [convID: string] }>()
async function refresh() { items.value = await listConversations() }
onMounted(refresh)
defineExpose({ refresh })
</script>
<template>
  <aside class="conv-list">
    <button class="refresh" @click="refresh">↻</button>
    <ul>
      <li v-for="c in items" :key="c.ID" @click="emit('select', c.ID)">
        {{ c.Title || c.ID.slice(0, 8) }}
      </li>
    </ul>
  </aside>
</template>
```

- [ ] **Step 8: 装配 App.vue**

```vue
<script setup lang="ts">
import { ref } from 'vue'
import { getApiKey, listMessages } from './api/client'
import { useConversationStore } from './stores/conversation'
import { openEventStream, type StreamHandle } from './composables/useEventStream'
import ApiKeyGate from './components/ApiKeyGate.vue'
import ConversationList from './components/ConversationList.vue'
import Composer from './components/Composer.vue'
import ChatThread from './components/ChatThread.vue'

const ready = ref(!!getApiKey())
const store = useConversationStore()
let handle: StreamHandle | null = null

async function open(convID: string) {
  handle?.close()
  store.reset()
  for (const m of await listMessages(convID)) store.ingest(m)
  handle = openEventStream(convID, store)
}
</script>
<template>
  <ApiKeyGate v-if="!ready" @ready="ready = true" />
  <div v-else class="app">
    <ConversationList @select="open" />
    <main>
      <ChatThread />
      <Composer @started="open" />
    </main>
  </div>
</template>
```

- [ ] **Step 9: main.ts 挂 Pinia**

```ts
import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import './style.css'

createApp(App).use(createPinia()).mount('#app')
```

- [ ] **Step 10: 跑测试 + 构建**

Run: `pnpm test && pnpm build`
Expected: 全 PASS + build 产出 dist/

- [ ] **Step 11: 提交**

```bash
git add src/
git commit -m "feat(ui): 容器组件 + App 装配（发起/订阅/回看打通）"
```

---

### Task B7: 技术终端风样式 + README

**Files:**
- Modify: `src/style.css`（设计 token + 终端风）
- Create: `README.md`（dev/build/反代部署说明）

- [ ] **Step 1: 写 style.css（设计 token + 终端风）**

```css
:root {
  --bg: #0d1117;
  --surface: #161b22;
  --border: #30363d;
  --text: #c9d1d9;
  --muted: #8b949e;
  --mono: ui-monospace, SFMono-Regular, "JetBrains Mono", monospace;
  --accent: #58a6ff;
  --sev-critical: #f85149;
  --sev-high: #ff7b35;
  --sev-medium: #d29922;
  --sev-low: #6e7681;
  --space: 12px;
}
* { box-sizing: border-box; }
body { margin: 0; background: var(--bg); color: var(--text); font-family: system-ui, sans-serif; }
code, pre, .args, .out, .err, .meta { font-family: var(--mono); }
.app { display: grid; grid-template-columns: 240px 1fr; height: 100vh; }
.conv-list { background: var(--surface); border-right: 1px solid var(--border); padding: var(--space); overflow-y: auto; }
.conv-list li { padding: 8px; cursor: pointer; border-radius: 6px; }
.conv-list li:hover { background: var(--bg); }
main { display: flex; flex-direction: column; min-height: 0; }
.thread { flex: 1; overflow-y: auto; padding: var(--space); display: flex; flex-direction: column; gap: 8px; }
.card { border: 1px solid var(--border); border-radius: 6px; padding: 8px 10px; background: var(--surface); }
.card.user { align-self: flex-end; background: #1f6feb22; border-color: var(--accent); max-width: 70%; }
.card.tool-call .tool-head { background: none; border: none; color: var(--accent); cursor: pointer; font-family: var(--mono); }
.card.tool-result[data-error="true"] { border-color: var(--sev-critical); }
.card.tool-result .err { color: var(--sev-critical); }
.card.finding { border-color: var(--sev-high); display: flex; gap: 8px; align-items: center; }
.card.finding .badge { background: var(--sev-high); color: #000; font-family: var(--mono); font-size: 11px; padding: 1px 6px; border-radius: 3px; }
pre { margin: 6px 0 0; white-space: pre-wrap; word-break: break-word; max-height: 320px; overflow-y: auto; }
.meta { color: var(--muted); font-size: 12px; }
.composer { display: flex; gap: 8px; padding: var(--space); border-top: 1px solid var(--border); background: var(--surface); }
.composer textarea { flex: 1; min-height: 48px; background: var(--bg); color: var(--text); border: 1px solid var(--border); border-radius: 6px; padding: 8px; font-family: inherit; resize: vertical; }
.composer button, .composer select, .gate button, .gate input { background: var(--bg); color: var(--text); border: 1px solid var(--border); border-radius: 6px; padding: 8px 12px; }
.gate { height: 100vh; display: grid; place-content: center; gap: 8px; }
```

- [ ] **Step 2: 写 README.md**

````markdown
# liusha-ui

liusha 对话式扫描前端（Vue 3 + Vite + TS）。详见 liusha 仓
`docs/superpowers/specs/2026-06-10-liusha-ui-conversational-frontend-design.md`。

## Dev

需要 liusha Go API 跑在 `localhost:8080`，且后端已设 `LIUSHA_STREAM_COOKIE_SECRET`。

```bash
pnpm install
pnpm dev   # Vite dev server，/api/* 代理到 localhost:8080（同源 → cookie 生效）
```

打开页面输入 X-API-Key，选场景，输入任务发起扫描，agent 过程实时流式展示。

## Build & 部署

```bash
pnpm build   # 产出 dist/
```

prod 用反向代理把 dist/ 与 Go API 挂同一域名（`/` → dist，`/api/*` → Go API），
保持同源，使 `liusha_stream` cookie（SameSite=Lax）生效。

## Test

```bash
pnpm test    # vitest：类型解析 / store 去重 / 事件映射 / 组件渲染
```
````

- [ ] **Step 3: 跑全部测试 + 构建确认绿**

Run: `pnpm test && pnpm build`
Expected: 全 PASS + dist/ 产出

- [ ] **Step 4: 提交**

```bash
git add src/style.css README.md
git commit -m "feat(ui): 技术终端风样式 + README"
```

---

## 验收（人工，两仓都起来后）

1. liusha Go 仓：`export LIUSHA_STREAM_COOKIE_SECRET=$(openssl rand -hex 32)`，重启 api/scanner/proxy（新代码）。
2. liusha-ui：`pnpm dev`，浏览器开 Vite URL，输入 X-API-Key。
3. 选场景（web-pentest），输入 brief 发起 → 应见 UserBubble，随后流式 ToolCallCard / ToolResultCard，挖到漏洞出 FindingCard。
4. 刷新页面 → ConversationList 点回历史对话 → 历史消息补齐，仍在跑的继续流式。
5. 断网几秒再恢复 → EventSource 自动重连，带 Last-Event-ID 续读不重复。

## 测试清单映射（spec §8）

- SSE 帧解析（大写键）→ Task B2
- Metadata→卡片映射 → Task B5
- seq 去重 / 有序 → Task B3
- 重连续读（withCredentials + Last-Event-ID）→ Task B4 + 原生 EventSource
- 各卡片渲染（折叠/错误/严重度）→ Task B5
- cookie 签发/校验 → Task A1
- streamHandler cookie 鉴权分支 → Task A2
- POST /chat 下发 cookie → Task A3
- E2E 主流程（可选 Playwright）→ 验收步骤 3（手动；自动化留后续）
