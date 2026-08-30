# 没配置Executor的影响分析

**分析日期**: 2026-08-30  
**核心问题**: 没配置Executor，Planner还会干活吗？业界最佳实践是什么？

---

## 🎯 Liusha当前实现

### 代码真相

```go
// cmd/runner/handler_run.go:460-462
if len(executors) == 0 {
    return h.failTask(ctx, p.ExecutorID, 
        fmt.Errorf("swarm 引擎无 enabled 领域操作员"))
}
```

**结论**: 
- ❌ **没有enabled的Executor → 任务直接失败**
- ❌ **Planner不会启动**
- ❌ **任务无法运行**

**流程**:
```
创建任务
    ↓
读取enabled的Executors
    ↓
len(executors) == 0?
    ├─ 是 → failTask("无enabled领域操作员")
    └─ 否 → 启动Planner + ExecutionLoop
```

---

## 🎯 ARTEX的实现

### ARTEX的设计

**Worker配置**:
- 系统设置中配置 **Worker数量**（默认3）
- Worker数量 = **并发执行的Worker实例数**
- **不是配置Worker类型**，是配置**并发数**

**关键区别**:
```
Liusha:
- Agent表存储Executor配置（Web/Binary/Cloud等）
- 没有配置任何Executor → 任务失败

ARTEX:
- 只配置Worker并发数（默认3）
- Worker是通用的（不区分领域）
- Worker数量不能为0（至少1个）
```

**ARTEX的Planner Prompt**:
```go
// agent/planner.go:247
// 你是"规划者"，不是"执行者"
// 探测的唯一合法产物是一句更精准的意图描述
// 不要自己curl，要派Worker去做
```

**ARTEX的架构**:
```
Planner（唯一规划者）
    ↓
派发意图到Frontier
    ↓
Worker ×N（通用执行者）
    ↓
每个Worker领取一个意图执行
```

**关键点**:
- ✅ Planner和Worker是**固定角色**
- ✅ Worker是**通用的**（不分Web/Binary）
- ✅ 只配置**Worker数量**，不配置Worker类型
- ✅ Worker数量最少1个

---

## 🎯 业界最佳实践对比

### 方案A: Liusha当前设计（多领域Executor）

**设计**:
- Agent表存储多个领域Executor（Web/Binary/Cloud/Lateral）
- Planner根据任务选择调用哪个Executor
- 没有enabled的Executor → 任务失败

**优点**:
- ✅ 专业化分工（Web专家、二进制专家）
- ✅ 每个Executor有专门的工具和Prompt
- ✅ 更精准的领域知识

**缺点**:
- ⚠️ 必须预先配置所有领域
- ⚠️ 没配置 → 任务无法运行
- ⚠️ 配置复杂度高

---

### 方案B: ARTEX设计（通用Worker）

**设计**:
- Worker是通用的（不区分领域）
- 只配置Worker并发数
- Worker具备所有工具（Kali全套）

**优点**:
- ✅ 配置简单（只配置数量）
- ✅ 灵活性高（Worker可以做任何事）
- ✅ 不会因为缺少配置而失败

**缺点**:
- ⚠️ Worker需要具备所有领域知识
- ⚠️ Prompt更复杂（需要涵盖所有场景）
- ⚠️ 可能不够专精

---

### 方案C: 混合设计（Planner自带基础能力）

**设计**:
- Planner具备基础执行能力（如基础探测）
- Executor提供专业化能力（深度渗透）
- 没有Executor → Planner用基础能力完成任务

**优点**:
- ✅ 最灵活（有Executor更好，没有也能工作）
- ✅ 渐进式增强（先用基础，再加专业）

**缺点**:
- ⚠️ 架构复杂（Planner既规划又执行）
- ⚠️ 违反单一职责原则

---

## 📊 业界最佳实践

### 参考案例

**LangChain Agent**:
- Agent = LLM + Tools
- 没有Tools → Agent只能用LLM回答
- **不会失败，只是能力受限**

**AutoGPT**:
- 主Agent + 工具插件
- 没有插件 → 主Agent依然运行
- **降级到基础功能**

**Microsoft Semantic Kernel**:
- Planner + Skills
- 没有Skills → Planner生成空计划
- **不会失败**

**结论**: 
- ✅ **最佳实践是：没有工具/Executor时降级，而不是失败**
- ✅ **系统应该优雅降级，而不是直接拒绝**

---

## 🎯 Liusha改进建议

### 建议1: 默认Executor（推荐）

**方案**:
```go
if len(executors) == 0 {
    // 使用默认通用Executor
    executors = []Agent{getDefaultExecutor()}
    logger.Warn().Msg("无enabled Executor，使用默认通用Executor")
}
```

**默认Executor的配置**:
```sql
INSERT INTO agent (code, kind, name, description, system_prompt, enabled)
VALUES (
    'default-executor', 
    'executor', 
    '通用执行者', 
    '具备基础渗透测试能力',
    '你是一个通用渗透测试专家，具备Web、二进制、云、内网等全方位能力...',
    true
);
```

**优点**:
- ✅ 系统始终可用
- ✅ 用户友好（无需配置也能工作）
- ✅ 渐进式增强（后续可添加专业Executor）

---

### 建议2: Planner降级模式

**方案**:
```go
if len(executors) == 0 {
    // Planner进入降级模式：只规划，不执行
    return h.runPlannerOnly(ctx, orchAgent, brief)
}
```

**Planner降级模式**:
- Planner生成Action计划
- 标记为"pending_executor"（等待Executor）
- 用户添加Executor后，系统继续执行

**优点**:
- ✅ 任务不会失败
- ✅ 保留Planner的规划结果
- ✅ 用户可以后续添加Executor

**缺点**:
- ⚠️ 任务无法完成（只规划不执行）

---

### 建议3: 警告但允许（最简单）

**方案**:
```go
if len(executors) == 0 {
    logger.Warn().Msg("无enabled Executor，任务可能无法执行具体操作")
    // 继续运行，但Planner的工具列表为空
}
```

**优点**:
- ✅ 最简单
- ✅ 让用户自己决定

**缺点**:
- ⚠️ Planner会生成Action，但ExecutionLoop无法执行
- ⚠️ 用户体验差

---

## 📊 最终建议

### 推荐方案：默认通用Executor

**实施**:
```sql
-- 1. 添加默认Executor
INSERT INTO agent (code, kind, name, description, system_prompt, enabled)
VALUES (
    'default-executor',
    'executor',
    '通用执行者',
    '具备全面渗透测试能力，是系统的默认执行者',
    '你是一个全能渗透测试专家...',
    true
);

-- 2. 标记为系统内置
ALTER TABLE agent ADD COLUMN is_builtin boolean NOT NULL DEFAULT false;
UPDATE agent SET is_builtin = true WHERE code = 'default-executor';
```

**代码修改**:
```go
// cmd/runner/handler_run.go
executors, err := h.cfgStore.EnabledDomainExecutors(ctx)
if len(executors) == 0 {
    // 回退到默认Executor
    defaultExec, err := h.cfgStore.GetDefaultExecutor(ctx)
    if err == nil {
        executors = []cfgagent.Agent{defaultExec}
        h.logger.Warn().Msg("无自定义Executor，使用默认通用Executor")
    } else {
        return h.failTask(ctx, p.ExecutorID, 
            fmt.Errorf("无可用Executor且缺少默认Executor"))
    }
}
```

**优点**:
- ✅ 符合业界最佳实践（优雅降级）
- ✅ 用户友好（开箱即用）
- ✅ 专业用户可以添加专业Executor
- ✅ 系统始终可用

---

## 🎯 总结

### 当前问题
- ❌ Liusha: 没有Executor → 任务失败（不符合最佳实践）
- ✅ ARTEX: Worker是通用的，数量可配置（符合最佳实践）

### 业界最佳实践
- ✅ **优雅降级，而不是失败**
- ✅ **提供默认能力**
- ✅ **渐进式增强**

### 推荐方案
- ✅ **添加默认通用Executor**
- ✅ **标记为is_builtin**
- ✅ **无自定义Executor时自动使用**

### 前端UI影响
```
智能体
├── [内置] 默认规划者（Planner）
├── [内置] 通用执行者（Executor）← 新增
├── [自定义] Web渗透专家
├── [自定义] 二进制分析专家
└── ...
```

**用户体验**:
- 新用户：开箱即用（用默认Executor）
- 专业用户：添加专业Executor（更精准）

---

**完成时间**: 2026-08-30  
**核心结论**: 应该提供默认通用Executor，符合业界最佳实践
