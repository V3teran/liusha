# Scenario vs Agent 终极概念重构方案

**分析日期**: 2026-08-30  
**核心问题**: Profile翻译、Agent表内容、MainAgent、Orchestrator、命名优雅性

---

## 🎯 问题逐一解答

### 问题1: Profile翻译成什么？

**Profile的几种翻译**:
- ❌ **配置档** - 太技术化，不适合用户界面
- ❌ **档案** - 偏静态，不体现动态性
- ⚠️ **配置** - 太泛，与系统配置混淆
- ✅ **智能体** - 保留Agent翻译，但重新定义

**重新理解"智能体"**:
- 用户视角：**智能体 = 一个有特定能力的助手**
- 技术视角：**智能体 = System Prompt + Tools + 配置**
- 实际就是：**扮演某个角色的LLM配置**

**结论**: 
- 保留**Agent（智能体）**这个名字
- 但明确：**Agent = 可配置的Prompt模板**，不是独立推理实体
- 前端UI就叫"智能体管理"

---

### 问题2: 原Agent表里放的是什么？

**从代码推断Agent表的内容**:

```go
// composeSoloInstruction 构建solo agent的完整system prompt
func composeSoloInstruction(scen Scenario, op Agent) string {
    var b strings.Builder
    b.WriteString(executorbuilder.SystemPrompt())  // 基础框架Prompt
    if scen.Instruction != "" {
        b.WriteString("\n\n")
        b.WriteString(scen.Instruction)  // Scenario的指令
    }
    if op.Body != "" {
        b.WriteString("\n\n")
        b.WriteString(op.Body)  // Agent的Body
    }
    return b.String()
}

// swarmSystemPrompt 构建swarm的system prompt
func swarmSystemPrompt(orchBody, scenInstruction string, subAgents []Agent) string {
    // ...
    if len(subAgents) > 0 {
        b.WriteString("\n\n## 可用专项代理\n")
        for _, a := range subAgents {
            b.WriteString(fmt.Sprintf("- **%s**: %s\n", a.Name, a.Description))
        }
    }
}
```

**Agent表的实际内容**:
- `kind='planner'` - 规划者的配置（1个）
- `kind='executor'` - 执行者的配置（多个，如web、binary、cloud、lateral）

**示例数据推断**:
```sql
-- Planner（规划者）
INSERT INTO agent (code, kind, name, description, body)
VALUES ('planner', 'planner', '规划者', '负责全局规划和任务分解', '你是一个渗透测试规划者...');

-- Executors（执行者）
INSERT INTO agent (code, kind, name, description, body)
VALUES 
    ('web-executor', 'executor', 'Web执行者', '负责Web应用渗透', '你是一个Web渗透专家...'),
    ('binary-executor', 'executor', '二进制执行者', '负责二进制分析', '你是一个二进制安全专家...'),
    ('cloud-executor', 'executor', '云执行者', '负责云环境渗透', '你是一个云安全专家...'),
    ('lateral-executor', 'executor', '横移执行者', '负责内网横移', '你是一个内网渗透专家...');
```

**关键发现**:
- Agent表存储的是**不同领域的执行者配置**
- 每个Agent有自己的**专业领域描述**（Description）
- 每个Agent有自己的**方法论**（Body）

---

### 问题3: ARTEX的MainAgent是干什么的？

**MainAgent的职责**（从注释）:
```go
// MainAgent is the thin human-interface orchestrator (docs §4.2 / §7). 
// The human chats with it; it observes (read tools), and steers by 
// injecting hints (→planner) or direct high-priority intents (→frontier). 
// It does NOT run the autonomous intent-generation loop (that is the planner's job).
```

**翻译**:
- **MainAgent = 人机接口层**
- 职责1：**观察** - 回答人类关于进展的问题（用graph_overview等工具）
- 职责2：**操舵** - 把人类意图传递给系统
  - 用`add_hint`写提示 → Planner下次会读到
  - 用`add_intent`直接注入高优先级意图 → Worker立即执行
- 职责3：**不生成意图** - 它不自主规划（那是Planner的工作）

**ARTEX的架构**:
```
Human ←→ MainAgent（对话界面）
           ↓ add_hint/add_intent
         Planner（规划者）
           ↓ generate intents
         Worker（执行者 ×N）
```

**类比**:
- MainAgent = **前台接待**（接待客户，传递需求）
- Planner = **项目经理**（制定计划）
- Worker = **工程师**（执行任务）

---

### 问题4: Liusha的Orchestrator是干什么的？

**Orchestrator的职责**（从代码）:
```go
// ExecutionLoop 基于统一世界模型的执行循环
type ExecutionLoop struct {
    world    *worldmodel.Store
    executor Executor
    promoter Promoter
    eventBus *EventBus
}

func (l *ExecutionLoop) Run(ctx context.Context, taskID string) (Report, error) {
    // 1. 轮询待执行的Action
    // 2. 调用Executor执行
    // 3. 调用Promoter验证并晋升到世界模型
    // 4. 发布事件
}
```

**Orchestrator的职责**:
- 职责1：**调度循环** - 轮询世界模型，找到pending的Action
- 职责2：**执行编排** - 调用Executor执行Action
- 职责3：**结果验证** - 调用Promoter验证Attempt
- 职责4：**事件发布** - 通过EventBus通知Planner

**对比MainAgent**:
| 维度 | ARTEX MainAgent | Liusha Orchestrator |
|------|----------------|-------------------|
| **职责** | 人机接口（对话） | 任务调度循环 |
| **输入** | 人类消息 | 世界模型中的Action |
| **输出** | add_hint/add_intent | 执行结果 + 事件 |
| **是否对话** | ✅ 是（LLM驱动） | ❌ 否（框架代码） |

**结论**: 
- **MainAgent ≠ Orchestrator**
- MainAgent是**对话层**（人在环路）
- Orchestrator是**调度层**（框架固定）

---

### 问题5: Orchestrator和Planner命名是否优雅？

**当前架构层级**:
```
Orchestrator（任务调度）
    ↓
Planner（宏观规划 - 6分钟评估）
    ↓
Executor（微观执行 - 5步评估）
```

**命名分析**:

| 层级 | 当前命名 | 职责 | 问题 |
|------|---------|------|------|
| L1 | Orchestrator | 任务调度循环 | ⚠️ 名字暗示"编排"，但实际是调度 |
| L2 | Planner | 宏观规划 | ✅ 准确 |
| L3 | Executor | 微观执行 | ✅ 准确 |

**命名问题**:
1. **Orchestrator** - "编排者"听起来像是在做规划，但实际只是调度
2. **Planner vs Orchestrator** - 两个名字都有"协调"的含义，容易混淆

**更优雅的命名方案**:

#### 方案A: 重命名Orchestrator为Scheduler
```
Scheduler（任务调度器）
    ↓
Planner（宏观规划器）
    ↓
Executor（微观执行器）
```

**优点**:
- ✅ Scheduler更准确（就是调度循环）
- ✅ 三者职责清晰：调度 → 规划 → 执行
- ✅ 没有语义重叠

#### 方案B: 重命名Orchestrator为Engine
```
Engine（执行引擎）
    ↓
Planner（规划器）
    ↓
Executor（执行器）
```

**优点**:
- ✅ Engine是中性词（不暗示具体职责）
- ✅ 强调"驱动"的角色

#### 方案C: 重命名Planner为Strategist
```
Orchestrator（编排器）
    ↓
Strategist（策略器）
    ↓
Executor（执行器）
```

**优点**:
- ✅ Orchestrator保留原名
- ✅ Strategist与Planner区分开

**推荐**: **方案A（Scheduler）**
- 最准确（Orchestrator确实就是Scheduler）
- 最清晰（调度 → 规划 → 执行）

---

## 📊 最终概念重构方案

### 统一结论

**保留Agent概念，删除Scenario概念**:

1. **Agent表** → 保留并重新定义
   - 前端叫"智能体管理"
   - 存储：System Prompt + Tools + 配置
   - 包含：Planner配置 + 多个Executor配置

2. **Scenario表** → 删除
   - 与Agent冗余
   - engine字段违反新架构
   - instruction可以合并到Agent.Body

3. **前端UI**:
   ```
   智能体管理
   ├── 规划者（Planner）
   │   └── 默认规划者
   └── 执行者（Executor）
       ├── Web渗透专家
       ├── 二进制分析专家
       ├── 云安全专家
       └── 内网横移专家
   ```

### 重构后的Agent表

```sql
CREATE TABLE agent (
    id            uuid PRIMARY KEY,
    code          text NOT NULL UNIQUE,
    kind          text NOT NULL CHECK (kind IN ('planner','executor')),
    name          text NOT NULL,
    description   text NOT NULL,  -- 专业领域描述
    system_prompt text NOT NULL,  -- 完整System Prompt
    tools         jsonb NOT NULL DEFAULT '[]',
    max_iterations int NOT NULL DEFAULT 40,
    complexity    text NOT NULL DEFAULT 'medium',
    enabled       boolean NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
```

**示例数据**:
```sql
-- Planner
INSERT INTO agent (code, kind, name, description, system_prompt)
VALUES ('default-planner', 'planner', '默认规划者', '负责全局任务规划和分解', '你是一个渗透测试规划者...');

-- Executors
INSERT INTO agent (code, kind, name, description, system_prompt, tools)
VALUES 
    ('web-executor', 'executor', 'Web渗透专家', '专注于Web应用安全测试', '你是一个Web渗透专家...', '["http_request","parse_html"]'),
    ('binary-executor', 'executor', '二进制分析专家', '专注于二进制安全分析', '你是一个二进制安全专家...', '["gdb","radare2"]');
```

### 使用方式

**任务创建**:
```go
task := CreateTask{
    ExecutorCode: "web-executor",  // 选择一个Executor
}
```

**运行时**:
```go
// 1. 固定使用默认Planner
planner := store.GetAgent("default-planner")

// 2. 根据任务选择Executor
executor := store.GetAgent(task.ExecutorCode)

// 3. 始终走：Scheduler → Planner → Executor
scheduler := NewScheduler()
scheduler.Run(planner, executor)
```

---

## 🎯 命名重构建议

### 建议1: 重命名Orchestrator为Scheduler

**修改**:
```go
// 原
package orchestrator
type ExecutionLoop struct {}

// 改为
package scheduler
type Scheduler struct {}
```

**理由**:
- ✅ 更准确（就是调度循环）
- ✅ 与Planner不冲突
- ✅ 清晰的层级：Scheduler → Planner → Executor

### 建议2: 保持Planner和Executor不变

**层级**:
```
Scheduler（调度器 - 框架层）
    ↓
Planner（规划器 - 配置层，6分钟评估）
    ↓
Executor（执行器 - 配置层，5步评估）
```

---

## 📋 对比ARTEX

| 层级 | ARTEX | Liusha（重构后） |
|------|-------|-----------------|
| 人机接口 | MainAgent | ❌ 无（未来可添加） |
| 任务调度 | Engine | Scheduler（原Orchestrator） |
| 规划层 | Planner | Planner |
| 执行层 | Worker ×N | Executor |
| 配置表 | agents（role: mainagent/planner/worker） | agent（kind: planner/executor） |

**核心差异**:
1. ARTEX有**MainAgent**（人在环路对话）- Liusha缺失
2. Liusha有**双层监察**（Planner 6分钟 + Executor 5步）- ARTEX没有这么明确

---

## 🎯 最终建议

### 1. Agent表 - 保留并简化
```sql
-- 删除Scenario表
DROP TABLE scenario;

-- Agent表保留，重命名字段
ALTER TABLE agent RENAME COLUMN body TO system_prompt;
```

### 2. 前端UI - 智能体管理
```
智能体管理
├── 规划者（1个，框架默认）
└── 执行者（多个，用户可配置）
    ├── Web渗透专家
    ├── 二进制分析专家
    ├── 云安全专家
    └── 内网横移专家
```

### 3. 代码重构 - 重命名Orchestrator
```go
// internal/orchestrator → internal/scheduler
package scheduler

type Scheduler struct {
    planner  *planner.Agent
    executor *executor.Executor
}
```

### 4. 架构清晰化
```
Scheduler（任务调度循环）
    ↓
Planner（宏观规划 - 6分钟评估）
    ↓
Executor（微观执行 - 5步评估）
```

---

**完成时间**: 2026-08-30  
**核心结论**: 
- ✅ 保留Agent（智能体），删除Scenario
- ✅ 重命名Orchestrator为Scheduler
- ✅ Planner和Executor保持不变
