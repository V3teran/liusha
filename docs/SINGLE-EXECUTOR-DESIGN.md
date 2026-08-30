# 简化为单一通用Executor的设计方案

**分析日期**: 2026-08-30  
**核心理念**: LLM都懂，不需要画蛇添足

---

## 🎯 设计理念

### 核心洞察

**你说得对**：
- ✅ **LLM本身就具备全领域知识**（Web、二进制、云、内网）
- ✅ **配置多个领域Executor是画蛇添足**
- ✅ **只需要一个通用Executor + 完整工具集**
- ✅ **LLM会根据任务自动选择合适的方法**

**对比**：
```
旧设计（画蛇添足）：
- Web渗透专家（System Prompt: 你是Web专家...）
- 二进制分析专家（System Prompt: 你是二进制专家...）
- 云安全专家（System Prompt: 你是云专家...）
- 内网横移专家（System Prompt: 你是横移专家...）
→ LLM本来就懂这些，何必分开？

新设计（简洁）：
- Executor（System Prompt: 你是渗透测试专家...）
- 提供完整工具集
- LLM自己判断用什么方法
```

---

## 📊 Agent表简化设计

### 方案：只保留两个Agent

**最终Agent表**：
```sql
CREATE TABLE agent (
    id            uuid PRIMARY KEY,
    code          text NOT NULL UNIQUE,
    kind          text NOT NULL CHECK (kind IN ('planner','executor')),
    name          text NOT NULL,
    description   text NOT NULL,
    system_prompt text NOT NULL,
    skills        jsonb NOT NULL DEFAULT '[]',
    function_tools jsonb NOT NULL DEFAULT '[]',
    cli_tools     jsonb NOT NULL DEFAULT '[]',
    max_iterations int NOT NULL DEFAULT 40,
    complexity    text NOT NULL DEFAULT 'medium',
    is_builtin    boolean NOT NULL DEFAULT false,  -- 内置Agent不可删除
    enabled       boolean NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- 添加约束：每种kind只能有一个enabled的Agent
CREATE UNIQUE INDEX idx_agent_kind_enabled ON agent (kind) WHERE enabled = true;
```

**种子数据（只有2条）**：
```sql
-- 1. Planner（规划者）
INSERT INTO agent (code, kind, name, description, system_prompt, is_builtin, enabled)
VALUES (
    'planner',
    'planner',
    '规划者',
    '负责全局规划、任务分解、策略制定',
    '你是一个渗透测试规划者...',
    true,
    true
);

-- 2. Executor（执行者）
INSERT INTO agent (code, kind, name, description, system_prompt, skills, is_builtin, enabled)
VALUES (
    'executor',
    'executor',
    '执行者',
    '负责执行具体的渗透测试任务',
    '你是一个渗透测试专家，具备全面的安全测试能力。
你精通：
- Web应用安全测试（SQL注入、XSS、CSRF等）
- 二进制程序分析（逆向、溢出、提权）
- 云环境渗透（AWS、Azure、K8s）
- 内网横向移动（域渗透、权限提升）
根据任务自动选择合适的方法和工具。',
    '["playwright-cli", "api-recon"]',  -- 所有skills
    true,
    true
);
```

**关键点**：
- ✅ **只有2个Agent**（Planner + Executor）
- ✅ **is_builtin=true**（内置，不可删除）
- ✅ **UNIQUE INDEX保证每种kind只有1个enabled**

---

## 🎯 代码简化

### 删除多Executor逻辑

**之前的复杂逻辑**：
```go
// 获取所有enabled的Executors
executors, err := h.cfgStore.EnabledDomainExecutors(ctx)

// 构建Prompt，列出所有Executor
if len(subAgents) > 0 {
    b.WriteString("\n\n## 可用专项代理\n")
    for _, a := range subAgents {
        b.WriteString(fmt.Sprintf("- **%s**: %s\n", a.Name, a.Description))
    }
}
```

**简化后**：
```go
// 获取唯一的Executor
executor, err := h.cfgStore.GetExecutor(ctx)
if err != nil {
    return fmt.Errorf("获取Executor失败: %w", err)
}

// 直接使用，不需要列表
```

**Store接口简化**：
```go
// 之前
EnabledDomainExecutors(ctx) ([]Agent, error)  // 返回多个

// 之后
GetPlanner(ctx) (*Agent, error)   // 返回单个Planner
GetExecutor(ctx) (*Agent, error)  // 返回单个Executor
```

---

## 🎨 前端UI简化

### 从"智能体管理"变成"配置"

**旧UI设计**（复杂）：
```
智能体
├── 默认规划者
├── Web渗透专家
├── 二进制分析专家
├── 云安全专家
└── 内网横移专家
  [+ 添加智能体]
```

**新UI设计**（简洁）：
```
系统配置 > Agent配置

规划者（Planner）
┌─────────────────────────────────┐
│ System Prompt:                  │
│ 你是一个渗透测试规划者...        │
│                                 │
│ [编辑]                          │
└─────────────────────────────────┘

执行者（Executor）
┌─────────────────────────────────┐
│ System Prompt:                  │
│ 你是一个渗透测试专家...          │
│                                 │
│ Skills: [playwright-cli] [+]    │
│ Tools: [http_request] [+]       │
│                                 │
│ [编辑]                          │
└─────────────────────────────────┘
```

**UI特点**：
- ✅ **固定2个Agent**（Planner + Executor）
- ✅ **不能添加/删除**（is_builtin）
- ✅ **只能编辑Prompt和工具**
- ✅ **简洁直观**

**或者更简单，放到系统设置中**：
```
系统设置
├── LLM配置
├── 代理配置
│   ├── Planner System Prompt
│   └── Executor System Prompt
├── 工具配置
└── ...
```

---

## 🎯 为什么这个设计更好？

### 1. 符合LLM的本质

**LLM的特性**：
- ✅ **通用性**：GPT-4、Claude等大模型本身就是全能的
- ✅ **上下文理解**：LLM会根据任务自动判断方法
- ✅ **无需角色扮演**：不需要"你是Web专家"这种提示

**例子**：
```
任务："测试example.com的SQL注入"

旧设计：
Planner → 选择"Web渗透专家" → 执行

新设计：
Planner → Executor（自动用SQL注入方法）→ 执行

结果一样！多一层选择是多余的！
```

---

### 2. 减少配置复杂度

**用户视角**：
```
旧设计：
1. 配置Web渗透专家的Prompt
2. 配置二进制分析专家的Prompt
3. 配置云安全专家的Prompt
4. 配置内网横移专家的Prompt
→ 4个Prompt都差不多，只是换了个名字

新设计：
1. 配置Executor的Prompt
→ 一个通用Prompt搞定
```

---

### 3. 避免选择困难

**Planner的困境**：
```
旧设计：
Planner: "这个任务既涉及Web，又涉及二进制，我该选哪个？"
→ 选择焦虑

新设计：
Planner: "直接派给Executor执行"
→ Executor自己判断用什么方法
```

---

### 4. 更接近ARTEX的设计

**ARTEX的成功经验**：
- ✅ Worker是通用的
- ✅ 只配置数量，不配置类型
- ✅ 简单高效

**Liusha新设计**：
- ✅ Executor是通用的
- ✅ 只有1个Executor
- ✅ 简单高效

---

## 🚫 删除的概念

### 不再需要的东西

1. **kind='domain'** → 删除
2. **EnabledDomainExecutors()** → 删除
3. **多Executor选择逻辑** → 删除
4. **"可用专项代理"列表** → 删除
5. **Agent管理UI** → 简化为配置页

---

## ✅ 数据库迁移

### 迁移脚本

```sql
-- 0127: 简化为单一通用Executor

-- 1. 删除旧的多个Executor
DELETE FROM agent WHERE kind = 'executor';

-- 2. 添加is_builtin字段
ALTER TABLE agent ADD COLUMN IF NOT EXISTS is_builtin boolean NOT NULL DEFAULT false;

-- 3. 添加唯一约束（每种kind只能有1个enabled）
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_kind_enabled 
ON agent (kind) WHERE enabled = true;

-- 4. 插入新的Planner和Executor
INSERT INTO agent (code, kind, name, description, system_prompt, is_builtin, enabled)
VALUES 
    ('planner', 'planner', '规划者', '负责全局规划', 
     '你是一个渗透测试规划者...', true, true),
    ('executor', 'executor', '执行者', '负责执行渗透测试', 
     '你是一个渗透测试专家，精通Web安全、二进制分析、云环境、内网横移等全方位能力...', 
     true, true)
ON CONFLICT (code) DO UPDATE SET
    system_prompt = EXCLUDED.system_prompt,
    is_builtin = EXCLUDED.is_builtin,
    enabled = EXCLUDED.enabled;

-- 5. 删除Scenario表（已在之前的迁移中）
DROP TABLE IF EXISTS scenario CASCADE;
```

---

## 🎯 代码改动

### Store接口简化

```go
// internal/config/agent/store.go

// 删除
func (s *Store) EnabledDomainExecutors(ctx context.Context) ([]Agent, error)

// 改为
func (s *Store) GetPlanner(ctx context.Context) (*Agent, error) {
    return s.getByKind(ctx, KindPlanner)
}

func (s *Store) GetExecutor(ctx context.Context) (*Agent, error) {
    return s.getByKind(ctx, KindExecutor)
}

func (s *Store) getByKind(ctx context.Context, kind Kind) (*Agent, error) {
    query := `
        SELECT id, code, kind, name, description, system_prompt, 
               skills, function_tools, cli_tools, max_iterations, 
               complexity, is_builtin, enabled
        FROM agent 
        WHERE kind = $1 AND enabled = true
        LIMIT 1
    `
    // ...
}
```

### Runner简化

```go
// cmd/runner/handler_run.go

// 删除
executors, err := h.cfgStore.EnabledDomainExecutors(ctx)
if len(executors) == 0 {
    return h.failTask(...)
}

// 改为
executor, err := h.cfgStore.GetExecutor(ctx)
if err != nil {
    return h.failTask(ctx, p.ExecutorID, 
        fmt.Errorf("获取Executor失败: %w", err))
}

// 删除多Executor的Prompt构建逻辑
// 直接使用executor.SystemPrompt
```

---

## 📊 最终架构

### 简化后的架构

```
┌─────────────────────────────────────┐
│  Agent表（只有2条记录）              │
├─────────────────────────────────────┤
│  code      kind      name           │
│  planner   planner   规划者         │
│  executor  executor  执行者         │
└─────────────────────────────────────┘

运行时：
runCognition()
    ↓
Planner（读取agent表的planner记录）
    ↓
生成Action → 写入WorldModel
    ↓
ExecutionLoop
    ↓
Executor（读取agent表的executor记录）
    ↓
执行Action
```

**特点**：
- ✅ **极简**（只有2个Agent）
- ✅ **固定**（不能添加/删除）
- ✅ **灵活**（可以编辑Prompt和工具）

---

## 🎯 总结

### 设计优势

1. **简洁性**
   - 只有2个Agent（Planner + Executor）
   - 不需要管理多个Executor
   - 配置简单

2. **正确性**
   - 符合LLM的通用性
   - 不需要"领域专家"的角色扮演
   - LLM自己会判断方法

3. **易用性**
   - 开箱即用
   - 不需要配置
   - 用户只需编辑Prompt（可选）

4. **可维护性**
   - 代码简单
   - 没有复杂的选择逻辑
   - 没有多Executor管理

### 与旧设计对比

| 维度 | 旧设计（多Executor） | 新设计（单Executor） |
|------|-------------------|-------------------|
| Agent数量 | 5+ (Planner + 4个Executor) | 2 (Planner + Executor) |
| 配置复杂度 | 高（每个Executor都要配置） | 低（只配置2个） |
| 用户理解成本 | 高（需要理解领域划分） | 低（直观） |
| 代码复杂度 | 高（选择逻辑） | 低（直接使用） |
| 符合LLM特性 | ❌ 人为限制 | ✅ 充分利用通用性 |

---

## ✅ 最终方案

**Agent表**：
- ✅ 只保留2个Agent（Planner + Executor）
- ✅ is_builtin=true（不可删除）
- ✅ 通用Executor，LLM自己判断方法

**前端UI**：
- ✅ 不叫"智能体管理"
- ✅ 放在"系统设置 > Agent配置"
- ✅ 只能编辑Prompt和工具，不能添加/删除

**代码**：
- ✅ 删除多Executor逻辑
- ✅ GetPlanner() + GetExecutor()
- ✅ 简化Prompt构建

---

**完成时间**: 2026-08-30  
**核心理念**: LLM都懂，不需要画蛇添足！
