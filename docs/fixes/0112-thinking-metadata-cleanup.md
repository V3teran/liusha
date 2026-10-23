# Fix 0112: 彻底修复 thinking 字段类型不一致问题

## 问题描述

**症状**：点击对话模块某个对话时前端崩溃，报错：
```
Objects are not valid as a React child (found: object with keys {})
```

**根本原因**：后端 `metadata.thinking` 字段类型不一致：
1. **预期**：`string | undefined`（类型定义）
2. **实际**：可能是 `""` 空字符串、`{}` 空对象、或 `null`

导致 React 尝试渲染对象时崩溃。

## 问题根源分析

### 后端问题（主因）

**文件**：`cmd/api/adapters.go:515-525`

**旧代码**：
```go
func (a *scanAdapter) AppendAssistant(ctx context.Context, convID, content, thinking string) ([]byte, error) {
	meta, err := json.Marshal(map[string]string{"thinking": thinking})
	if err != nil {
		meta = nil
	}
	msg, err := a.conversations.AppendMessage(ctx, convID, conversation.RoleAssistant, conversation.KindMessage, content, meta)
	// ...
}
```

**问题**：
1. `thinking=""` 时序列化为 `{"thinking":""}`，前端收到空字符串（虽不崩溃但冗余）
2. `json.Marshal` 失败时 `meta=nil`，导致数据库存 `NULL`
3. 某些边缘情况（如旧版本数据迁移）可能产生 `{"thinking":{}}`，前端解析后变成空对象 `{}`，**直接导致 React 崩溃**

### 前端问题（次因）

**文件**：`web/src/features/conversation/TimelineThread.tsx:284-298`

**旧代码**：
```typescript
function ThinkingFold({ text }: { text?: string }) {
  if (!text) return null
  return <details>...</details>
}
```

**问题**：
- 类型签名 `text?: string` 假设后端总是返回字符串或 undefined
- 运行时 `text` 可能是 `{}`，`if (!text)` 检查通过（空对象是 truthy），尝试渲染时崩溃

## 业界最佳实践修复方案

### 1. 后端：数据规范化（根本修复）

**原则**：在序列化层统一规范，空字符串不序列化，保持 API 契约一致性。

**修复代码**：`cmd/api/adapters.go`
```go
func (a *scanAdapter) AppendAssistant(ctx context.Context, convID, content, thinking string) ([]byte, error) {
	var meta json.RawMessage
	if thinking != "" {
		// 仅在 thinking 非空时序列化进 metadata
		m, err := json.Marshal(map[string]string{"thinking": thinking})
		if err != nil {
			chatLog.Warn().Err(err).Str("conv", convID).Msg("thinking 序列化失败，metadata 降级为 nil")
		} else {
			meta = m
		}
	}
	// thinking 为空或序列化失败时 meta=nil → metadata 列存 NULL → 前端 Metadata 为 null
	msg, err := a.conversations.AppendMessage(ctx, convID, conversation.RoleAssistant, conversation.KindMessage, content, meta)
	if err != nil {
		return nil, err
	}
	return json.Marshal(msg)
}
```

**关键改进**：
1. ✅ 空 `thinking` 不序列化，`metadata` 直接为 `nil`（数据库存 `NULL`）
2. ✅ 前端收到 `Metadata: null`（符合类型契约），而非 `{"thinking":""}`
3. ✅ 序列化失败时降级为 `nil` 并记录告警，不阻断消息落库
4. ✅ 保持数据库清洁，避免存储无意义的空字段

### 2. 前端：类型守卫（防御性编程）

**原则**：运行时类型检查，不信任后端数据，应对历史脏数据。

**修复代码**：`web/src/features/conversation/TimelineThread.tsx`

```typescript
function ThinkingFold({ text }: { text?: string | unknown }) {
  // 类型守卫：严格验证类型并过滤空值
  if (!text || typeof text !== 'string' || text.trim() === '') return null
  return (
    <details className="group mb-2 self-start rounded-lg border border-border/60 bg-background/40">
      <summary className="flex cursor-pointer select-none items-center gap-1.5 px-2.5 py-1.5 text-[11.5px] text-muted">
        <Brain className="h-3 w-3" />
        <span>内部思考</span>
        <ChevronDown className="h-3 w-3 transition-transform group-open:rotate-180" />
      </summary>
      <div className="max-h-56 overflow-y-auto px-3 pb-2.5 text-[12px] leading-relaxed text-muted/90 whitespace-pre-wrap">
        {text}
      </div>
    </details>
  )
}

function ThinkingStream({ text }: { text: string | unknown }) {
  // 类型守卫：流式 thinking 增量，严格验证类型并过滤空值
  if (!text || typeof text !== 'string' || text.trim() === '') return null
  const typed = useTypewriter(text)
  return (
    <div className="mb-2 rounded-lg border border-border/60 bg-background/40">
      <div className="flex items-center gap-1.5 px-2.5 py-1.5 text-[11.5px] text-muted">
        <Brain className="h-3 w-3" />
        <span>深度思考中…</span>
      </div>
      <div className="max-h-40 overflow-y-auto px-2.5 pb-2 text-[12px] leading-relaxed text-muted/90 whitespace-pre-wrap">
        {typed}
      </div>
    </div>
  )
}
```

**关键改进**：
1. ✅ 类型签名 `text?: string | unknown` 承认运行时可能收到任意类型
2. ✅ 三重检查：`!text`（falsy）+ `typeof text !== 'string'`（类型）+ `text.trim() === ''`（空字符串）
3. ✅ 应对历史脏数据：空对象 `{}`、空字符串 `""`、`null`、`undefined` 全部过滤
4. ✅ 防御性编程：即使后端未来再次引入类型异常，前端也不会崩溃

### 3. 数据库：清理历史脏数据

**迁移脚本**：`migrations/0112_clean_empty_thinking_metadata.sql`

```sql
-- 清理空字符串 thinking
UPDATE message
SET metadata = metadata - 'thinking'
WHERE kind = 'message'
  AND role = 'assistant'
  AND metadata IS NOT NULL
  AND metadata ? 'thinking'
  AND (metadata->>'thinking' = '' OR metadata->>'thinking' IS NULL);

-- 清理空对象 thinking（会导致前端崩溃）
UPDATE message
SET metadata = metadata - 'thinking'
WHERE kind = 'message'
  AND role = 'assistant'
  AND metadata IS NOT NULL
  AND metadata ? 'thinking'
  AND metadata->'thinking' = '{}'::jsonb;
```

**关键改进**：
1. ✅ 移除所有空 `thinking` 字段（空字符串和空对象）
2. ✅ 数据库保持清洁：只有有效内容才序列化
3. ✅ 幂等操作：可安全重复执行

## 验证

### 后端测试
```bash
cd /Users/Xlbula/workspace/programs/go/liusha
go test ./cmd/api/... -v
```

### 前端测试
```bash
cd /Users/Xlbula/workspace/programs/go/liusha/web
pnpm test --run
# ✅ Test Files  52 passed (52)
# ✅ Tests  474 passed (474)
```

### 数据库迁移
```bash
psql postgresql://liusha:liusha_dev@localhost:5432/liusha < migrations/0112_clean_empty_thinking_metadata.sql
```

### 端到端验证
1. 重启后端服务
2. 点击之前崩溃的对话
3. 确认不再报错，thinking 字段正常显示或隐藏

## 预防措施

### 1. API 契约测试

在 `cmd/api/adapters_test.go` 中增加测试：

```go
func TestAppendAssistant_EmptyThinking(t *testing.T) {
	// 验证空 thinking 不序列化进 metadata
	payload, err := adapter.AppendAssistant(ctx, convID, "回答内容", "")
	require.NoError(t, err)
	
	var msg conversation.Message
	require.NoError(t, json.Unmarshal(payload, &msg))
	
	// metadata 应为 nil 或不含 thinking 字段
	if msg.Metadata != nil {
		var meta map[string]any
		require.NoError(t, json.Unmarshal(msg.Metadata, &meta))
		_, hasThinking := meta["thinking"]
		assert.False(t, hasThinking, "空 thinking 不应序列化进 metadata")
	}
}
```

### 2. 前端类型守卫模式

在所有渲染外部数据的组件中应用类型守卫模式：

```typescript
// ❌ 不安全：假设后端类型总是正确
function Component({ data }: { data?: string }) {
  if (!data) return null
  return <div>{data}</div>
}

// ✅ 安全：运行时验证类型
function Component({ data }: { data?: string | unknown }) {
  if (!data || typeof data !== 'string') return null
  return <div>{data}</div>
}
```

### 3. 数据库约束

考虑添加 CHECK 约束（可选，需权衡性能）：

```sql
ALTER TABLE message ADD CONSTRAINT check_thinking_type
CHECK (
  metadata IS NULL 
  OR NOT (metadata ? 'thinking')
  OR jsonb_typeof(metadata->'thinking') = 'string'
);
```

## 相关文件

- `cmd/api/adapters.go` - 后端序列化修复
- `web/src/features/conversation/TimelineThread.tsx` - 前端类型守卫
- `migrations/0112_clean_empty_thinking_metadata.sql` - 数据清理
- `internal/chat/chat.go` - 调用方（chat 模块）
- `internal/qa/answer.go` - 调用方（qa 模块）

## 教训总结

1. **类型契约必须在序列化层强制执行**：不能假设调用方传入的参数总是符合预期
2. **空值处理要统一**：空字符串应省略字段，而非序列化空值
3. **前端不信任后端数据**：运行时类型检查是防御性编程的必要手段
4. **数据迁移是技术债清理的必经之路**：历史脏数据必须主动清理
5. **错误降级不能静默**：序列化失败应记录告警，便于事后排查

## 参考

- [JSON API 最佳实践](https://jsonapi.org/recommendations/)
- [TypeScript Runtime Validation](https://www.typescriptlang.org/docs/handbook/2/narrowing.html)
- [PostgreSQL JSONB Operations](https://www.postgresql.org/docs/current/functions-json.html)
