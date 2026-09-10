# 现有配置系统深度分析与优化建议

## 🎯 分析目标

1. 检查是否重复造轮子
2. 评估现有系统是否适用新架构
3. 提出目录结构优化建议

---

## 📊 现有系统全景

### **已存在的配置系统（非常完善！）**

```
internal/config/
├── agent/                    # Agent 配置持久化 ✅
│   ├── model.go             # Agent 数据模型
│   ├── store.go             # CRUD 操作
│   └── store_integration_test.go
│
├── configstore/             # 三级缓存层 ✅✅✅
│   ├── store.go            # 内存 → Redis → PostgreSQL
│   └── adapter.go          # 缓存适配器
│
├── seed/                    # 种子初始化 ✅
│   ├── seed.go             # 从磁盘加载 *.md
│   ├── system.go
│   └── llm.go
│
├── settingstore/           # 系统设置存储 ✅
│   ├── model.go
│   ├── store.go
│   └── store_test.go
│
├── llmcfg/                 # LLM 配置 ✅
│   ├── model.go
│   ├── store.go
│   └── apikey.go
│
├── tool/                   # 工具配置 ✅
│   ├── model.go
│   ├── store.go
│   └── reconcile.go
│
├── config.go               # 顶层配置（Viper） ✅
└── config_test.go

internal/httpapi/
└── config_handler.go       # Agent CRUD API ✅

db/migrations/
└── 0126_simplify_agent_architecture.up.sql  # 已有表结构 ✅
```

---

## ✅ 发现 1：已有完善的三级缓存系统！

### **configstore 包（核心发现）**

```go
// internal/configstore/store.go
// 多级缓存读写层：内存 L1 → Redis L2 → DB

// 缓存机制：
// 1. 写路径：写 DB + Redis 广播失效
// 2. 读路径：L1 命中 → L2 命中 → DB 读取 + 回填
// 3. 失效总线：Redis pubsub，各进程订阅
```

**架构特点：**
- ✅ **完全符合你的需求**：内存 → Redis → PostgreSQL
- ✅ **多进程支持**：api 改配置，runner 自动失效 L1
- ✅ **已经实现**：不需要重新设计

**关键代码：**
```go
// 写入时自动失效
func (s *Store) UpdateExecutor(ctx context.Context, id string, p cfgagent.UpdateParams) {
    // 1. 更新数据库
    h, err := s.executors.Update(ctx, id, p)
    
    // 2. 失效缓存（L1 + L2）
    s.cache.Invalidate(ctx, agentKeys(h.ID, h.Code)...)
    
    return h, nil
}

// 读取时走缓存
func (s *Store) GetExecutorByCode(ctx context.Context, code string) {
    // 尝试缓存
    key := fmt.Sprintf("agent:code:%s", code)
    
    // L1 → L2 → DB
    return s.cache.GetOrLoad(ctx, key, func() { 
        return s.executors.GetByCode(ctx, code) 
    })
}
```

---

## ✅ 发现 2：已有 Agent 配置表！

### **数据库表（0126 migration）**

```sql
-- agent 表已存在
CREATE TABLE agent (
    id              UUID PRIMARY KEY,
    code            VARCHAR(100) UNIQUE NOT NULL,  -- 'planner', 'executor'
    kind            VARCHAR(50) NOT NULL,          -- 'planner', 'executor'
    name            VARCHAR(100),
    description     TEXT,
    system_prompt   TEXT,                          -- 重命名自 body
    function_tools  JSONB,
    cli_tools       JSONB,
    skills          JSONB,
    max_iterations  INTEGER DEFAULT 40,
    complexity      VARCHAR(20) DEFAULT 'medium',
    is_builtin      BOOLEAN DEFAULT false,
    enabled         BOOLEAN DEFAULT true,
    created_at      TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ
);

-- 唯一索引：每种 kind 只能有 1 个 enabled
CREATE UNIQUE INDEX idx_agent_kind_enabled
    ON agent (kind) WHERE enabled = true;
```

**问题：**
- ⚠️ **只有 2 个 Agent**：planner, executor
- ⚠️ **缺少 Evaluator**：新架构需要第 3 个

---

## ✅ 发现 3：已有种子加载系统！

### **seed 包**

```go
// internal/config/seed/seed.go

// 从磁盘加载 agents/*.md
// Frontmatter: id, kind, function_tools, cli_tools, max_iterations, tier
// Body: system_prompt

// insert-only 语义：
// - 只填空库
// - 已存在的跳过（不覆盖前端改动）
```

**种子文件格式：**
```markdown
---
id: planner
kind: planner
function_tools: []
cli_tools: []
max_iterations: 100
tier: medium
---

你是渗透测试规划者。

职责：
- 评估世界模型...
```

---

## ✅ 发现 4：已有 HTTP API！

### **config_handler.go**

```go
// GET /api/agents/executors - 列出所有 Executor
// GET /api/agents/executors/:id - 获取单个
// PUT /api/agents/executors/:id - 更新配置
// PUT /api/agents/executors/:id/complexity - 更新复杂度
```

**问题：**
- ⚠️ **只有 Executor API**：缺少 Planner 的 CRUD
- ⚠️ **缺少 Evaluator API**

---

## 🔍 现有系统与新架构的差异

### **架构对比**

| 维度 | 现有系统 | 新架构需求 | 差异 |
|------|---------|-----------|------|
| **Agent 数量** | 2 个（Planner + Executor） | 3 个（Planner + Executor + Evaluator） | ⚠️ 缺 Evaluator |
| **三级缓存** | ✅ 已实现（内存 → Redis → PostgreSQL） | ✅ 需要 | ✅ 完美匹配 |
| **种子加载** | ✅ 已实现（*.md frontmatter） | ✅ 需要 | ✅ 可复用 |
| **前端编辑** | ✅ 已实现（HTTP API） | ✅ 需要 | ✅ 可复用 |
| **数据库表** | ✅ agent 表已存在 | ✅ 需要 | ✅ 可复用 |
| **Agent 字段** | code, kind, name, system_prompt, tools... | 同左 | ✅ 完全匹配 |
| **种子格式** | Markdown (*.md) | YAML (*.yaml) | ⚠️ 格式不同 |

---

## 🎯 问题 1：目录结构是否需要调整？

### **你的需求：**
> 不光是 agents，还有系统配置，以及将来可能的新概念需要三级缓存机制

### **现有目录已支持多种配置：**

```
internal/config/
├── agent/          # Agent 配置 ✅
├── llmcfg/         # LLM 配置 ✅
├── tool/           # 工具配置 ✅
├── settingstore/   # 系统设置 ✅
└── seed/           # 统一种子加载 ✅

configstore/        # 通用三级缓存层 ✅
```

**评估：目录结构已经很好！**

- ✅ **分类清晰**：agent, llmcfg, tool, settingstore
- ✅ **可扩展**：新增概念只需新建子目录（如 `internal/config/workflow/`）
- ✅ **统一缓存**：configstore 是通用层，所有配置都能用
- ✅ **统一种子**：seed 包统一加载所有种子

---

## 🎯 问题 2：是否重复造轮子？

### **我的设计 vs 现有系统**

| 我的设计 | 现有系统 | 结论 |
|---------|---------|------|
| **PostgreSQL Schema** | ✅ agent 表已存在 | ❌ 重复 |
| **三级缓存（内存→Redis→PG）** | ✅ configstore 已实现 | ❌ 重复 |
| **种子加载器** | ✅ seed 包已实现 | ❌ 重复 |
| **HTTP CRUD API** | ✅ config_handler 已实现 | ❌ 重复 |
| **前端编辑** | ✅ 前端已有配置页 | ❌ 重复 |
| **配置服务** | ✅ configstore.Store 已实现 | ❌ 重复 |

**结论：我的方案 90% 都是重复的！** ❌

---

## ✅ 真正需要的工作

### **1. 添加 Evaluator Agent**

#### **1.1 更新数据库（新 migration）**

```sql
-- db/migrations/0132_add_evaluator_agent.up.sql

-- 插入 Evaluator
INSERT INTO agent (
    code, kind, name, description, system_prompt,
    function_tools, cli_tools, skills,
    max_iterations, complexity, is_builtin, enabled
) VALUES (
    'evaluator',
    'evaluator',
    'Evaluator Agent',
    '评估和验证 Observation，产生 Result',
    '你是 Evaluator Agent...（完整 prompt）',
    '["write_evaluation", "write_result", "read_knowledge_graph"]'::jsonb,
    '[]'::jsonb,
    '["evidence_verification", "claim_analysis"]'::jsonb,
    30,
    'standard',
    true,
    true
);

-- 更新 kind 枚举（如果需要）
-- ALTER TYPE agent_kind ADD VALUE IF NOT EXISTS 'evaluator';
```

#### **1.2 更新 Go 模型**

```go
// internal/config/agent/model.go

const (
    KindPlanner   Kind = "planner"
    KindExecutor  Kind = "executor"
    KindEvaluator Kind = "evaluator"  // 新增
)
```

#### **1.3 添加种子文件**

```bash
# 创建 agents/evaluator.md
cat > agents/evaluator.md << 'EOF'
---
id: evaluator
kind: evaluator
function_tools:
  - write_evaluation
  - write_result
  - read_knowledge_graph
cli_tools: []
max_iterations: 30
tier: standard
---

你是 Evaluator Agent...
（完整 system prompt）
EOF
```

---

### **2. 扩展 HTTP API**

#### **当前缺失：**
- ❌ GET /api/agents/planner
- ❌ PUT /api/agents/planner
- ❌ GET /api/agents/evaluator
- ❌ PUT /api/agents/evaluator

#### **需要添加：**

```go
// internal/httpapi/config_handler.go

// 通用 Agent API（支持所有类型）
func RegisterAgentRoutes(r *gin.RouterGroup, api ConfigAPI) {
    // 列出所有 Agent（包括 Planner/Executor/Evaluator）
    r.GET("/agents", listAllAgents(api))
    
    // 单个 Agent CRUD
    r.GET("/agents/:code", getAgentByCode(api))      // code: planner/executor/evaluator
    r.PUT("/agents/:code", updateAgent(api))
    r.POST("/agents/:code/reset", resetAgent(api))   // 重置为默认
}
```

---

### **3. 统一种子文件格式（可选）**

#### **当前：Markdown + Frontmatter**
```markdown
---
id: planner
kind: planner
---
System prompt body...
```

#### **建议：保持 Markdown 格式**
- ✅ 现有系统已用 Markdown
- ✅ Frontmatter 支持 YAML 语法
- ✅ Body 是 System Prompt（长文本，Markdown 更适合）
- ✅ 不需要改动现有代码

**如果坚持 YAML：**
```yaml
# 需要修改 seed.go 解析逻辑
id: planner
kind: planner
system_prompt: |
  多行文本...
```

---

## 📋 优化建议

### **建议 1：统一 Agent API**

**当前：**
```go
// 只有 Executor 的 API
GET  /api/agents/executors
PUT  /api/agents/executors/:id
```

**优化为：**
```go
// 通用 Agent API（支持所有类型）
GET  /api/agents                    # 列出所有（Planner + Executor + Evaluator）
GET  /api/agents/:code              # 按 code 获取（planner/executor/evaluator）
PUT  /api/agents/:code              # 更新
POST /api/agents/:code/reset        # 重置为默认
GET  /api/agents/:code/history      # 配置历史（可选）
```

---

### **建议 2：扩展 configstore 的缓存键**

**当前：**
```go
// 只缓存 Executor
func agentKeys(id, code string) []string {
    return []string{
        fmt.Sprintf("agent:id:%s", id),
        fmt.Sprintf("agent:code:%s", code),
    }
}
```

**优化为：**
```go
// 支持所有 Agent 类型
func agentKeys(id, code string) []string {
    return []string{
        fmt.Sprintf("agent:id:%s", id),
        fmt.Sprintf("agent:code:%s", code),
        fmt.Sprintf("agent:all"),  // 列表缓存键
    }
}

// 新增：按类型缓存
func agentByKindKey(kind string) string {
    return fmt.Sprintf("agent:kind:%s", kind)
}
```

---

### **建议 3：完善种子文件**

**当前种子位置：**
```
agents/           # 在项目根目录？
├── planner.md
└── executor.md
```

**优化为：**
```
internal/config/seed/agents/
├── planner.md
├── executor.md
└── evaluator.md    # 新增
```

---

### **建议 4：目录结构保持不变**

**现有结构已经很好：**

```
internal/config/
├── agent/          # Agent 专用配置
│   ├── model.go
│   └── store.go
│
├── llmcfg/         # LLM 专用配置
│   ├── model.go
│   └── store.go
│
├── tool/           # 工具专用配置
│   ├── model.go
│   └── store.go
│
├── settingstore/   # 系统设置
│   ├── model.go
│   └── store.go
│
├── configstore/    # 通用三级缓存层（所有配置共用）
│   ├── store.go
│   └── adapter.go
│
├── seed/           # 统一种子加载
│   ├── seed.go
│   ├── agents/     # 种子文件
│   │   ├── planner.md
│   │   ├── executor.md
│   │   └── evaluator.md
│   ├── system.go
│   └── llm.go
│
└── config.go       # 顶层配置（Viper）
```

**扩展性：**
```
# 将来新增概念（如 Workflow 配置）
internal/config/
├── workflow/       # 新增
│   ├── model.go
│   └── store.go
└── seed/
    └── workflows/  # 新增种子文件
        └── default.yaml
```

---

## ✅ 最终方案

### **不需要重新设计！只需要：**

#### **1. 添加 Evaluator Agent（15分钟）**
```bash
# 步骤 1: 创建 migration
cat > db/migrations/0132_add_evaluator_agent.up.sql

# 步骤 2: 更新 Go 枚举
# internal/config/agent/model.go: 添加 KindEvaluator

# 步骤 3: 创建种子文件
cat > internal/config/seed/agents/evaluator.md
```

#### **2. 扩展 HTTP API（30分钟）**
```go
// internal/httpapi/config_handler.go
// 添加通用 Agent CRUD（支持 planner/executor/evaluator）
```

#### **3. 删除 hunters 目录（1分钟）**
```bash
rm -rf hunters
```

#### **4. 更新文档（可选）**
```bash
# 说明新的 3-Agent 架构
docs/AGENT_CONFIG_SYSTEM.md
```

---

## 📊 工作量对比

| 方案 | 工作量 | 结果 |
|------|--------|------|
| **我的原始方案** | 8-10 小时 | ❌ 90% 重复造轮子 |
| **复用现有系统** | 1 小时 | ✅ 只需添加 Evaluator |

---

## 🎉 总结

### **好消息：**
1. ✅ **已有完善的三级缓存系统**（configstore）
2. ✅ **已有 Agent 配置表**（agent 表）
3. ✅ **已有种子加载系统**（seed 包）
4. ✅ **已有 HTTP API**（config_handler）
5. ✅ **目录结构已经很好**（可扩展）

### **需要做的：**
1. ✅ 添加 Evaluator Agent（migration + 种子 + 枚举）
2. ✅ 扩展 HTTP API（通用 Agent CRUD）
3. ✅ 删除 hunters 目录

### **不需要做的：**
1. ❌ 重新设计三级缓存
2. ❌ 重新设计数据库表
3. ❌ 重新实现种子加载
4. ❌ 重新设计目录结构
5. ❌ 重新实现 HTTP API

---

**要开始添加 Evaluator 吗？** 🚀
