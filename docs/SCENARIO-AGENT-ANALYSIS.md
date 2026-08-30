# Scenario 和 Agent 在新架构中的沿用分析

**分析日期**: 2026-08-30  
**分析目标**: 评估老架构的 Scenario 和 Agent 概念是否适配新架构

---

## 📋 执行摘要

### Scenario（场景）
- **建议**: ✅ **可以沿用**，但需重新定义
- **原因**: 概念本身有价值，但当前实现与新架构不完全匹配
- **改动**: 需要适配新的双层监察架构

### Agent（智能体）
- **建议**: ✅ **可以沿用**，概念已部分适配
- **原因**: Agent已区分Planner和Executor，与新架构一致
- **改动**: 概念清晰，但需补充Orchestrator层配置

---

## 第一部分：当前 Agent 架构分析

### 1.1 Agent 数据模型

**数据库表结构**（agent表）:
```sql
CREATE TABLE agent (
    id             uuid        PRIMARY KEY,
    code           text        NOT NULL UNIQUE,
    kind           text        NOT NULL CHECK (kind IN ('planner','executor')),
    name           text        NOT NULL,
    description    text        NOT NULL DEFAULT '',
    body           text        NOT NULL DEFAULT '',      -- System Prompt
    tools          jsonb       NOT NULL DEFAULT '[]',
    max_iterations int         NOT NULL DEFAULT 40,
    enabled        boolean     NOT NULL DEFAULT true,
    complexity     text,                                 -- simple|medium|complex
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
```

**Go类型定义**（internal/config/agent/model.go）:
```go
type Kind string

const (
    KindPlanner  Kind = "planner"   // 规划型（Planner Agent）
    KindExecutor Kind = "executor"  // 执行型（Executor Agent）
)

type Agent struct {
    ID            string
    Code          string
    Kind          Kind
    Name          string
    Description   string
    Body          string        // System Prompt（方法论charter）
    FunctionTools []string      // 内置函数工具
    CliTools      []string      // 外部CLI工具
    MaxIterations int
    Enabled       bool
    Complexity    string        // simple|medium|complex
    CreatedAt     time.Time
    UpdatedAt     time.Time
}
```

### 1.2 Agent 在新架构中的映射

**当前Agent Kind → 新架构组件映射**:

| Agent Kind | 新架构组件 | 说明 | 适配情况 |
|-----------|-----------|------|---------|
| `planner` | Planner | 宏观规划（6分钟评估） | ✅ 概念一致 |
| `executor` | Executor | 微观执行（5步评估） | ✅ 概念一致 |
| ❌ 缺失 | Orchestrator | 任务编排层（原cognition） | ⚠️ 未在Agent表中体现 |

**分析**:
- ✅ Agent的`planner`和`executor` kind **完全匹配**新架构的Planner和Executor
- ⚠️ 新架构的Orchestrator层（任务编排）没有对应的Agent配置
- ⚠️ Agent表存储的是"System Prompt配置"，而非"运行实例"

### 1.3 Agent 概念的清晰度

**优点**:
1. ✅ `kind`字段已区分planner和executor
2. ✅ `body`字段存储System Prompt（方法论）
3. ✅ `tools`字段存储工具配置
4. ✅ `complexity`字段已支持复杂度配置

**问题**:
1. ⚠️ `description`字段的用途不清晰（"派活摘要"）
2. ⚠️ 缺少Orchestrator层的配置
3. ⚠️ Planner的监察配置（6分钟间隔）未在Agent表中

**结论**: Agent概念**可以沿用**，但需要：
- 补充Orchestrator配置（或明确不需要配置）
- 明确各字段在新架构中的语义

---

## 第二部分：当前 Scenario 架构分析

### 2.1 Scenario 数据模型

**数据库表结构**（scenario表）:
```sql
CREATE TABLE scenario (
    id                uuid        PRIMARY KEY,
    code              text        NOT NULL UNIQUE,
    name              text        NOT NULL,
    description       text        NOT NULL DEFAULT '',
    instruction       text        NOT NULL DEFAULT '',  -- 场景指令
    engine            text        NOT NULL CHECK (engine IN ('solo','swarm')),
    solo_agent_id     uuid        REFERENCES agent(id),  -- solo模式的agent
    enabled           boolean     NOT NULL DEFAULT true,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
```

**Go类型定义**（internal/config/scenario/model.go）:
```go
type Engine string

const (
    EngineSolo  Engine = "solo"   // 单Agent执行
    EngineSwarm Engine = "swarm"  // 多Agent编排
)

type Scenario struct {
    ID             string
    Code           string
    Name           string
    Description    string
    Instruction    string      // 场景级指令
    Engine         Engine      // solo | swarm
    SoloAgentID    *string    // solo模式的executor引用
    Enabled        bool
    CreatedAt      time.Time
    UpdatedAt      time.Time
}
```

### 2.2 Scenario 在新架构中的语义

**当前Scenario的作用**:
1. **选择执行引擎**: `engine`字段决定solo（单Agent）还是swarm（多Agent编排）
2. **提供场景指令**: `instruction`字段注入到System Prompt
3. **关联Agent**: solo模式时指定单个executor

**新架构中的对应概念**:

| Scenario概念 | 新架构对应 | 匹配度 | 说明 |
|-------------|-----------|--------|------|
| `engine=solo` | Executor单独运行 | ✅ 匹配 | 微观执行 |
| `engine=swarm` | Planner + Executors | ⚠️ 部分匹配 | 宏观规划+微观执行 |
| `instruction` | 场景上下文 | ✅ 匹配 | 注入到Prompt |
| `solo_agent_id` | 单Executor引用 | ✅ 匹配 | Solo模式 |

**问题分析**:

#### 问题1: `engine`字段的语义模糊

**当前语义**: 
- `solo` = 单个Executor直接执行
- `swarm` = Planner编排多个Executor

**新架构语义**:
- **所有任务**都应该经过双层监察（Planner 6分钟 + Executor 5步）
- `solo`模式绕过了Planner，**不符合新架构**
- `swarm`模式才是新架构的正确实现

**矛盾**: 
- 新架构要求**所有任务都有Planner监察**
- 但`engine=solo`模式跳过了Planner
- 这与新架构的双层监察理念冲突

#### 问题2: Scenario与Task的关系不清晰

**当前实现**（cmd/runner/handler.go）:
```go
// 1. 读取scenario
scen, err := h.cfgStore.ScenarioByCode(ctx, p.ScenarioID)

// 2. 根据engine分支
switch scen.Engine {
case EngineSolo:
    // 获取solo_agent_id指定的executor
    // 直接执行，无Planner
    return h.handleSolo(ctx, p, scen, op, brief)
    
case EngineSwarm:
    // 启动Planner + Executor编排
    return h.handleSwarm(ctx, p, scen, executors, brief)
}
```

**问题**:
- Scenario决定了执行流程（solo vs swarm）
- 但新架构应该**始终使用双层监察**
- Solo模式的存在是架构漏洞

### 2.3 Scenario 的价值分析

**Scenario概念的核心价值**:
1. ✅ **场景分类**: 不同攻击场景有不同目标（如：SQL注入、XSS、权限提升）
2. ✅ **指令注入**: `instruction`字段提供场景特定上下文
3. ✅ **配置复用**: 预定义场景可以被多个任务复用

**Scenario应该提供什么**:
- ✅ 场景描述（description）
- ✅ 场景指令（instruction）
- ✅ 默认目标类型（如：web应用、API、二进制）
- ❌ **不应该**决定执行引擎（solo vs swarm）

**建议重构**:
- 保留Scenario表
- 删除`engine`字段（或固定为`swarm`）
- 删除`solo_agent_id`字段
- 增加`default_complexity`字段（场景默认复杂度）
- 增加`target_type`字段（web/binary/cloud/lateral）

---

## 第三部分：新架构适配方案

### 3.1 Agent 适配方案

#### 方案A：保持现状（推荐）
```go
// Agent表保持不变
// kind = planner | executor

// Orchestrator不需要配置（代码固定）
// 因为Orchestrator是框架层，不是业务可配置的
```

**理由**:
- Orchestrator是框架基础设施，不需要用户配置
- Planner和Executor的配置已足够
- 简化配置复杂度

#### 方案B：补充Orchestrator配置
```go
const (
    KindOrchestrator Kind = "orchestrator"  // 新增
    KindPlanner      Kind = "planner"
    KindExecutor     Kind = "executor"
)
```

**理由**:
- 架构完整性
- 未来可配置Orchestrator行为

**建议**: **方案A**（Orchestrator不需要配置）

### 3.2 Scenario 适配方案

#### 方案A：简化为场景模板（推荐）

**重构Scenario表**:
```sql
CREATE TABLE scenario (
    id                uuid        PRIMARY KEY,
    code              text        NOT NULL UNIQUE,
    name              text        NOT NULL,
    description       text        NOT NULL DEFAULT '',
    instruction       text        NOT NULL DEFAULT '',  -- 场景指令（保留）
    target_type       text        NOT NULL,             -- web/binary/cloud/lateral（新增）
    default_complexity text       NOT NULL DEFAULT 'medium',  -- 默认复杂度（新增）
    -- 删除: engine、solo_agent_id
    enabled           boolean     NOT NULL DEFAULT true,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);
```

**语义**:
- Scenario = 场景模板（攻击场景的描述和配置）
- 不再决定执行引擎（都走Planner + Executor）
- 提供默认配置（complexity, target_type）

#### 方案B：完全删除Scenario

**理由**:
- Task可以直接指定Planner和Executor
- 减少配置层级

**缺点**:
- 失去场景复用能力
- 每个任务都要重新配置

**建议**: **方案A**（简化但保留）

### 3.3 执行流程重构

**当前流程**（有问题）:
```
Task → Scenario → [solo: 直接Executor] 或 [swarm: Planner + Executors]
```

**新架构流程**（统一）:
```
Task → Scenario（场景配置） → Orchestrator → Planner（6分钟评估） → Executor（5步评估）
```

**关键改动**:
1. 删除solo模式（或废弃）
2. 所有任务都经过Planner监察
3. Scenario只提供配置，不决定流程

---

## 第四部分：数据库迁移计划

### 4.1 Agent表改动（无需改动）

```sql
-- Agent表保持不变
-- 已有的kind='planner'和'executor'完全适配新架构
```

### 4.2 Scenario表改动

```sql
-- 迁移0126: Scenario适配新架构

-- 1. 添加新字段
ALTER TABLE scenario ADD COLUMN target_type text;
ALTER TABLE scenario ADD COLUMN default_complexity text DEFAULT 'medium';

-- 2. 迁移数据（根据现有solo_agent_id推断target_type）
UPDATE scenario SET target_type = 'web' WHERE solo_agent_id IS NOT NULL;

-- 3. 废弃旧字段（但保留以兼容）
ALTER TABLE scenario ADD COLUMN engine_deprecated text;
UPDATE scenario SET engine_deprecated = engine;
ALTER TABLE scenario DROP COLUMN engine;

-- 4. solo_agent_id改为可选（swarm模式不需要）
-- 保留字段但语义变为"推荐的默认executor"
ALTER TABLE scenario RENAME COLUMN solo_agent_id TO default_executor_id;

-- 5. 添加约束
ALTER TABLE scenario ADD CONSTRAINT scenario_target_type_check 
    CHECK (target_type IN ('web','binary','cloud','lateral'));
    
ALTER TABLE scenario ADD CONSTRAINT scenario_default_complexity_check 
    CHECK (default_complexity IN ('simple','medium','complex'));
```

### 4.3 代码改动

**删除solo模式**:
```go
// cmd/runner/handler.go

// 删除
switch scen.Engine {
case EngineSolo:
    return h.handleSolo(...)
case EngineSwarm:
    return h.handleSwarm(...)
}

// 改为
// 所有任务都走Planner + Executor
return h.handleWithPlanner(ctx, p, scen, brief)
```

---

## 📊 总结与建议

### Agent 结论

✅ **可以直接沿用**
- kind='planner' → 新架构Planner ✅
- kind='executor' → 新架构Executor ✅
- 无需修改表结构
- 无需修改代码

### Scenario 结论

⚠️ **可以沿用，但需重构**

**必须改动**:
1. 删除`engine`字段（或废弃solo模式）
2. 重新定义语义：从"执行引擎选择器"改为"场景配置模板"
3. 添加`target_type`和`default_complexity`字段

**可选改动**:
1. 重命名`solo_agent_id`为`default_executor_id`
2. 补充场景元数据（如：MITRE ATT&CK映射）

### 实施优先级

**P0（必须）**:
- ✅ Agent无需改动
- ⚠️ 废弃Scenario的solo模式
- ⚠️ 统一所有任务走Planner + Executor

**P1（重要）**:
- 重构Scenario表结构
- 添加target_type和default_complexity
- 创建迁移脚本

**P2（可选）**:
- 补充Scenario元数据
- 前端UI适配新字段

---

**分析完成时间**: 2026-08-30  
**建议**: Agent直接沿用，Scenario需重构后沿用
