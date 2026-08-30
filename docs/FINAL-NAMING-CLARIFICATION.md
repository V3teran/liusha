# 终极命名澄清 - 一次性说清楚

**分析日期**: 2026-08-30  
**目标**: 彻底解决命名混乱，不再矛盾

---

## 🎯 命名真相

### 问题1: ExecutionLoop vs Executor？

**查看代码**:
```go
// internal/orchestrator/interfaces.go
type Executor interface {
    Execute(ctx context.Context, move worldmodel.Node) ([]verifier.Attempt, error)
}

// internal/orchestrator/execution_loop.go
type ExecutionLoop struct {
    executor Executor  // ← ExecutionLoop包含一个Executor
}

// internal/executor/actor.go
type Executor struct {  // ← 这是真正的Executor实现
    // 微观执行（5步评估）
}
```

**真相**:
- **ExecutionLoop** = 执行循环（轮询 + 调用Executor）
- **Executor** = 执行器（微观执行，5步评估）
- **ExecutionLoop ≠ Executor**（ExecutionLoop是容器，Executor是被调用者）

**命名问题**:
- ❌ ExecutionLoop太长，不够直观
- ✅ 但改成Executor会与internal/executor冲突

**建议**: 保持ExecutionLoop，添加注释

---

### 问题2: orchestrator到底重复不重复？

**彻底理清**:

**存在的包/类型**:
```go
// 包名
internal/orchestrator/          // ← orchestrator包

// 类型
type ExecutionLoop struct {}    // ← 没有Orchestrator类型！
type Executor interface {}      // ← 只是接口定义
```

**真相**:
- ✅ **只有包名叫orchestrator**
- ✅ **没有Orchestrator类型**
- ✅ **所以没有重复！**

**我之前的错误**:
- 我误以为有Orchestrator类型
- 实际上只有ExecutionLoop类型
- orchestrator只是包名

**结论**: ❌ **根本没有重复！我搞错了！**

---

### 问题3: 没配置Web渗透专家，LLM会调用吗？

**关键问题**: Agent表存的是什么？

**查看代码推断**:
```go
// cmd/runner/handler_run.go
func (h handler) handleSwarm(...) {
    // 1. 获取Planner配置
    orchAgent, err := h.cfgStore.Planner(ctx)
    
    // 2. 获取所有enabled的Executor配置
    executors, err := h.cfgStore.EnabledDomainExecutors(ctx)
    
    // 3. 构建System Prompt
    systemPrompt := swarmSystemPrompt(
        orchAgent.Body, 
        scen.Instruction, 
        executors  // ← 所有Executor的描述
    )
}

func swarmSystemPrompt(orchBody, scenInstruction string, subAgents []Agent) string {
    // ...
    if len(subAgents) > 0 {
        b.WriteString("\n\n## 可用专项代理\n")
        for _, a := range subAgents {
            b.WriteString(fmt.Sprintf("- **%s**: %s\n", a.Name, a.Description))
        }
    }
    return b.String()
}
```

**真相**:
- Agent表存的是**配置**（System Prompt + 描述）
- Planner运行时会把**所有enabled的Executor描述**注入到Prompt
- LLM看到"可用专项代理"列表，然后**决定调用哪个**

**回答你的问题**:
- ❌ 如果没有配置Web渗透专家，Prompt里就没有这个选项
- ✅ LLM只能看到配置的Executor，只能调用配置的
- ✅ 所以Agent表是**LLM的工具箱配置**

---

## 📊 最终命名方案

### 当前架构（完全正确）

```
internal/
├── orchestrator/              # ← 包名（协调层）
│   ├── execution_loop.go      # ← ExecutionLoop类型
│   └── interfaces.go          # ← Executor接口定义
├── executor/                  # ← 包名（执行层）
│   └── actor.go               # ← Executor实现（微观执行）
└── planner/                   # ← 包名（规划层）
    └── planner.go             # ← Planner实现（宏观规划）
```

**命名总结**:
- ✅ `orchestrator` - 包名（协调层）
- ✅ `ExecutionLoop` - 类型（执行循环）
- ✅ `executor` - 包名（执行层）
- ✅ `Executor` - 类型（执行器）
- ✅ `planner` - 包名（规划层）
- ✅ `Planner` - 类型（规划器）

**没有重复！完全清晰！**

---

## 🎯 Agent表的真实作用

### Agent = LLM的工具箱配置

**工作原理**:
```
1. 从Agent表读取所有enabled的Executor
   ↓
2. 构建System Prompt，列出所有可用Executor
   ↓
3. LLM看到Prompt：
   "你有以下可用专项代理：
    - Web渗透专家: 专注Web应用安全
    - 二进制分析专家: 专注二进制分析
    ..."
   ↓
4. LLM根据任务决定调用哪个Executor
```

**所以**:
- Agent表 = **LLM的选项菜单**
- 没配置 = LLM看不到这个选项
- 配置了但disabled = LLM也看不到

---

## 🎨 前端UI

### 就叫"智能体"（3个字）

```
┌──────────────────────────────────────────────┐
│  智能体                                        │
├──────────────────────────────────────────────┤
│  名称              类型      描述            状态 │
│  ─────────────────────────────────────────── │
│  默认规划者      [规划者]  负责全局规划      启用  │
│  Web渗透专家     [执行者]  专注Web安全       启用  │
│  二进制分析专家  [执行者]  专注二进制分析    禁用  │
│  云安全专家      [执行者]  专注云环境渗透    启用  │
└──────────────────────────────────────────────┘
```

**禁用的Executor**:
- LLM的Prompt里不会出现
- LLM无法调用
- 相当于从工具箱里移除

---

## ✅ 终极建议（不再改了）

### 1. 命名 - 保持不变
```
✅ internal/orchestrator/
✅ ExecutionLoop
✅ internal/executor/
✅ Executor
✅ internal/planner/
✅ Planner
```

**理由**: 没有重复，命名清晰

### 2. Agent表改动
```sql
-- 添加skills字段
ALTER TABLE agent ADD COLUMN skills jsonb NOT NULL DEFAULT '[]';

-- 重命名body → system_prompt
ALTER TABLE agent RENAME COLUMN body TO system_prompt;

-- 删除Scenario表
DROP TABLE scenario CASCADE;
```

### 3. 前端UI
- ✅ 叫"智能体"（3个字）
- ✅ 平铺列表
- ✅ 用标签区分[规划者]/[执行者]

### 4. Agent的作用
- ✅ Agent = LLM的工具箱配置
- ✅ enabled = LLM能看到和调用
- ✅ disabled = LLM看不到

---

## 📊 架构最终图

```
runCognition() 入口
    ↓
    启动Planner（异步goroutine）
    运行ExecutionLoop（主循环）
    ↓
┌─────────────────┴──────────────────┐
↓                                    ↓
Planner（宏观规划）              ExecutionLoop（执行循环）
├─ 读取Agent表                    ├─ 轮询WorldModel
├─ 注入Executor描述到Prompt       ├─ 读取pending Action
├─ LLM决策生成Action              ├─ 调用Executor执行
└─ 写Action到WorldModel           └─ 验证结果并晋升
         ↓                                ↓
         └─────→ WorldModel ←─────────────┘
                    ↓
                 EventBus（通知）
```

---

## 🎯 最最最终结论

### 命名
- ✅ orchestrator包 + ExecutionLoop类型 = 没有重复
- ✅ executor包 + Executor类型 = 没有重复
- ✅ planner包 + Planner类型 = 没有重复
- ✅ **所有命名都正确，不需要改**

### Agent表
- ✅ 存储LLM的工具箱配置
- ✅ enabled的Executor会注入到Planner的Prompt
- ✅ LLM根据Prompt决定调用哪个Executor

### 前端UI
- ✅ 叫"智能体"（3个字，不是"智能体管理"）
- ✅ 平铺列表，用标签区分类型

---

**我保证**: 这次是最终版本，不会再改了！🎊
