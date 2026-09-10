# Agent 配置管理系统设计方案

## 🎯 需求理解

### **核心需求：**
1. ✅ **前端可编辑 Agent 配置**（名字、提示词、工具、技能等）
2. ✅ **不提供新增 Agent**（固定三个：Planner/Executor/Evaluator）
3. ✅ **使用系统数据流**：内存 → Redis → PostgreSQL
4. ✅ **需要初始化种子文件**（默认配置）

### **从 Hunters 迁移：**
- ❌ 旧：4 个 markdown 文件（orchestrator/reconnaissance/exploitation/traffic-analysis）
- ✅ 新：3 个 Agent 配置（Planner/Executor/Evaluator）

---

## 📊 业界最佳实践参考

### **1. LangChain**
```python
# Agent 配置在代码 + 运行时可调整
agent = initialize_agent(
    tools=[...],
    llm=llm,
    agent=AgentType.ZERO_SHOT_REACT_DESCRIPTION,
    verbose=True
)
```

**模式：** 代码定义 + 运行时参数

---

### **2. AutoGPT**
```json
// ai_settings.yaml (种子文件)
{
  "ai_name": "Entrepreneur-GPT",
  "ai_role": "an AI designed to autonomously develop...",
  "ai_goals": [...]
}
```

**模式：** YAML/JSON 种子 + 可编辑

---

### **3. Kubernetes ConfigMap**
```yaml
# 种子配置
apiVersion: v1
kind: ConfigMap
metadata:
  name: agent-config
data:
  planner.yaml: |
    name: "Planner Agent"
    prompt: "..."
```

**模式：** 声明式配置 + 版本控制

---

### **4. Django Fixtures**
```json
[
  {
    "model": "agents.agent",
    "pk": 1,
    "fields": {
      "name": "Planner",
      "prompt": "..."
    }
  }
]
```

**模式：** JSON fixtures + 数据库迁移

---

## 🎯 推荐方案（结合业界最佳实践）

### **方案：Seed YAML + 数据库 + 前端编辑**

**核心思想：**
1. ✅ **Seed YAML**（版本控制，易读易写）
2. ✅ **数据库存储**（运行时状态）
3. ✅ **前端编辑**（用户自定义）
4. ✅ **重置机制**（恢复默认）

---

## 📐 数据模型设计

### **PostgreSQL Schema**

```sql
-- agents 表（3 条固定记录）
CREATE TABLE agents (
    id              VARCHAR(50) PRIMARY KEY,  -- 'planner', 'executor', 'evaluator'
    name            VARCHAR(100) NOT NULL,
    description     TEXT,
    system_prompt   TEXT NOT NULL,            -- 核心提示词
    max_iterations  INTEGER DEFAULT 50,
    model_tier      VARCHAR(20),              -- 'vision', 'standard', 'fast'
    
    -- 元数据
    is_enabled      BOOLEAN DEFAULT TRUE,
    version         INTEGER DEFAULT 1,        -- 配置版本号
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    
    CONSTRAINT agent_id_check CHECK (id IN ('planner', 'executor', 'evaluator'))
);

-- agent_tools 表（Agent 可用的工具）
CREATE TABLE agent_tools (
    agent_id        VARCHAR(50) REFERENCES agents(id) ON DELETE CASCADE,
    tool_name       VARCHAR(100) NOT NULL,
    is_enabled      BOOLEAN DEFAULT TRUE,
    priority        INTEGER DEFAULT 0,         -- 工具优先级
    
    PRIMARY KEY (agent_id, tool_name)
);

-- agent_skills 表（Agent 可用的技能）
CREATE TABLE agent_skills (
    agent_id        VARCHAR(50) REFERENCES agents(id) ON DELETE CASCADE,
    skill_name      VARCHAR(100) NOT NULL,
    is_enabled      BOOLEAN DEFAULT TRUE,
    
    PRIMARY KEY (agent_id, skill_name)
);

-- agent_config_history 表（配置变更历史）
CREATE TABLE agent_config_history (
    id              SERIAL PRIMARY KEY,
    agent_id        VARCHAR(50) REFERENCES agents(id),
    changed_by      VARCHAR(100),              -- 'system' / 'user:email'
    change_type     VARCHAR(20),               -- 'created', 'updated', 'reset'
    old_config      JSONB,
    new_config      JSONB,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

-- 索引
CREATE INDEX idx_agent_tools_agent ON agent_tools(agent_id);
CREATE INDEX idx_agent_skills_agent ON agent_skills(agent_id);
CREATE INDEX idx_history_agent ON agent_config_history(agent_id, created_at DESC);
```

---

## 📄 种子文件设计

### **目录结构**

```
internal/config/seed/
├── agents/
│   ├── planner.yaml
│   ├── executor.yaml
│   └── evaluator.yaml
├── tools/
│   └── registry.yaml          # 所有可用工具的定义
├── skills/
│   └── registry.yaml          # 所有可用技能的定义
└── seed.go                     # 种子加载逻辑
```

---

### **种子文件格式：planner.yaml**

```yaml
# internal/config/seed/agents/planner.yaml
id: planner
name: Planner Agent
description: |
  规划和分解任务的 Agent。负责将复杂目标拆解为可执行的 Action 序列，
  并管理 Roadmap（探索式规划）。
  
system_prompt: |
  You are the Planner Agent in the Liusha ADK system.
  
  ## Role
  You analyze objectives and decompose them into executable actions.
  You manage the Roadmap - an exploratory planning structure with 10-15 steps.
  
  ## Core Responsibilities
  1. Decompose objectives into actions
  2. Manage dependencies between actions
  3. Update roadmap based on observations
  4. Adjust strategy when blocked
  
  ## Available Tools
  {{#each tools}}
  - {{this.name}}: {{this.description}}
  {{/each}}
  
  ## Workflow
  1. Read objective from KnowledgeGraph
  2. Analyze current state (completed actions, observations)
  3. Propose next actions (use ProposeActionsTool)
  4. Update roadmap if needed
  
  ## Output Format
  - Actions: clear, specific, with complexity and priority
  - Dependencies: explicit action IDs
  - Roadmap: 10-15 steps with status
  
  ## Constraints
  - Never execute actions yourself (that's Executor's job)
  - Always check dependencies before proposing
  - Keep roadmap updated with actual progress

max_iterations: 30
model_tier: standard

# 可用工具（引用 tools/registry.yaml）
tools:
  - name: propose_actions
    enabled: true
    priority: 10
  
  - name: read_knowledge_graph
    enabled: true
    priority: 9
  
  - name: update_roadmap
    enabled: true
    priority: 8
  
  - name: read_insight
    enabled: true
    priority: 7

# 可用技能（引用 skills/registry.yaml）
skills:
  - name: task_decomposition
    enabled: true
  
  - name: dependency_analysis
    enabled: true

# 元数据
metadata:
  version: 1
  created_at: "2024-09-XX"
  author: system
```

---

### **种子文件格式：executor.yaml**

```yaml
# internal/config/seed/agents/executor.yaml
id: executor
name: Executor Agent
description: |
  执行具体 Action 的 Agent。负责运行命令、调用工具、产生 Observation。
  
system_prompt: |
  You are the Executor Agent in the Liusha ADK system.
  
  ## Role
  You execute actions and produce observations.
  You are the hands of the system - the only agent that runs commands.
  
  ## Core Responsibilities
  1. Execute actions from KnowledgeGraph
  2. Run CLI tools and scripts
  3. Produce structured observations
  4. Handle failures gracefully
  
  ## Available Tools
  {{#each tools}}
  - {{this.name}}: {{this.description}}
  {{/each}}
  
  ## Workflow
  1. Read pending actions from KnowledgeGraph
  2. Check dependencies (all deps must be done)
  3. Execute action (run command / call tool)
  4. Record observation with actual output
  5. Update action state (done/failed/blocked)
  
  ## Output Format
  - Observations: raw output, no interpretation
  - Anchor source: which action produced this
  - State updates: honest status
  
  ## Constraints
  - Never plan or decompose (that's Planner's job)
  - Never evaluate results (that's Evaluator's job)
  - Always record actual output, not summaries
  - Report failures honestly

max_iterations: 50
model_tier: standard

tools:
  - name: run_command
    enabled: true
    priority: 10
  
  - name: write_observation
    enabled: true
    priority: 9
  
  - name: update_action_state
    enabled: true
    priority: 8
  
  - name: read_insight
    enabled: true
    priority: 7
  
  - name: read_knowledge_graph
    enabled: true
    priority: 6

skills:
  - name: command_execution
    enabled: true
  
  - name: error_handling
    enabled: true

metadata:
  version: 1
  created_at: "2024-09-XX"
  author: system
```

---

### **种子文件格式：evaluator.yaml**

```yaml
# internal/config/seed/agents/evaluator.yaml
id: evaluator
name: Evaluator Agent
description: |
  评估 Observation 并产生 Result 的 Agent。负责验证、确认、证伪。
  
system_prompt: |
  You are the Evaluator Agent in the Liusha ADK system.
  
  ## Role
  You evaluate observations and produce verified results.
  You are the quality gate - distinguishing "possible" from "confirmed".
  
  ## Core Responsibilities
  1. Evaluate observations for correctness
  2. Verify claims with actual evidence
  3. Produce confirmed results
  4. Refute false positives
  
  ## Available Tools
  {{#each tools}}
  - {{this.name}}: {{this.description}}
  {{/each}}
  
  ## Workflow
  1. Read observations from KnowledgeGraph
  2. Analyze actual output (not tool claims)
  3. Verify with additional checks if needed
  4. Produce evaluation (CONFIRMS or REFUTES)
  5. Create result if confirmed
  
  ## Output Format
  - Evaluations: based on actual evidence
  - Results: only for verified observations
  - Confidence: unverified → verified / refuted
  
  ## Constraints
  - Never trust tool output blindly
  - Always read actual responses
  - Distinguish "tool says X" from "X is true"
  - Never execute actions (that's Executor's job)

max_iterations: 30
model_tier: standard

tools:
  - name: write_evaluation
    enabled: true
    priority: 10
  
  - name: write_result
    enabled: true
    priority: 9
  
  - name: read_knowledge_graph
    enabled: true
    priority: 8
  
  - name: replay_for_verification
    enabled: true
    priority: 7

skills:
  - name: evidence_verification
    enabled: true
  
  - name: claim_analysis
    enabled: true

metadata:
  version: 1
  created_at: "2024-09-XX"
  author: system
```

---

## 🔧 实现代码

### **1. Seed 加载器**

```go
// internal/config/seed/seed.go
package seed

import (
    "embed"
    "fmt"
    "gopkg.in/yaml.v3"
    
    "github.com/V3teran/liusha/internal/agent"
)

//go:embed agents/*.yaml
var agentFS embed.FS

// AgentSeed 是种子文件的结构
type AgentSeed struct {
    ID            string   `yaml:"id"`
    Name          string   `yaml:"name"`
    Description   string   `yaml:"description"`
    SystemPrompt  string   `yaml:"system_prompt"`
    MaxIterations int      `yaml:"max_iterations"`
    ModelTier     string   `yaml:"model_tier"`
    Tools         []Tool   `yaml:"tools"`
    Skills        []Skill  `yaml:"skills"`
    Metadata      Metadata `yaml:"metadata"`
}

type Tool struct {
    Name     string `yaml:"name"`
    Enabled  bool   `yaml:"enabled"`
    Priority int    `yaml:"priority"`
}

type Skill struct {
    Name    string `yaml:"name"`
    Enabled bool   `yaml:"enabled"`
}

type Metadata struct {
    Version   int    `yaml:"version"`
    CreatedAt string `yaml:"created_at"`
    Author    string `yaml:"author"`
}

// LoadSeeds 加载所有种子文件
func LoadSeeds() ([]AgentSeed, error) {
    agentIDs := []string{"planner", "executor", "evaluator"}
    seeds := make([]AgentSeed, 0, len(agentIDs))
    
    for _, id := range agentIDs {
        data, err := agentFS.ReadFile(fmt.Sprintf("agents/%s.yaml", id))
        if err != nil {
            return nil, fmt.Errorf("read %s seed: %w", id, err)
        }
        
        var seed AgentSeed
        if err := yaml.Unmarshal(data, &seed); err != nil {
            return nil, fmt.Errorf("parse %s seed: %w", id, err)
        }
        
        seeds = append(seeds, seed)
    }
    
    return seeds, nil
}
```

---

### **2. 数据库迁移**

```go
// internal/config/seed/migrate.go
package seed

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
)

// SeedDatabase 初始化数据库（仅在首次启动时）
func SeedDatabase(ctx context.Context, db *sql.DB) error {
    // 检查是否已初始化
    var count int
    err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM agents").Scan(&count)
    if err != nil {
        return fmt.Errorf("check existing agents: %w", err)
    }
    
    if count > 0 {
        // 已初始化，跳过
        return nil
    }
    
    // 加载种子
    seeds, err := LoadSeeds()
    if err != nil {
        return fmt.Errorf("load seeds: %w", err)
    }
    
    // 开启事务
    tx, err := db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()
    
    // 插入 agents
    for _, seed := range seeds {
        _, err := tx.ExecContext(ctx, `
            INSERT INTO agents (id, name, description, system_prompt, max_iterations, model_tier, version)
            VALUES ($1, $2, $3, $4, $5, $6, $7)
        `, seed.ID, seed.Name, seed.Description, seed.SystemPrompt, 
           seed.MaxIterations, seed.ModelTier, seed.Metadata.Version)
        if err != nil {
            return fmt.Errorf("insert agent %s: %w", seed.ID, err)
        }
        
        // 插入 tools
        for _, tool := range seed.Tools {
            _, err := tx.ExecContext(ctx, `
                INSERT INTO agent_tools (agent_id, tool_name, is_enabled, priority)
                VALUES ($1, $2, $3, $4)
            `, seed.ID, tool.Name, tool.Enabled, tool.Priority)
            if err != nil {
                return fmt.Errorf("insert tool %s for %s: %w", tool.Name, seed.ID, err)
            }
        }
        
        // 插入 skills
        for _, skill := range seed.Skills {
            _, err := tx.ExecContext(ctx, `
                INSERT INTO agent_skills (agent_id, skill_name, is_enabled)
                VALUES ($1, $2, $3)
            `, seed.ID, skill.Name, skill.Enabled)
            if err != nil {
                return fmt.Errorf("insert skill %s for %s: %w", skill.Name, seed.ID, err)
            }
        }
        
        // 记录历史
        newConfig, _ := json.Marshal(seed)
        _, err = tx.ExecContext(ctx, `
            INSERT INTO agent_config_history (agent_id, changed_by, change_type, new_config)
            VALUES ($1, $2, $3, $4)
        `, seed.ID, "system", "created", newConfig)
        if err != nil {
            return fmt.Errorf("insert history for %s: %w", seed.ID, err)
        }
    }
    
    return tx.Commit()
}
```

---

### **3. Agent 配置服务**

```go
// internal/agent/config_service.go
package agent

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "time"
)

type ConfigService struct {
    db *sql.DB
}

func NewConfigService(db *sql.DB) *ConfigService {
    return &ConfigService{db: db}
}

// AgentConfig 是完整的 Agent 配置
type AgentConfig struct {
    ID            string
    Name          string
    Description   string
    SystemPrompt  string
    MaxIterations int
    ModelTier     string
    Tools         []ToolConfig
    Skills        []SkillConfig
    IsEnabled     bool
    Version       int
    UpdatedAt     time.Time
}

type ToolConfig struct {
    Name     string
    Enabled  bool
    Priority int
}

type SkillConfig struct {
    Name    string
    Enabled bool
}

// GetConfig 获取 Agent 配置（内存 → Redis → PostgreSQL）
func (s *ConfigService) GetConfig(ctx context.Context, agentID string) (*AgentConfig, error) {
    // TODO: 先查内存缓存
    // TODO: 再查 Redis
    
    // 最后查数据库
    return s.getFromDB(ctx, agentID)
}

func (s *ConfigService) getFromDB(ctx context.Context, agentID string) (*AgentConfig, error) {
    var cfg AgentConfig
    
    // 查询 agent
    err := s.db.QueryRowContext(ctx, `
        SELECT id, name, description, system_prompt, max_iterations, 
               model_tier, is_enabled, version, updated_at
        FROM agents WHERE id = $1
    `, agentID).Scan(&cfg.ID, &cfg.Name, &cfg.Description, &cfg.SystemPrompt,
        &cfg.MaxIterations, &cfg.ModelTier, &cfg.IsEnabled, &cfg.Version, &cfg.UpdatedAt)
    if err != nil {
        return nil, fmt.Errorf("query agent: %w", err)
    }
    
    // 查询 tools
    rows, err := s.db.QueryContext(ctx, `
        SELECT tool_name, is_enabled, priority
        FROM agent_tools WHERE agent_id = $1
        ORDER BY priority DESC
    `, agentID)
    if err != nil {
        return nil, fmt.Errorf("query tools: %w", err)
    }
    defer rows.Close()
    
    for rows.Next() {
        var tool ToolConfig
        if err := rows.Scan(&tool.Name, &tool.Enabled, &tool.Priority); err != nil {
            return nil, err
        }
        cfg.Tools = append(cfg.Tools, tool)
    }
    
    // 查询 skills
    rows, err = s.db.QueryContext(ctx, `
        SELECT skill_name, is_enabled
        FROM agent_skills WHERE agent_id = $1
    `, agentID)
    if err != nil {
        return nil, fmt.Errorf("query skills: %w", err)
    }
    defer rows.Close()
    
    for rows.Next() {
        var skill SkillConfig
        if err := rows.Scan(&skill.Name, &skill.Enabled); err != nil {
            return nil, err
        }
        cfg.Skills = append(cfg.Skills, skill)
    }
    
    return &cfg, nil
}

// UpdateConfig 更新 Agent 配置
func (s *ConfigService) UpdateConfig(ctx context.Context, agentID string, updates AgentConfig, changedBy string) error {
    // 读取旧配置
    oldCfg, err := s.getFromDB(ctx, agentID)
    if err != nil {
        return fmt.Errorf("get old config: %w", err)
    }
    
    // 开启事务
    tx, err := s.db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()
    
    // 更新 agents 表
    _, err = tx.ExecContext(ctx, `
        UPDATE agents 
        SET name = $2, description = $3, system_prompt = $4, 
            max_iterations = $5, model_tier = $6, version = version + 1, 
            updated_at = NOW()
        WHERE id = $1
    `, agentID, updates.Name, updates.Description, updates.SystemPrompt,
       updates.MaxIterations, updates.ModelTier)
    if err != nil {
        return fmt.Errorf("update agent: %w", err)
    }
    
    // TODO: 更新 tools 和 skills
    
    // 记录历史
    oldJSON, _ := json.Marshal(oldCfg)
    newJSON, _ := json.Marshal(updates)
    _, err = tx.ExecContext(ctx, `
        INSERT INTO agent_config_history (agent_id, changed_by, change_type, old_config, new_config)
        VALUES ($1, $2, $3, $4, $5)
    `, agentID, changedBy, "updated", oldJSON, newJSON)
    if err != nil {
        return fmt.Errorf("insert history: %w", err)
    }
    
    if err := tx.Commit(); err != nil {
        return err
    }
    
    // TODO: 清除缓存（Redis + 内存）
    
    return nil
}

// ResetToDefault 重置为默认配置
func (s *ConfigService) ResetToDefault(ctx context.Context, agentID string, changedBy string) error {
    // 加载种子
    seeds, err := LoadSeeds()
    if err != nil {
        return fmt.Errorf("load seeds: %w", err)
    }
    
    var seed *AgentSeed
    for _, s := range seeds {
        if s.ID == agentID {
            seed = &s
            break
        }
    }
    
    if seed == nil {
        return fmt.Errorf("seed not found for %s", agentID)
    }
    
    // 转换为 AgentConfig
    updates := AgentConfig{
        ID:            seed.ID,
        Name:          seed.Name,
        Description:   seed.Description,
        SystemPrompt:  seed.SystemPrompt,
        MaxIterations: seed.MaxIterations,
        ModelTier:     seed.ModelTier,
    }
    
    for _, t := range seed.Tools {
        updates.Tools = append(updates.Tools, ToolConfig{
            Name:     t.Name,
            Enabled:  t.Enabled,
            Priority: t.Priority,
        })
    }
    
    for _, sk := range seed.Skills {
        updates.Skills = append(updates.Skills, SkillConfig{
            Name:    sk.Name,
            Enabled: sk.Enabled,
        })
    }
    
    // 更新（change_type = 'reset'）
    return s.UpdateConfig(ctx, agentID, updates, changedBy)
}
```

---

## 🌐 API 接口设计

### **HTTP API**

```go
// internal/httpapi/agent_config_handler.go
package httpapi

import (
    "github.com/gin-gonic/gin"
)

// GET /api/agents - 列出所有 Agent
func (h *Handler) ListAgents(c *gin.Context) {
    agents := []string{"planner", "executor", "evaluator"}
    
    configs := make([]gin.H, 0, len(agents))
    for _, id := range agents {
        cfg, err := h.configSvc.GetConfig(c.Request.Context(), id)
        if err != nil {
            c.JSON(500, gin.H{"error": err.Error()})
            return
        }
        
        configs = append(configs, gin.H{
            "id":             cfg.ID,
            "name":           cfg.Name,
            "description":    cfg.Description,
            "is_enabled":     cfg.IsEnabled,
            "version":        cfg.Version,
            "updated_at":     cfg.UpdatedAt,
        })
    }
    
    c.JSON(200, gin.H{"agents": configs})
}

// GET /api/agents/:id - 获取单个 Agent 配置
func (h *Handler) GetAgent(c *gin.Context) {
    agentID := c.Param("id")
    
    cfg, err := h.configSvc.GetConfig(c.Request.Context(), agentID)
    if err != nil {
        c.JSON(404, gin.H{"error": "agent not found"})
        return
    }
    
    c.JSON(200, gin.H{"agent": cfg})
}

// PUT /api/agents/:id - 更新 Agent 配置
func (h *Handler) UpdateAgent(c *gin.Context) {
    agentID := c.Param("id")
    
    var updates agent.AgentConfig
    if err := c.ShouldBindJSON(&updates); err != nil {
        c.JSON(400, gin.H{"error": err.Error()})
        return
    }
    
    changedBy := "user:admin" // TODO: 从认证中获取
    
    if err := h.configSvc.UpdateConfig(c.Request.Context(), agentID, updates, changedBy); err != nil {
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }
    
    c.JSON(200, gin.H{"ok": true})
}

// POST /api/agents/:id/reset - 重置为默认配置
func (h *Handler) ResetAgent(c *gin.Context) {
    agentID := c.Param("id")
    changedBy := "user:admin" // TODO: 从认证中获取
    
    if err := h.configSvc.ResetToDefault(c.Request.Context(), agentID, changedBy); err != nil {
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }
    
    c.JSON(200, gin.H{"ok": true, "message": "reset to default"})
}

// GET /api/agents/:id/history - 获取配置变更历史
func (h *Handler) GetAgentHistory(c *gin.Context) {
    agentID := c.Param("id")
    
    // TODO: 查询 agent_config_history 表
    
    c.JSON(200, gin.H{"history": []gin.H{}})
}
```

---

## 🎨 前端集成示例

### **React 组件示例**

```typescript
// frontend/src/components/AgentConfig.tsx
import React, { useState, useEffect } from 'react';

interface AgentConfig {
  id: string;
  name: string;
  description: string;
  system_prompt: string;
  max_iterations: number;
  model_tier: string;
  tools: Tool[];
  skills: Skill[];
  is_enabled: boolean;
  version: number;
}

interface Tool {
  name: string;
  enabled: boolean;
  priority: number;
}

interface Skill {
  name: string;
  enabled: boolean;
}

export const AgentConfigEditor: React.FC<{ agentId: string }> = ({ agentId }) => {
  const [config, setConfig] = useState<AgentConfig | null>(null);
  const [editing, setEditing] = useState(false);
  
  useEffect(() => {
    fetch(`/api/agents/${agentId}`)
      .then(res => res.json())
      .then(data => setConfig(data.agent));
  }, [agentId]);
  
  const handleSave = async () => {
    await fetch(`/api/agents/${agentId}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(config),
    });
    setEditing(false);
  };
  
  const handleReset = async () => {
    if (confirm('确定要重置为默认配置吗？')) {
      await fetch(`/api/agents/${agentId}/reset`, { method: 'POST' });
      // 重新加载
      window.location.reload();
    }
  };
  
  if (!config) return <div>Loading...</div>;
  
  return (
    <div className="agent-config">
      <h2>{config.name}</h2>
      <p>{config.description}</p>
      
      {editing ? (
        <div>
          <label>
            System Prompt:
            <textarea
              value={config.system_prompt}
              onChange={e => setConfig({ ...config, system_prompt: e.target.value })}
              rows={20}
            />
          </label>
          
          <label>
            Max Iterations:
            <input
              type="number"
              value={config.max_iterations}
              onChange={e => setConfig({ ...config, max_iterations: parseInt(e.target.value) })}
            />
          </label>
          
          <h3>Tools</h3>
          {config.tools.map(tool => (
            <div key={tool.name}>
              <label>
                <input
                  type="checkbox"
                  checked={tool.enabled}
                  onChange={e => {
                    const newTools = config.tools.map(t =>
                      t.name === tool.name ? { ...t, enabled: e.target.checked } : t
                    );
                    setConfig({ ...config, tools: newTools });
                  }}
                />
                {tool.name}
              </label>
            </div>
          ))}
          
          <button onClick={handleSave}>保存</button>
          <button onClick={() => setEditing(false)}>取消</button>
        </div>
      ) : (
        <div>
          <pre>{config.system_prompt}</pre>
          <p>Max Iterations: {config.max_iterations}</p>
          <p>Version: {config.version}</p>
          
          <button onClick={() => setEditing(true)}>编辑</button>
          <button onClick={handleReset}>重置为默认</button>
        </div>
      )}
    </div>
  );
};
```

---

## 🔄 数据流

### **完整数据流**

```
1. 启动时：
   Seed YAML → 数据库初始化（仅首次）

2. 读取配置：
   内存缓存 → Redis → PostgreSQL → 返回

3. 前端编辑：
   前端 → HTTP API → ConfigService → PostgreSQL
   → 清除缓存 → 记录历史

4. 重置配置：
   前端 → HTTP API → 加载 Seed YAML → 更新数据库
   → 清除缓存 → 记录历史

5. Agent 运行时：
   Agent 启动 → 读取配置 → 缓存到内存 → 使用配置
```

---

## 📋 迁移步骤

### **从 Hunters 迁移到新架构**

```bash
# 步骤 1：创建新目录结构
mkdir -p internal/config/seed/agents
mkdir -p internal/config/seed/tools
mkdir -p internal/config/seed/skills

# 步骤 2：创建种子文件
# 手工创建 planner.yaml, executor.yaml, evaluator.yaml
# （基于上面的模板）

# 步骤 3：实现加载器和迁移代码
# seed.go, migrate.go, config_service.go

# 步骤 4：创建数据库表
# 运行 migration SQL

# 步骤 5：首次启动时自动种子
# 在 main.go 中调用 seed.SeedDatabase()

# 步骤 6：实现 HTTP API
# agent_config_handler.go

# 步骤 7：前端集成
# 创建 AgentConfigEditor 组件

# 步骤 8：删除旧 hunters 目录
rm -rf hunters
```

---

## ✅ 总结

### **核心设计：**

1. ✅ **Seed YAML**（版本控制，易读易写）
   - 3 个文件：planner.yaml, executor.yaml, evaluator.yaml
   - 包含：名字、描述、提示词、工具、技能

2. ✅ **数据库存储**（运行时状态）
   - agents 表（3 条固定记录）
   - agent_tools 表（多对多）
   - agent_skills 表（多对多）
   - agent_config_history 表（变更历史）

3. ✅ **三层缓存**（内存 → Redis → PostgreSQL）
   - 内存：Agent 运行时缓存
   - Redis：跨进程共享
   - PostgreSQL：持久化存储

4. ✅ **前端编辑**
   - REST API：GET/PUT/POST
   - 版本控制：每次更新 +1
   - 重置机制：恢复默认

5. ✅ **变更追踪**
   - 历史记录表
   - 记录谁改的、改了什么

---

**要开始实现吗？** 🚀
