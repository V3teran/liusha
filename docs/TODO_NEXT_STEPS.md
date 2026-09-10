# 🎯 后续工作清单

## ✅ 已完成

### **1. 架构重构**
- ✅ worldmodel → knowledgegraph
- ✅ lead → insight
- ✅ verifier → evaluator
- ✅ Priority 统一为字符串枚举

### **2. 目录重命名**
- ✅ llmcfg → llm
- ✅ settingstore → setting
- ✅ configstore → cache

---

## 📋 接下来需要做的工作

### **优先级 P0：核心功能完善**

#### **1. 添加 Evaluator Agent（1小时）** ⭐⭐⭐⭐⭐

**现状：**
- ❌ 只有 Planner 和 Executor
- ❌ 缺少 Evaluator Agent

**需要做：**

##### **1.1 创建数据库 migration（15分钟）**
```sql
-- db/migrations/0132_add_evaluator_agent.up.sql
INSERT INTO agent (
    code, kind, name, description, system_prompt,
    function_tools, cli_tools, max_iterations, complexity,
    is_builtin, enabled
) VALUES (
    'evaluator',
    'evaluator',
    'Evaluator Agent',
    '评估和验证 Observation，产生 Result',
    '你是 Evaluator Agent。

## Role
评估 Observation 的正确性，验证声明，产生确认的 Result。

## Responsibilities
1. 评估 Observation（验证 vs 证伪）
2. 区分"工具说的"和"实际是真的"
3. 产生 verified Result
4. 标记 Confidence

## Tools
- write_evaluation
- write_result
- read_knowledge_graph

## Constraints
- 不执行 Action（Executor 的职责）
- 不规划（Planner 的职责）
- 只基于实际证据评估',
    '["write_evaluation", "write_result", "read_knowledge_graph"]'::jsonb,
    '[]'::jsonb,
    30,
    'standard',
    true,
    true
);
```

##### **1.2 更新 Go 枚举（5分钟）**
```go
// internal/config/agent/model.go
const (
    KindPlanner   Kind = "planner"
    KindExecutor  Kind = "executor"
    KindEvaluator Kind = "evaluator"  // 新增
)
```

##### **1.3 创建种子文件（10分钟）**
```bash
# 创建 internal/config/seed/agents/evaluator.md
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
```

##### **1.4 运行 migration（5分钟）**
```bash
# 运行数据库迁移
make migrate-up
# 或
go run cmd/migrate/main.go up
```

---

#### **2. 扩展 HTTP API（30分钟）** ⭐⭐⭐⭐

**现状：**
- ❌ 只有 Executor 的 CRUD API
- ❌ 缺少通用 Agent API

**需要做：**

##### **2.1 添加通用 Agent API**
```go
// internal/httpapi/config_handler.go

// 新增：通用 Agent CRUD（支持 planner/executor/evaluator）
func RegisterAgentRoutes(r *gin.RouterGroup, api ConfigAPI) {
    agents := r.Group("/agents")
    {
        // 列出所有 Agent
        agents.GET("", listAllAgents(api))
        
        // 单个 Agent CRUD
        agents.GET("/:code", getAgentByCode(api))       // planner/executor/evaluator
        agents.PUT("/:code", updateAgent(api))
        agents.POST("/:code/reset", resetAgent(api))    // 重置为默认
        agents.GET("/:code/history", getHistory(api))   // 配置历史（可选）
    }
}
```

##### **2.2 实现处理函数**
```go
func listAllAgents(api ConfigAPI) gin.HandlerFunc {
    return func(c *gin.Context) {
        // 列出 planner + executor + evaluator
        agents := []string{"planner", "executor", "evaluator"}
        result := []gin.H{}
        
        for _, code := range agents {
            cfg, err := api.GetAgentByCode(c.Request.Context(), code)
            if err != nil {
                continue
            }
            result = append(result, agentToJSON(cfg))
        }
        
        c.JSON(200, gin.H{"agents": result})
    }
}

func getAgentByCode(api ConfigAPI) gin.HandlerFunc {
    return func(c *gin.Context) {
        code := c.Param("code")
        cfg, err := api.GetAgentByCode(c.Request.Context(), code)
        if err != nil {
            c.JSON(404, gin.H{"error": "agent not found"})
            return
        }
        c.JSON(200, gin.H{"agent": agentToJSON(cfg)})
    }
}

func updateAgent(api ConfigAPI) gin.HandlerFunc {
    return func(c *gin.Context) {
        code := c.Param("code")
        var updates agent.UpdateParams
        
        if err := c.ShouldBindJSON(&updates); err != nil {
            c.JSON(400, gin.H{"error": err.Error()})
            return
        }
        
        cfg, err := api.UpdateAgent(c.Request.Context(), code, updates, "user:admin")
        if err != nil {
            c.JSON(500, gin.H{"error": err.Error()})
            return
        }
        
        c.JSON(200, gin.H{"agent": agentToJSON(cfg)})
    }
}

func resetAgent(api ConfigAPI) gin.HandlerFunc {
    return func(c *gin.Context) {
        code := c.Param("code")
        
        // 从种子文件重新加载
        if err := api.ResetAgentToDefault(c.Request.Context(), code); err != nil {
            c.JSON(500, gin.H{"error": err.Error()})
            return
        }
        
        c.JSON(200, gin.H{"ok": true, "message": "reset to default"})
    }
}
```

---

#### **3. 删除 hunters 目录（1分钟）** ⭐⭐⭐⭐⭐

```bash
rm -rf hunters
git add hunters
git commit -m "chore: 移除过时的 hunters 文档

理由：
1. 架构已彻底重构为通用 ADK（Planner/Executor/Evaluator）
2. 定位已根本改变（安全专用 → 通用 ADK）
3. 文档已完全过时
4. 无需要提取的核心价值

新架构文档：
- docs/ARCHITECTURE_FINAL_REVIEW_COMPLETE.md
- docs/EXISTING_CONFIG_SYSTEM_ANALYSIS.md
"
```

---

### **优先级 P1：配置完善**

#### **4. 完善 cache 包文档（15分钟）** ⭐⭐⭐

```go
// internal/cache/store.go

// Package cache 实现通用三级缓存层：内存 L1 → Redis L2 → PostgreSQL。
//
// 设计目标：
//   - 多进程支持：api 改配置后，runner 自动失效本地缓存
//   - 失效总线：Redis pubsub 广播失效消息
//   - 资源无关：所有配置（agent/llm/tool/setting）共用同一缓存机制
//
// 使用方式：
//   cache := cache.New(pool, redisClient)
//   
//   // 写入（自动失效缓存）
//   cache.Set(ctx, "agent:planner", data)
//   
//   // 读取（L1 → L2 → DB）
//   cache.Get(ctx, "agent:planner")
package cache
```

---

#### **5. 更新配置文档（30分钟）** ⭐⭐⭐

**创建：`docs/CONFIGURATION_GUIDE.md`**

```markdown
# Liusha 配置管理指南

## 目录结构

internal/config/
├── agent/          # Agent 配置（Planner/Executor/Evaluator）
├── llm/            # LLM 配置（Provider/API Key）
├── tool/           # 工具配置
├── setting/        # 系统设置
├── cache/          # 三级缓存层（内存→Redis→PostgreSQL）
└── seed/           # 种子初始化
    └── agents/
        ├── planner.md
        ├── executor.md
        └── evaluator.md

## Agent 配置

### 三个 Agent
1. **Planner** - 规划和分解任务
2. **Executor** - 执行 Action，产生 Observation
3. **Evaluator** - 评估 Observation，产生 Result

### 配置方式

#### 方式 1：前端编辑
GET  /api/agents/:code
PUT  /api/agents/:code
POST /api/agents/:code/reset

#### 方式 2：种子文件
编辑 internal/config/seed/agents/*.md
重启应用自动加载

#### 方式 3：数据库直接修改
UPDATE agent SET system_prompt = '...' WHERE code = 'planner';
-- 需要手动清除缓存

## 三级缓存机制

写入流程：
1. 更新 PostgreSQL
2. Redis 广播失效
3. 各进程清除内存缓存

读取流程：
1. 查内存 L1 → 命中返回
2. 查 Redis L2 → 命中回填 L1
3. 查 PostgreSQL → 回填 L2 + L1

## 最佳实践

1. ✅ 通过 API 修改配置（自动失效缓存）
2. ❌ 不要直接改数据库（缓存不会失效）
3. ✅ 重要配置留历史（agent_config_history 表）
4. ✅ 定期备份种子文件
```

---

### **优先级 P2：文档清理**

#### **6. 清理临时文档（10分钟）** ⭐⭐

```bash
# 删除分析过程文档（保留最终文档）
rm docs/HUNTERS_ANALYSIS.md
rm docs/HUNTERS_VALUE_ASSESSMENT.md
rm docs/HUNTERS_TO_ADK_MAPPING.md
rm docs/HUNTERS_HONEST_REFLECTION.md

# 保留的核心文档：
# - docs/ARCHITECTURE_FINAL_REVIEW_COMPLETE.md
# - docs/EXISTING_CONFIG_SYSTEM_ANALYSIS.md
# - docs/CONFIG_DIRECTORY_NAMING_OPTIMIZATION.md
# - docs/REFACTOR_COMPLETE.md
```

---

#### **7. 更新 README.md（20分钟）** ⭐⭐⭐

```markdown
# Liusha - AI Agent Development Kit

## 架构

### 核心概念（ReAct 模式）
1. **Objective** - 目标
2. **Action** - 动作
3. **Observation** - 观察
4. **Evaluation** - 评估
5. **Result** - 结果

### 三个 Agent
1. **Planner** - 规划和分解任务
2. **Executor** - 执行 Action，产生 Observation
3. **Evaluator** - 评估 Observation，产生 Result

### 知识图谱（KnowledgeGraph）
- 所有数据存储为图节点
- 关系边表达依赖关系
- Roadmap 支持探索式规划

## 快速开始

### 配置
```bash
cp config.example.yaml config.yaml
# 编辑 config.yaml
```

### 运行
```bash
make run-api     # 启动 API 服务
make run-runner  # 启动 Runner
```

### 文档
- [架构设计](docs/ARCHITECTURE_FINAL_REVIEW_COMPLETE.md)
- [配置指南](docs/CONFIGURATION_GUIDE.md)
- [重构报告](docs/REFACTOR_COMPLETE.md)
```

---

## 📊 工作量估算

| 任务 | 优先级 | 工作量 | 状态 |
|------|--------|--------|------|
| **1. 添加 Evaluator** | P0 | 1小时 | ⏰ 待做 |
| **2. 扩展 HTTP API** | P0 | 30分钟 | ⏰ 待做 |
| **3. 删除 hunters** | P0 | 1分钟 | ⏰ 待做 |
| **4. 完善 cache 文档** | P1 | 15分钟 | ⏰ 待做 |
| **5. 更新配置文档** | P1 | 30分钟 | ⏰ 待做 |
| **6. 清理临时文档** | P2 | 10分钟 | ⏰ 待做 |
| **7. 更新 README** | P2 | 20分钟 | ⏰ 待做 |
| **总计** | - | **约 3小时** | - |

---

## 🎯 推荐执行顺序

### **今天完成（P0）：**
1. ✅ 删除 hunters（1分钟）
2. ⏰ 添加 Evaluator（1小时）
3. ⏰ 扩展 HTTP API（30分钟）

**总计：1.5小时**

### **本周完成（P1）：**
4. 完善 cache 文档（15分钟）
5. 更新配置文档（30分钟）

**总计：45分钟**

### **有空再做（P2）：**
6. 清理临时文档（10分钟）
7. 更新 README（20分钟）

**总计：30分钟**

---

## ✅ 立即行动

**下一步：删除 hunters 目录（1分钟）**

```bash
rm -rf hunters
git add hunters
git commit -m "chore: 移除过时的 hunters 文档"
```

**要继续吗？** 🚀
