# Provider 接口统一分析

## 两个 Provider 定义的对比

### 1. credential/provider.go - Provider（凭证存储）
**职责**：活凭证存储抽象
```go
type Provider interface {
    BatchSave(ctx context.Context, byHost map[string][]Identity, ttlSeconds int) error
    GetIdentitiesByHost(ctx context.Context, host string) ([]Identity, error)
    List(ctx context.Context, host string) (map[string][]Identity, error)
    Delete(ctx context.Context, host string) error
}
```
**使用场景**：
- 存储和检索目标主机的身份信息（凭证）
- 按 host 分组管理
- TTL 支持过期策略
- Redis/内存实现替换

**特点**：
- 防御性处理 anonymous（不持久化）
- 业务层依赖此接口
- 便于单测 mock/fake 替换

**数据对象**：`Identity`（主机身份/凭证）

---

### 2. framework/llm/provider.go - Provider（LLM 调用）
**职责**：LLM 提供者抽象
```go
type Provider interface {
    Complete(ctx context.Context, req Request) (Response, error)
    Stream(ctx context.Context, req Request) (<-chan StreamEvent, error)
    CountTokens(ctx context.Context, req Request) (int, error)
    ModelID() string
    ProviderID() string
}
```
**使用场景**：
- 屏蔽 LLM 协议差异（Anthropic/OpenAI/等）
- 支持流式和非流式调用
- 精确 token 计数
- 并发安全

**实现**：
- `anthropic.go`：Anthropic API
- `openai.go`：OpenAI 兼容层
- Router：多提供者路由选择

**特点**：
- 并发安全
- 支持流式输出（<-chan StreamEvent）
- 精确 token 计数（不是估算）

**数据对象**：
- `Request`：消息 + 工具 + 参数
- `Response`：文本 + 工具调用 + 使用量
- `StreamEvent`：流式事件

---

## 结论

**两个 Provider 接口不应统一**

| 方面 | credential/Provider | framework/llm/Provider |
|------|---|---|
| **职责** | 凭证存储管理 | LLM 协议屏蔽 |
| **操作** | 保存/检索/删除凭证 | 调用/流式/计数 |
| **数据对象** | Identity（凭证） | Request/Response/StreamEvent |
| **层级** | 业务层 | 框架层 |
| **并发** | 单实例私有 | 多 goroutine 并发安全 |
| **实现** | Redis/内存 | Anthropic/OpenAI |
| **接口位置** | 消费方（业务层） | 消费方（executor/dispatcher） |

### 设计正当性

1. **credential/Provider**
   - 凭证存储专用
   - 与 LLM 无关
   - 支持多种后端实现

2. **framework/llm/Provider**
   - LLM 协议统一入口
   - 支持多个 LLM 提供者
   - 框架级抽象

两者名字虽然都叫 "Provider"，但语义完全不同，属于不同层级的设计模式：
- **credential/Provider** = 存储访问抽象（Repository 模式）
- **framework/llm/Provider** = 协议适配器（Adapter 模式）

---

## 综合总结：类型统一工作完成

### ✅ 已统一和验证（10项）

#### 已统一：
1. **Message 协议层** - 6处合并为 4处（internal/llm 为标准）
2. **AgentConfig.Type** - 补全缺失字段
3. **EventBus 接口** - 提取 EventPublisher/EventSubscriber
4. **Event 结构** - 补全 ID 字段，统一接口签名

#### 已验证（合理重复）：
5. **RetryConfig (3处)** - 框架层/运行时层/应用配置层，语义完全不同
6. **Context (3处)** - TaskContext/SpanContext/ExecutionContext，职责不同
7. **Event 类型 (3处)** - 框架事件/审计事件/规划驱动事件，职责不同
8. **Store 接口 (7个)** - 分层职责清晰，各有专属用途
9. **Provider 接口 (2个)** - 凭证存储 vs LLM 协议，完全不同领域

### ❌ 不应统一（合理分离）

1. **业务 Agent 配置** - 不同业务需求差异大
2. **LLM Provider 配置** - 协议特定参数
3. **各层独立接口** - 分层设计原则

### 最佳实践总结

**1. 接口定义在消费方**
- middleware 定义 HumanInputStore
- executor 定义 PlannerEventBus
- credential 定义 Provider
- llm 定义 Provider

**2. 使用类型别名统一标准**
```go
// framework/llm/provider.go
type Message = corellm.Message
type ToolCall = corellm.ToolCall
```

**3. 分层职责清晰**
- 框架层：EventBus、GraphStore、Store
- Middleware 层：HumanInputStore、StreamAdapter
- LLM 层：Provider、Router
- RAG 层：VectorStore
- 业务层：各种业务特定接口

**4. 避免过度统一**
- 不强行合并语义不同的类型
- 不违反依赖倒置原则
- 不跨层共享接口

### 下一步

类型统一工作已完成。建议：
1. ✅ 验证现有代码是否遵循这些原则
2. ✅ 添加架构决策记录（ADR）文档
3. ✅ 在新开发时应用这些指导原则
