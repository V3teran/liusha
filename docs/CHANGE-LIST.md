# 简化为单一Executor的完整变更清单

**核心变更**: Agent表只保留2条记录（Planner + Executor）

---

## 📋 变更清单

### 1. 数据库变更

#### 1.1 Agent表改动
```sql
-- 添加is_builtin字段
ALTER TABLE agent ADD COLUMN is_builtin boolean NOT NULL DEFAULT false;

-- 重命名body为system_prompt
ALTER TABLE agent RENAME COLUMN body TO system_prompt;

-- 添加skills字段
ALTER TABLE agent ADD COLUMN skills jsonb NOT NULL DEFAULT '[]';

-- 添加唯一约束（每种kind只能有1个enabled）
CREATE UNIQUE INDEX idx_agent_kind_enabled ON agent (kind) WHERE enabled = true;

-- 删除旧的多个Executor
DELETE FROM agent WHERE kind = 'executor';

-- 插入新的2个Agent
INSERT INTO agent (code, kind, name, description, system_prompt, skills, is_builtin, enabled)
VALUES 
    ('planner', 'planner', '规划者', '负责全局规划和任务分解', 
     '你是一个渗透测试规划者...', '[]', true, true),
    ('executor', 'executor', '执行者', '负责执行具体的渗透测试任务',
     '你是一个渗透测试专家，精通Web安全、二进制分析、云环境、内网横移等全方位能力。根据任务自动选择合适的方法和工具。', 
     '["playwright-cli", "api-recon"]', true, true);
```

#### 1.2 删除Scenario表
```sql
DROP TABLE scenario CASCADE;
```

---

### 2. 后端代码变更

#### 2.1 Store接口变更

**文件**: `internal/config/agent/store.go`

**删除**:
```go
func (s *Store) EnabledDomainExecutors(ctx context.Context) ([]Agent, error)
```

**新增**:
```go
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
    var a Agent
    err := s.db.QueryRow(ctx, query, kind).Scan(
        &a.ID, &a.Code, &a.Kind, &a.Name, &a.Description, 
        &a.SystemPrompt, &a.Skills, &a.FunctionTools, &a.CliTools,
        &a.MaxIterations, &a.Complexity, &a.IsBuiltin, &a.Enabled,
    )
    if err != nil {
        return nil, err
    }
    return &a, nil
}
```

#### 2.2 Agent模型变更

**文件**: `internal/config/agent/model.go`

**修改**:
```go
type Agent struct {
    ID            string
    Code          string
    Kind          Kind
    Name          string
    Description   string
    SystemPrompt  string      // 原Body字段重命名
    Skills        []string    // 新增
    FunctionTools []string
    CliTools      []string
    MaxIterations int
    Enabled       bool
    Complexity    string
    IsBuiltin     bool        // 新增
    CreatedAt     time.Time
    UpdatedAt     time.Time
}
```

#### 2.3 Runner处理逻辑变更

**文件**: `cmd/runner/handler_run.go`

**删除**:
```go
func (h handler) handleSwarm(..., executors []cfgagent.Agent, ...) error {
    if len(executors) == 0 {
        return h.failTask(ctx, p.ExecutorID, fmt.Errorf("swarm 引擎无 enabled 领域操作员"))
    }
    // ...
    // 构建多Executor的Prompt
    if len(subAgents) > 0 {
        b.WriteString("\n\n## 可用专项代理\n")
        for _, a := range subAgents {
            b.WriteString(fmt.Sprintf("- **%s**: %s\n", a.Name, a.Description))
        }
    }
}
```

**改为**:
```go
func (h handler) handleSwarm(...) error {
    // 获取单个Executor
    executor, err := h.cfgStore.GetExecutor(ctx)
    if err != nil {
        return h.failTask(ctx, p.ExecutorID, fmt.Errorf("获取Executor失败: %w", err))
    }
    
    // 直接使用executor.SystemPrompt，不需要构建列表
    // ...
}
```

**文件**: `cmd/runner/handler.go`

**删除**:
```go
executors, err := h.cfgStore.EnabledDomainExecutors(ctx)
return h.handleSwarm(ctx, p, scen, executors, input.Brief)
```

**改为**:
```go
return h.handleSwarm(ctx, p, scen, input.Brief)
```

#### 2.4 删除Prompt构建逻辑

**文件**: `cmd/runner/handler_run.go`

**删除函数**:
```go
func swarmSystemPrompt(orchBody, scenInstruction string, subAgents []Agent) string
```

**简化为直接使用**:
```go
systemPrompt := planner.SystemPrompt + "\n\n" + executor.SystemPrompt
```

---

### 3. 前端变更

#### 3.1 页面命名

**独立页面，就叫"智能体"（3个字）**

```
侧边栏导航：
├── 首页
├── 任务
├── 智能体 ← 这里
├── 发现
└── ...
```

#### 3.2 页面布局

**文件**: `web/src/pages/AgentsPage.tsx`

```tsx
// 智能体页面（固定2个Agent）
export function AgentsPage() {
  return (
    <div>
      <h1>智能体</h1>
      
      {/* Planner卡片 */}
      <AgentCard 
        agent={planner}
        isBuiltin={true}
        canDelete={false}
      />
      
      {/* Executor卡片 */}
      <AgentCard 
        agent={executor}
        isBuiltin={true}
        canDelete={false}
      />
    </div>
  );
}
```

**UI展示**:
```
┌────────────────────────────────────────────┐
│ 智能体                                      │
├────────────────────────────────────────────┤
│                                            │
│ ┌─────────────────────────────────────┐   │
│ │ [内置] 规划者 (Planner)             │   │
│ │                                     │   │
│ │ 负责全局规划和任务分解               │   │
│ │                                     │   │
│ │ [查看详情] [编辑Prompt]             │   │
│ └─────────────────────────────────────┘   │
│                                            │
│ ┌─────────────────────────────────────┐   │
│ │ [内置] 执行者 (Executor)            │   │
│ │                                     │   │
│ │ 负责执行具体的渗透测试任务           │   │
│ │                                     │   │
│ │ Skills: [playwright-cli] [api-recon]│   │
│ │                                     │   │
│ │ [查看详情] [编辑Prompt] [编辑Skills]│   │
│ └─────────────────────────────────────┘   │
└────────────────────────────────────────────┘
```

**特点**:
- ✅ 固定2个Agent卡片
- ✅ 标记[内置]
- ✅ 不能删除（没有删除按钮）
- ✅ 可以编辑Prompt和Skills

#### 3.3 API调用变更

**文件**: `web/src/api/client.ts`

**删除**:
```typescript
// 删除创建/删除Agent的API
export function createAgent(data: AgentData): Promise<Agent>
export function deleteAgent(id: string): Promise<void>
```

**保留/修改**:
```typescript
// 只保留列表和更新
export function listAgents(): Promise<Agent[]>  // 始终返回2个
export function updateAgent(id: string, data: Partial<AgentData>): Promise<Agent>
```

#### 3.4 类型定义变更

**文件**: `web/src/api/types.ts`

```typescript
export interface Agent {
  id: string;
  code: string;
  kind: 'planner' | 'executor';
  name: string;
  description: string;
  systemPrompt: string;     // 原body字段
  skills: string[];         // 新增
  functionTools: string[];
  cliTools: string[];
  maxIterations: number;
  complexity: string;
  isBuiltin: boolean;       // 新增
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}
```

---

### 4. 删除的文件/代码

#### 4.1 删除Scenario相关

**后端**:
- `internal/config/scenario/` 整个包
- `db/migrations/*scenario*.sql` 相关迁移

**前端**:
- `web/src/pages/ScenarioAdmin.tsx`
- `web/src/api/scenarios.ts`
- 路由中的scenario相关路由

#### 4.2 删除多Executor逻辑

**后端**:
- `EnabledDomainExecutors()` 方法
- `swarmSystemPrompt()` 函数中的"可用专项代理"构建逻辑

---

### 5. 数据库迁移文件

**文件**: `db/migrations/0126_simplify_to_single_executor.up.sql`

```sql
-- 0126: 简化为单一通用Executor

-- 1. 添加新字段
ALTER TABLE agent ADD COLUMN IF NOT EXISTS is_builtin boolean NOT NULL DEFAULT false;
ALTER TABLE agent ADD COLUMN IF NOT EXISTS skills jsonb NOT NULL DEFAULT '[]';

-- 2. 重命名body字段
ALTER TABLE agent RENAME COLUMN body TO system_prompt;

-- 3. 添加唯一约束
CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_kind_enabled 
ON agent (kind) WHERE enabled = true;

-- 4. 删除旧的Executor
DELETE FROM agent WHERE kind = 'executor';

-- 5. 插入新的Agent（固定2个）
INSERT INTO agent (code, kind, name, description, system_prompt, skills, is_builtin, enabled)
VALUES 
    ('planner', 'planner', '规划者', '负责全局规划和任务分解', 
     '你是一个渗透测试规划者，负责分析目标、制定策略、分解任务。你会评估世界模型中的已知信息，判断下一步应该做什么，并生成具体的执行动作。', 
     '[]', true, true),
    ('executor', 'executor', '执行者', '负责执行具体的渗透测试任务',
     '你是一个渗透测试专家，具备全面的安全测试能力。你精通：
- Web应用安全测试（SQL注入、XSS、CSRF、文件上传等）
- 二进制程序分析（逆向工程、缓冲区溢出、格式化字符串等）
- 云环境渗透（AWS、Azure、K8s配置错误等）
- 内网横向移动（域渗透、权限提升、凭据窃取等）
根据任务自动选择合适的方法和工具，高效完成渗透测试。', 
     '["playwright-cli", "api-recon"]', true, true)
ON CONFLICT (code) DO UPDATE SET
    system_prompt = EXCLUDED.system_prompt,
    skills = EXCLUDED.skills,
    is_builtin = EXCLUDED.is_builtin,
    enabled = EXCLUDED.enabled;

-- 6. 删除Scenario表
DROP TABLE IF EXISTS scenario CASCADE;
```

**文件**: `db/migrations/0126_simplify_to_single_executor.down.sql`

```sql
-- 回滚脚本
DROP INDEX IF EXISTS idx_agent_kind_enabled;
ALTER TABLE agent RENAME COLUMN system_prompt TO body;
ALTER TABLE agent DROP COLUMN IF EXISTS skills;
ALTER TABLE agent DROP COLUMN IF EXISTS is_builtin;
-- 注意：无法完全回滚（旧的多Executor数据已丢失）
```

---

## 📊 变更总结

### 数据库
1. ✅ Agent表添加 `is_builtin`, `skills` 字段
2. ✅ Agent表重命名 `body` → `system_prompt`
3. ✅ 添加唯一约束 `idx_agent_kind_enabled`
4. ✅ 删除Scenario表
5. ✅ Agent表固定2条记录（Planner + Executor）

### 后端代码
1. ✅ 删除 `EnabledDomainExecutors()`
2. ✅ 新增 `GetPlanner()`, `GetExecutor()`
3. ✅ 删除多Executor的Prompt构建逻辑
4. ✅ 简化 `handleSwarm()` 函数
5. ✅ 删除 `internal/config/scenario/` 包

### 前端代码
1. ✅ 页面命名：**"智能体"**（独立页面，3个字）
2. ✅ 固定显示2个Agent卡片（Planner + Executor）
3. ✅ 标记[内置]，不能删除
4. ✅ 可以编辑Prompt和Skills
5. ✅ 删除创建/删除Agent的功能
6. ✅ Agent类型添加 `systemPrompt`, `skills`, `isBuiltin` 字段

---

**变更完成！前端就叫"智能体"！** 🎊
