# Agent表完整字段定义（补充tools）

## 📊 Agent表完整结构

```sql
CREATE TABLE agent (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code            text NOT NULL UNIQUE,
    kind            text NOT NULL CHECK (kind IN ('planner','executor')),
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    
    -- Prompt配置
    system_prompt   text NOT NULL,  -- 原body字段重命名
    
    -- 工具配置（三类）
    skills          jsonb NOT NULL DEFAULT '[]',    -- Skill ID列表（如["playwright-cli"]）
    function_tools  jsonb NOT NULL DEFAULT '[]',    -- 内置function工具（如["http_request"]）
    cli_tools       jsonb NOT NULL DEFAULT '[]',    -- 外部CLI工具（如["curl", "sqlmap"]）
    
    -- 执行配置
    max_iterations  int NOT NULL DEFAULT 40,
    complexity      text NOT NULL DEFAULT 'medium',
    
    -- 元数据
    is_builtin      boolean NOT NULL DEFAULT false,  -- 内置Agent不可删除
    enabled         boolean NOT NULL DEFAULT true,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- 唯一约束：每种kind只能有1个enabled
CREATE UNIQUE INDEX idx_agent_kind_enabled ON agent (kind) WHERE enabled = true;
```

## 🎯 三类工具说明

### 1. skills（技能）
- **类型**: Skill ID列表
- **示例**: `["playwright-cli", "api-recon"]`
- **说明**: Skill是工具包的组合，每个Skill包含多个工具

### 2. function_tools（函数工具）
- **类型**: 内置function工具名称列表
- **示例**: `["http_request", "parse_html", "extract_links"]`
- **说明**: LLM可以直接调用的function calling工具

### 3. cli_tools（CLI工具）
- **类型**: 外部CLI工具名称列表
- **示例**: `["curl", "sqlmap", "nikto", "nmap"]`
- **说明**: 通过命令行调用的外部工具

## 📋 完整变更清单（更新）

### 数据库变更

**Agent表字段变更**:
```sql
-- 1. 重命名
ALTER TABLE agent RENAME COLUMN body TO system_prompt;

-- 2. 新增字段
ALTER TABLE agent ADD COLUMN skills jsonb NOT NULL DEFAULT '[]';
ALTER TABLE agent ADD COLUMN is_builtin boolean NOT NULL DEFAULT false;

-- 3. 已存在的字段（确认存在）
-- function_tools jsonb  -- 应该已存在
-- cli_tools jsonb       -- 应该已存在

-- 4. 添加约束
CREATE UNIQUE INDEX idx_agent_kind_enabled ON agent (kind) WHERE enabled = true;
```

**种子数据**:
```sql
INSERT INTO agent (
    code, kind, name, description, system_prompt, 
    skills, function_tools, cli_tools, 
    is_builtin, enabled
)
VALUES 
    (
        'planner', 
        'planner', 
        '规划者', 
        '负责全局规划和任务分解',
        '你是一个渗透测试规划者...',
        '[]',  -- skills
        '[]',  -- function_tools（Planner通常不需要工具）
        '[]',  -- cli_tools
        true,  -- is_builtin
        true   -- enabled
    ),
    (
        'executor',
        'executor',
        '执行者',
        '负责执行具体的渗透测试任务',
        '你是一个渗透测试专家，精通Web安全、二进制分析、云环境、内网横移等...',
        '["playwright-cli", "api-recon"]',  -- skills
        '["http_request", "parse_html", "execute_js"]',  -- function_tools
        '["curl", "sqlmap", "nikto", "nmap", "gobuster"]',  -- cli_tools
        true,  -- is_builtin
        true   -- enabled
    );
```

## 🎨 前端UI展示

```tsx
// Executor卡片
<AgentCard agent={executor}>
  <div>
    <h3>执行者 (Executor)</h3>
    <p>{executor.description}</p>
    
    {/* System Prompt */}
    <div>
      <label>System Prompt</label>
      <textarea value={executor.systemPrompt} />
    </div>
    
    {/* Skills */}
    <div>
      <label>Skills</label>
      <div>
        {executor.skills.map(skill => (
          <Tag key={skill}>{skill}</Tag>
        ))}
        <Button>+ 添加Skill</Button>
      </div>
    </div>
    
    {/* Function Tools */}
    <div>
      <label>Function Tools</label>
      <div>
        {executor.functionTools.map(tool => (
          <Tag key={tool}>{tool}</Tag>
        ))}
        <Button>+ 添加工具</Button>
      </div>
    </div>
    
    {/* CLI Tools */}
    <div>
      <label>CLI Tools</label>
      <div>
        {executor.cliTools.map(tool => (
          <Tag key={tool}>{tool}</Tag>
        ))}
        <Button>+ 添加工具</Button>
      </div>
    </div>
    
    <Button>保存</Button>
  </div>
</AgentCard>
```

## 🎯 Go模型定义

```go
// internal/config/agent/model.go
type Agent struct {
    ID            string
    Code          string
    Kind          Kind      // planner | executor
    Name          string
    Description   string
    
    // Prompt
    SystemPrompt  string    // 原Body字段
    
    // 工具（三类）
    Skills        []string  // Skill ID列表
    FunctionTools []string  // 内置function工具
    CliTools      []string  // 外部CLI工具
    
    // 配置
    MaxIterations int
    Complexity    string
    IsBuiltin     bool      // 新增
    Enabled       bool
    
    CreatedAt     time.Time
    UpdatedAt     time.Time
}
```

## ✅ 完整字段清单

| 字段 | 类型 | 说明 | 变更 |
|------|------|------|------|
| id | uuid | 主键 | - |
| code | text | 唯一标识 | - |
| kind | text | planner/executor | - |
| name | text | 名称 | - |
| description | text | 描述 | - |
| **system_prompt** | text | System Prompt | ✅ 重命名（原body） |
| **skills** | jsonb | Skill ID列表 | ✅ 新增 |
| function_tools | jsonb | Function工具 | ✅ 已存在（确认） |
| cli_tools | jsonb | CLI工具 | ✅ 已存在（确认） |
| max_iterations | int | 最大迭代次数 | - |
| complexity | text | 复杂度 | - |
| **is_builtin** | boolean | 内置标记 | ✅ 新增 |
| enabled | boolean | 启用状态 | - |
| created_at | timestamptz | 创建时间 | - |
| updated_at | timestamptz | 更新时间 | - |

---

**感谢指出！现在字段完整了！** 🎊
