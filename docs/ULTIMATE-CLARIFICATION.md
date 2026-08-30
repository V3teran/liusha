# Liusha 概念终极澄清与重构方案

**分析日期**: 2026-08-30  
**核心问题**: ExecutionLoop/Orchestrator到底是什么？Agent表设计？MainAgent功能？

---

## 🎯 核心澄清

### 问题1: ExecutionLoop/Orchestrator是什么？

**从代码看真相**（cmd/runner/cognition.go）:

```go
func (h handler) runCognition(ctx context.Context, ...) {
    // 1. 创建Executor（微观执行）
    executor := domainweb.NewExecutor(taskID, host, h.findings, run)
    
    // 2. 创建Planner（宏观规划）
    plannerAgent := planner.New(planner.Config{...})
    
    // 3. 在独立goroutine中启动Planner
    go func() {
        plannerAgent.Start(ctx)  // 异步运行
    }()
    
    // 4. 创建ExecutionLoop
    execLoop := orchestrator.NewExecutionLoop(
        h.world,
        executor,
        promoter,
        h.eventBus,
        h.logger,
    )
    
    // 5. 运行ExecutionLoop
    return execLoop.Run(ctx, taskID)
}
```

**真相**:
- **ExecutionLoop ≠ 凌驾于Planner之上**
- **ExecutionLoop = 执行循环（与Planner并行）**

**实际架构**:
```
        runCognition (入口函数)
              ↓
        ┌─────────┴──────────┐
        ↓                    ↓
   Planner（异步）    ExecutionLoop（主循环）
   6分钟评估            轮询执行Action
   写Action到世界模型      ↓
        ↓              读Action执行
    EventBus ←──────→  发布事件
```

**关键发现**:
1. **Planner和ExecutionLoop是并行的**，不是上下级
2. **Planner职责**: 评估世界模型 → 生成Action → 写入世界模型
3. **ExecutionLoop职责**: 轮询世界模型 → 读取Action → 调用Executor执行
4. **两者通过世界模型和EventBus通信**

**所以**:
- ❌ **不应该叫Scheduler**（不是调度Planner和Executor）
- ✅ **叫ExecutionLoop更准确**（执行循环）
- ✅ **或者叫TaskRunner**（任务运行器）

**命名建议**:
```
TaskRunner（任务运行器）
    - 启动Planner（异步）
    - 运行ExecutionLoop（主循环）
```

---

### 问题2: Agent表是否少了skill字段？

**当前Agent表字段**（推断）:
```go
type Agent struct {
    ID            string
    Code          string
    Kind          Kind        // planner | executor
    Name          string
    Description   string
    Body          string      // System Prompt
    FunctionTools []string    // ✅ 内置function工具
    CliTools      []string    // ✅ 外部CLI工具
    MaxIterations int
    Enabled       bool
    Complexity    string
}
```

**Skill在哪里？**

查看代码发现：
- `internal/skill/` 包存在
- 但**Agent表中没有skill字段**

**Skill的实际用途**:
- Skill = 特殊的工具包（如playwright-cli）
- Skill不是工具本身，是**工具的组合**

**建议**:
```sql
ALTER TABLE agent ADD COLUMN skills jsonb NOT NULL DEFAULT '[]';

-- 存储格式
-- skills: ["playwright-cli", "api-recon"]
```

**使用方式**:
```go
type Agent struct {
    Skills        []string    // Skill ID列表
    FunctionTools []string    // 内置function工具
    CliTools      []string    // 外部CLI工具
}

// 运行时
for _, skillID := range agent.Skills {
    skill := skillStore.Load(skillID)
    // 注入skill的tools
}
```

**结论**: ✅ **Agent表应该添加skills字段**

---

### 问题3: MainAgent的功能Liusha有吗？

**MainAgent的三大功能**:
1. **观察** - 回答人类问题（graph_overview, list_findings）
2. **操舵** - 传递人类意图（add_hint, add_intent）
3. **对话** - 与人类对话界面

**Liusha有这些功能吗？**

#### 功能1: 观察（有）
```
GET /attack_graph/:task_id  ← 已删除
GET /findings
GET /llm/invocations
GET /traffic
```

**结论**: ✅ Liusha有观察API，但**在前端实现**，不在MainAgent

#### 功能2: 操舵（无）
- `add_hint` → ❌ Liusha没有
- `add_intent` → ❌ Liusha没有
- 控制平面 → ⚠️ 有`/tasks/:id/control`，但不是对话式

**结论**: ⚠️ Liusha有控制平面API，但**不是对话式操舵**

#### 功能3: 对话（无）
- ARTEX: 人与MainAgent对话 → MainAgent理解意图 → add_hint/add_intent
- Liusha: 人直接操作前端 → 前端调用API → 直接控制

**结论**: ❌ Liusha没有对话式人在环路

**总结**: 
- Liusha的"人在环路"是**前端UI操作**，不是对话
- MainAgent的功能被**分散到前端和API**中
- **不需要单独的MainAgent LLM Agent**

---

## 📊 最终架构澄清

### 实际架构

```
cmd/runner/handler.handleSwarm()
    ↓
runCognition() (入口)
    ↓
┌───────────────┴──────────────┐
↓                              ↓
Planner（异步goroutine）    ExecutionLoop（主循环）
每6分钟评估                   轮询世界模型
生成Action                    执行Action
    ↓                            ↓
 WorldModel ←─────────→    Executor
    ↑                            ↓
    └──────── EventBus ──────────┘
```

**关键点**:
1. **Planner和ExecutionLoop并行运行**
2. **通过WorldModel和EventBus通信**
3. **没有"上级调度器"**

### 命名建议

**不要改名**:
- ✅ 保持 `orchestrator` 包名
- ✅ 保持 `ExecutionLoop` 类型名
- ✅ 添加注释说明其真实职责

**添加注释**:
```go
// ExecutionLoop 是执行循环，与Planner并行运行。
// Planner生成Action写入世界模型，ExecutionLoop轮询并执行这些Action。
// 两者通过WorldModel和EventBus通信，形成完整的认知循环。
```

---

## 🎯 Agent表最终设计

### 完整字段

```sql
CREATE TABLE agent (
    id            uuid PRIMARY KEY,
    code          text NOT NULL UNIQUE,
    kind          text NOT NULL CHECK (kind IN ('planner','executor')),
    name          text NOT NULL,
    description   text NOT NULL,
    
    -- Prompt配置
    system_prompt text NOT NULL,
    
    -- 工具配置
    skills        jsonb NOT NULL DEFAULT '[]',    -- ← 新增：Skill ID列表
    function_tools jsonb NOT NULL DEFAULT '[]',   -- 内置function工具
    cli_tools     jsonb NOT NULL DEFAULT '[]',    -- 外部CLI工具
    
    -- 执行配置
    max_iterations int NOT NULL DEFAULT 40,
    complexity    text NOT NULL DEFAULT 'medium',
    
    -- 元数据
    enabled       boolean NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
```

### 示例数据

```sql
-- Planner
INSERT INTO agent (code, kind, name, description, system_prompt)
VALUES ('default-planner', 'planner', '默认规划者', '负责全局规划', '你是规划者...');

-- Web Executor
INSERT INTO agent (code, kind, name, description, system_prompt, skills, function_tools, cli_tools)
VALUES (
    'web-executor', 
    'executor', 
    'Web渗透专家', 
    '专注于Web应用安全测试',
    '你是Web渗透专家...',
    '["playwright-cli", "api-recon"]',           -- Skills
    '["http_request", "parse_html"]',            -- Function tools
    '["curl", "sqlmap", "nikto"]'                -- CLI tools
);
```

---

## 🎯 前端UI设计

### 简化为平铺列表

```
智能体管理
├── 默认规划者（Planner）
├── Web渗透专家（Executor）
├── 二进制分析专家（Executor）
├── 云安全专家（Executor）
└── 内网横移专家（Executor）
```

**不要子栏目**，直接平铺显示，用标签区分：

```
[表格]
名称              类型      描述                  状态
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
默认规划者      [规划者]  负责全局规划           启用
Web渗透专家     [执行者]  专注Web应用安全        启用
二进制分析专家  [执行者]  专注二进制分析         启用
云安全专家      [执行者]  专注云环境渗透         启用
内网横移专家    [执行者]  专注内网横移           启用
```

---

## 📋 数据库迁移

### 迁移脚本

```sql
-- 0126: 统一Agent概念，删除Scenario

-- 1. 添加skills字段到agent表
ALTER TABLE agent ADD COLUMN skills jsonb NOT NULL DEFAULT '[]';

-- 2. 重命名body为system_prompt
ALTER TABLE agent RENAME COLUMN body TO system_prompt;

-- 3. 从scenario迁移数据（如果有用的instruction）
-- 将scenario.instruction追加到对应executor的system_prompt
UPDATE agent SET system_prompt = system_prompt || E'\n\n' || s.instruction
FROM scenario s
WHERE agent.code = s.solo_executor_id;

-- 4. 删除scenario表
DROP TABLE scenario CASCADE;

-- 5. 清理相关外键和索引
-- (scenario相关的清理)
```

---

## 🎯 MainAgent功能对比

| 功能 | ARTEX MainAgent | Liusha实现 | 是否需要 |
|------|----------------|-----------|---------|
| **观察** | LLM理解问题 | 前端API调用 | ❌ 不需要LLM |
| **操舵** | add_hint/add_intent | 控制平面API | ⚠️ 可选（对话式） |
| **对话** | 与人对话 | 前端UI操作 | ❌ 不需要LLM |

**结论**:
- Liusha的人在环路通过**前端UI**实现，更直观
- **不需要MainAgent LLM**（增加复杂度和成本）
- 可选：未来添加**对话式操舵**功能

---

## 🎯 最终建议

### 1. Agent表改动
```sql
-- 添加skills字段
ALTER TABLE agent ADD COLUMN skills jsonb NOT NULL DEFAULT '[]';

-- 重命名body → system_prompt
ALTER TABLE agent RENAME COLUMN body TO system_prompt;
```

### 2. 删除Scenario表
```sql
DROP TABLE scenario CASCADE;
```

### 3. 保持命名不变
- ✅ 保持 `orchestrator` 包名
- ✅ 保持 `ExecutionLoop` 类型名
- ✅ 添加详细注释说明

### 4. 前端UI
- ✅ "智能体管理"
- ✅ 平铺列表，不要子栏目
- ✅ 用标签区分[规划者]/[执行者]

### 5. MainAgent
- ❌ 不需要实现（前端UI足够）
- ⚠️ 未来可选：对话式操舵

---

## 📊 架构总结

**真实架构**（并行，不是层级）:
```
runCognition()
    ↓
    启动Planner（异步goroutine，6分钟评估）
    运行ExecutionLoop（主循环，轮询执行）
    ↓
通过WorldModel + EventBus通信
```

**命名**:
- orchestrator/ExecutionLoop = ✅ 准确（执行循环）
- Scheduler = ❌ 不准确（不是调度器）

**Agent表**:
- ✅ 添加skills字段
- ✅ 重命名body → system_prompt
- ✅ 删除Scenario表

**前端UI**:
- ✅ 智能体管理（平铺列表）
- ✅ 标签区分类型

---

**完成时间**: 2026-08-30  
**核心结论**: ExecutionLoop与Planner并行，不是上下级关系
