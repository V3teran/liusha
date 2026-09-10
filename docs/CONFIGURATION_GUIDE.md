# Liusha 配置管理指南

## 目录

- [配置架构](#配置架构)
- [Agent 配置](#agent-配置)
- [配置方式](#配置方式)
- [三级缓存机制](#三级缓存机制)
- [最佳实践](#最佳实践)

---

## 配置架构

### 目录结构

```
internal/config/
├── agent/          # Agent 配置（Planner/Executor/Evaluator）
│   ├── model.go    # 数据模型
│   └── store.go    # CRUD 操作
│
├── llm/            # LLM 配置（Provider/API Key）
│   ├── model.go
│   ├── store.go
│   └── apikey.go
│
├── tool/           # 工具配置
│   ├── model.go
│   └── store.go
│
├── setting/        # 系统设置
│   ├── model.go
│   └── store.go
│
├── cache/          # 三级缓存层（内存→Redis→PostgreSQL）
│   ├── store.go
│   ├── adapter.go
│   └── README.md
│
└── seed/           # 种子初始化
    ├── seed.go     # 加载逻辑
    ├── agents/     # Agent 种子文件
    │   ├── planner.md
    │   ├── executor.md
    │   └── evaluator.md
    ├── system.go
    └── llm.go
```

---

## Agent 配置

### 三个 Agent

Liusha ADK 包含三个固定的 Agent：

#### 1. **Planner Agent** - 规划者
- **职责**：规划和分解任务
- **输入**：Objective（目标）
- **输出**：Action 序列 + Roadmap

#### 2. **Executor Agent** - 执行者
- **职责**：执行 Action，产生 Observation
- **输入**：Action
- **输出**：Observation（原始执行结果）

#### 3. **Evaluator Agent** - 评估者
- **职责**：评估 Observation，产生 Result
- **输入**：Observation
- **输出**：Evaluation + Result（已验证）

### Agent 配置字段

```go
type Agent struct {
    ID            string      // UUID
    Code          string      // 'planner', 'executor', 'evaluator'
    Kind          Kind        // Agent 类型
    Name          string      // 显示名称
    Description   string      // 描述
    SystemPrompt  string      // System Prompt（核心配置）
    FunctionTools []string    // 可用的函数工具
    CliTools      []string    // 可用的 CLI 工具
    MaxIterations int         // 最大迭代次数
    Complexity    string      // 复杂度档位
    Enabled       bool        // 是否启用
}
```

---

## 配置方式

### 方式 1：前端编辑（推荐）

#### 列出所有 Agent
```http
GET /api/agents

Response:
{
  "agents": [
    {
      "code": "planner",
      "name": "Planner Agent",
      "system_prompt": "...",
      "max_iterations": 30,
      ...
    },
    {
      "code": "executor",
      ...
    },
    {
      "code": "evaluator",
      ...
    }
  ]
}
```

#### 获取单个 Agent
```http
GET /api/agents/planner

Response:
{
  "agent": {
    "id": "uuid",
    "code": "planner",
    "name": "Planner Agent",
    "system_prompt": "你是 Planner Agent...",
    "function_tools": ["propose_actions", "update_roadmap"],
    "cli_tools": [],
    "max_iterations": 30,
    "complexity": "standard"
  }
}
```

#### 更新 Agent 配置
```http
PUT /api/agents/planner

Request:
{
  "system_prompt": "新的 system prompt...",
  "max_iterations": 50
}

Response:
{
  "agent": { ... }  // 更新后的配置
}
```

#### 重置为默认配置
```http
POST /api/agents/planner/reset

Response:
{
  "ok": true,
  "message": "已重置为默认配置"
}
```

---

### 方式 2：种子文件（开发环境）

#### 种子文件格式

```markdown
# internal/config/seed/agents/planner.md
---
id: planner
kind: planner
name: Planner Agent
description: 规划和分解任务
function_tools:
  - propose_actions
  - update_roadmap
  - read_knowledge_graph
cli_tools: []
max_iterations: 30
tier: standard
---

你是 Planner Agent，Liusha ADK 系统中的规划者。

## 角色定位
...（完整的 system prompt）
```

#### 加载种子文件

```bash
# 首次启动时自动加载
make run-api

# 或手动运行
go run cmd/migrate/main.go seed
```

**注意：** 种子加载是 **insert-only** 语义：
- ✅ 只填充空库
- ✅ 已存在的记录不会被覆盖
- ✅ 保护前端/运维在数据库中的修改

---

### 方式 3：数据库直接修改（不推荐）

```sql
-- 查看所有 Agent
SELECT code, name, enabled FROM agent;

-- 更新 System Prompt
UPDATE agent 
SET system_prompt = '新的 prompt...' 
WHERE code = 'planner';

-- ⚠️ 注意：直接修改数据库后需要手动清除缓存
-- 推荐使用 API 方式，会自动失效缓存
```

---

## 三级缓存机制

### 架构

```
写入流程：
1. 更新 PostgreSQL
2. Redis 广播失效消息
3. 各进程清除内存缓存

读取流程：
1. 查内存 L1 → 命中返回
2. 查 Redis L2 → 命中回填 L1
3. 查 PostgreSQL → 回填 L2 + L1
```

### 为什么需要缓存？

**场景：**
- API 进程处理前端请求，修改配置
- Runner 进程执行任务，读取配置

**问题：**
- 如果 Runner 的本地缓存不失效
- 会使用旧配置装配 Agent
- 导致行为不符合预期

**解决：**
- API 修改配置后，通过 Redis Pub/Sub 广播失效
- Runner 收到消息后清除本地缓存
- 下次读取时重新加载最新配置

### 失效示例

```go
// API 进程
store.UpdateAgent(ctx, id, updates)
// → 写 PostgreSQL
// → Redis PUBLISH "cache:invalidate"

// Runner 进程
// → 收到消息
// → 清除 L1 + L2
// → 下次读取最新配置
```

详细文档：[Cache README](../internal/cache/README.md)

---

## 最佳实践

### ✅ 推荐做法

#### 1. 通过 API 修改配置
```bash
curl -X PUT http://localhost:8080/api/agents/planner \
  -H "Content-Type: application/json" \
  -d '{
    "system_prompt": "新的 prompt..."
  }'
```

**优点：**
- ✅ 自动失效缓存
- ✅ 多进程同步
- ✅ 有审计日志

---

#### 2. 重要配置保留历史

```sql
-- agent_config_history 表自动记录变更
SELECT 
  changed_by,
  change_type,
  old_config,
  new_config,
  created_at
FROM agent_config_history
WHERE agent_id = 'planner-id'
ORDER BY created_at DESC;
```

---

#### 3. 定期备份种子文件

```bash
# 导出当前配置为种子文件
./scripts/export-agent-config.sh planner > internal/config/seed/agents/planner.md

# 提交到版本控制
git add internal/config/seed/agents/
git commit -m "backup: 更新 Agent 配置种子"
```

---

#### 4. 测试配置变更

```bash
# 1. 在测试环境修改
curl -X PUT http://localhost:8080/api/agents/planner -d '{...}'

# 2. 验证行为
curl -X POST http://localhost:8080/api/assignments -d '{...}'

# 3. 检查日志
tail -f logs/app.log | grep "planner"

# 4. 确认无误后，在生产环境应用
```

---

### ❌ 避免的做法

#### 1. 不要直接修改数据库

```sql
-- ❌ 错误：缓存不会失效
UPDATE agent SET system_prompt = '...' WHERE code = 'planner';

-- ✅ 正确：使用 API
curl -X PUT http://localhost:8080/api/agents/planner -d '{...}'
```

---

#### 2. 不要绕过 cache 包

```go
// ❌ 错误：缓存不会失效
cfgagent.Store.Update(ctx, id, updates)

// ✅ 正确：使用 cache
cache.UpdateAgent(ctx, id, updates)
```

---

#### 3. 不要频繁修改配置

```bash
# ❌ 错误：每分钟修改一次
# 会导致频繁失效缓存，影响性能

# ✅ 正确：批量修改，一次完成
```

---

## 配置示例

### 修改 Planner 的 MaxIterations

```bash
curl -X PUT http://localhost:8080/api/agents/planner \
  -H "Content-Type: application/json" \
  -d '{
    "max_iterations": 50
  }'
```

### 修改 Executor 的 System Prompt

```bash
curl -X PUT http://localhost:8080/api/agents/executor \
  -H "Content-Type: application/json" \
  -d '{
    "system_prompt": "你是 Executor Agent，负责执行 Action...\n\n新增：特别注意安全性..."
  }'
```

### 重置 Evaluator 为默认配置

```bash
curl -X POST http://localhost:8080/api/agents/evaluator/reset
```

---

## 故障排查

### 问题 1：配置修改后未生效

**症状：** 修改了配置，但 Agent 行为未变化

**可能原因：**
1. 缓存未失效
2. Redis 连接断开
3. 修改了错误的 Agent

**排查步骤：**
```bash
# 1. 确认修改成功
curl http://localhost:8080/api/agents/planner | jq .agent.system_prompt

# 2. 检查 Redis 连接
redis-cli PING

# 3. 检查缓存订阅
redis-cli PUBSUB CHANNELS "cache:*"

# 4. 手动清除缓存（临时方案）
redis-cli DEL "cache:executor:id:xxx"

# 5. 重启进程（最后手段）
systemctl restart liusha-runner
```

---

### 问题 2：种子文件未加载

**症状：** 新增的 evaluator.md 未生效

**可能原因：**
- 数据库中已存在 evaluator 记录
- 种子加载是 insert-only（不覆盖已存在的）

**解决方案：**
```sql
-- 删除现有记录
DELETE FROM agent WHERE code = 'evaluator';

-- 重启应用，自动重新加载种子
```

---

### 问题 3：不同进程配置不一致

**症状：** API 进程和 Runner 进程看到的配置不同

**可能原因：**
- 失效广播未送达

**解决方案：**
```bash
# 重启所有进程
systemctl restart liusha-api
systemctl restart liusha-runner
```

---

## 监控建议

### 关键指标

1. **缓存命中率**
   - L1 命中率 > 90%
   - L2 命中率 > 95%

2. **配置变更频率**
   - 正常：< 10次/天
   - 异常：> 100次/天

3. **失效延迟**
   - 正常：< 100ms
   - 异常：> 1s

### 日志监控

```bash
# 配置变更日志
grep "UpdateAgent" logs/app.log

# 缓存失效日志
grep "cache:invalidate" logs/app.log

# 种子加载日志
grep "seed" logs/app.log
```

---

## 相关文档

- [Cache README](../internal/cache/README.md) - 三级缓存详细说明
- [Architecture](ARCHITECTURE_FINAL_REVIEW_COMPLETE.md) - 整体架构
- [HTTP API](../internal/httpapi/README.md) - API 文档
