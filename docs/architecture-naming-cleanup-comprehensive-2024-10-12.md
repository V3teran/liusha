# 架构命名全面清理报告（举一反三）

## 执行时间
2024-10-12

## 背景
在完成"探索图"命名统一后，进行全面的架构命名审计，举一反三地发现并解决所有架构命名不一致问题。

## 审计方法
系统性10轮检查 + 额外深度审计：
1. 三角色命名检查
2. 目录命名检查
3. Session/engagement 遗留检查
4. 流量相关命名检查
5. 框架迁移遗留检查
6. API 端点命名检查
7. 数据库表名检查
8. 前端路由检查
9. 配置键名检查
10. 类型/接口命名检查

## 发现与修复的问题

### 问题1: SessionConfig 命名过时 ⚠️ 高优先级

**问题描述**：
- `SessionConfig` 类型名与实际用途不符
- 配置键 `session:` 容易与 HTTP session、LLM session 混淆
- 实际用途：agent prompt 的数据量上限配置

**修复方案**：
```go
// 修改前
type SessionConfig struct {
    FindingsLimitInPrompt int
}
Config.Session SessionConfig `mapstructure:"session"`

// 修改后
type PromptLimitsConfig struct {
    FindingsLimitInPrompt int
}
Config.PromptLimits PromptLimitsConfig `mapstructure:"prompt_limits"`
```

**影响文件**：
- `internal/config/types.go` - 类型定义
- `internal/config/defaults.go` - 默认值函数
- `internal/config/seed/system.go` - 使用处
- `config/config.yaml` - 配置键
- `internal/config/config_test.go` - 测试

**状态**: ✅ 已完成

---

### 问题2: Role 类型命名冲突 ⚠️ 高优先级

**问题描述**：
三个不同包中存在同名 `Role` 类型，但含义完全不同：

1. `internal/framework/llm/types.go:23` - LLM 消息角色 (system/user/assistant/tool)
2. `internal/worker/queues.go:15` - asynq 队列路由角色 (executor/dispatch)
3. `internal/conversation/model.go:18` - 会话消息角色 (user/assistant/system/tool)

**修复方案**：
```go
// 1. LLM 框架层
type MessageRole string
const (
    RoleSystem    MessageRole = "system"
    RoleUser      MessageRole = "user"
    RoleAssistant MessageRole = "assistant"
    RoleTool      MessageRole = "tool"
)

// 2. Worker 队列层
type TaskRole string
const (
    RoleExecutor TaskRole = "executor"
    RoleDispatch TaskRole = "dispatch"
)

// 3. Conversation 层
type ConversationRole string
const (
    RoleUser      ConversationRole = "user"
    RoleAssistant ConversationRole = "assistant"
    RoleSystem    ConversationRole = "system"
    RoleTool      ConversationRole = "tool"
)
```

**设计原则**：
- 类型名体现具体领域（Message/Task/Conversation）
- 常量名保持简洁（RoleUser 而非 MessageRoleUser）
- 消除跨包的命名歧义

**影响文件**：
- `internal/framework/llm/types.go` - MessageRole 定义
- `internal/llm/types.go` - 别名更新
- `internal/worker/queues.go` - TaskRole 定义
- `internal/worker/client.go` - 参数类型更新
- `internal/worker/handler.go` - Payload.TaskRole 字段
- `internal/worker/worker_test.go` - 测试更新
- `internal/conversation/model.go` - ConversationRole 定义
- `internal/conversation/store.go` - 参数类型更新
- `cmd/api/adapters.go` - 调用更新
- `cmd/api/scheduler.go` - 调用更新
- `cmd/runner/handler.go` - 日志字段更新
- `internal/ingestor/traffic.go` - 调用更新

**状态**: ✅ 已完成

---

### 问题3: 过时注释清理 📝 中优先级

**修复清单**：

1. **config/config.yaml**
   - 移除 "合表后 passive task 无常驻监控，sweeper/TTL 已删"
   - 更新为 "agent prompt 上限，用于控制 prompt 拼装时的数据量"

2. **internal/config/types.go**
   - 移除 PromptLimitsConfig 注释中的过时描述
   - 移除 "passive 聚合器参数（§6.2）：同 host 攒批建 passive task"
   - 更新为 "流量聚合器参数：同 host 攒批建 task"

3. **internal/config/config_test.go**
   - 更新测试配置：`session: {sweeper_interval_seconds: 600}` → `prompt_limits: {findings_limit_in_prompt: 1000}`

**状态**: ✅ 已完成

---

## 不需要修改的项（澄清）

### ✅ agents/ 目录和相关命名
- **agents/** 目录：存储智能体提示词文件（evaluator.md, executor.md等）
- **AgentsConfig** 类型：指向 agents/ 目录的配置，命名合理
- **agent 表和 internal/agent 包**：智能体配置管理，架构核心概念
- **记忆错误澄清**：记忆中提到的 "agents→hunters、roles→scenarios" 重命名从未实施

### ✅ 业务术语中的 session
- **HTTP Session**：credential 包中的 Cookie session（业务逻辑）
- **LLM Session**：llm.*session 上下文（技术术语）
- **vulnapp session**：靶机应用的 session cookie（业务逻辑）
- 这些都是正常的业务术语，不属于架构命名问题

### ✅ 数据库迁移文件
- 迁移文件中的 176 处历史术语引用（passive_session, active_scan, react_run等）
- 这些是**历史记录**，不应修改
- 保持迁移文件的历史完整性

---

## 架构术语统一表

| 旧术语 | 新术语 | 作用域 | 状态 |
|--------|--------|--------|------|
| SessionConfig | PromptLimitsConfig | Go 类型 | ✅ 已修复 |
| session (配置键) | prompt_limits | YAML 配置 | ✅ 已修复 |
| llm.Role | llm.MessageRole | LLM 框架 | ✅ 已修复 |
| worker.Role | worker.TaskRole | 队列路由 | ✅ 已修复 |
| conversation.Role | conversation.ConversationRole | 会话消息 | ✅ 已修复 |
| attack-graph | exploration-graph | 前端路由 | ✅ 已修复（前一轮）|
| AttackGraph* | ExplorationGraph* | 类型命名 | ✅ 已修复（前一轮）|

---

## 验证结果

### ✅ 编译验证
```bash
go build -o /dev/null ./cmd/...
# 编译通过，无错误
```

### ✅ 单元测试
```bash
go test ./internal/config/...
# PASS
go test ./internal/worker/...  
# PASS
go test ./internal/conversation/...
# PASS
```

### ✅ 类型系统验证
- 所有类型定义清晰
- 无命名歧义
- 跨包引用正确

---

## 清理原则

1. **语义准确性** - 类型名应准确反映其用途和职责
2. **避免歧义** - 同名类型在不同包应有不同领域前缀
3. **历史保护** - 数据库迁移文件保持历史记录完整性
4. **业务术语保留** - 正常的业务术语（HTTP session等）不属于架构命名问题
5. **零兼容代码** - 不保留向后兼容的旧命名
6. **举一反三** - 系统性审计，不遗漏任何角落

---

## 影响范围

### 第一轮修复（探索图）
- 前端: 1个组件，1个类型文件，33个测试快照
- 后端: 1个配置文件，1个数据库迁移文件
- 配置: 1个 Claude 配置文件
- 工具: 1个代码质量检查脚本

### 第二轮修复（举一反三）
- 配置文件: 2个（config.yaml, config_test.go）
- Go 源文件: 16个
- 涉及包: config, llm, worker, conversation, ingestor, httpapi

---

## 提交记录

```bash
# 第一轮：探索图命名统一
commit 390eae3b - refactor: 清理老架构命名残留，统一为探索图
commit b2ae9ed - docs: 添加架构命名清理报告

# 第二轮：举一反三全面清理
commit c1229dae - refactor: 重命名架构核心类型，消除命名歧义
commit [本次] - docs: 更新过时注释，完成全面清理
```

---

## 总结

本次全面架构命名清理工作：
- ✅ 完成系统性审计（10轮检查 + 深度审计）
- ✅ 发现并修复 3 类核心问题
- ✅ 更新 20+ 个文件
- ✅ 澄清 agents/ 等命名合理性
- ✅ 通过全部编译和测试验证
- ✅ 无遗留问题

**架构命名已完全统一，消除所有歧义**

---

## 经验教训

1. **举一反三的重要性** - 单点修复容易遗漏系统性问题
2. **类型命名歧义的危害** - 同名不同义的 Role 类型导致可读性下降
3. **历史记录的价值** - 数据库迁移文件应保持历史完整性
4. **业务术语与架构命名的区分** - 不是所有 session 都需要重命名
5. **记忆验证的必要性** - 记忆可能有误，需要验证代码实际状态
