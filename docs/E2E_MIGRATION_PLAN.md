# E2E 测试迁移计划

## 背景

当前 e2e 测试（`scripts/dev/e2e.sh` 和 `cmd/e2e/`）基于旧的 finding 架构，需要迁移到新的知识图谱架构。

## 架构对比

### 旧架构（Finding-based）
```
Task → Finding (漏洞发现)
     → AgentRun (执行记录)
```

- **核心概念**: `Finding`（漏洞）作为主要输出
- **表**: `finding`, `finding_relation`, `agent_run`
- **API**: `/api/v1/tasks/{taskId}/findings`
- **验收标准**: 发现的漏洞数量 ≥ minFindings

### 新架构（Knowledge Graph-based）
```
Task → Objective (目标)
     → Action (动作)
     → Observation (观察)
     → Result (结果)
```

- **核心概念**: 知识图谱节点和边
- **表**: `wm_node`, `wm_edge`
- **节点类型**: `objective`, `action`, `observation`, `evaluation`, `result`
- **关系类型**: `GENERATES`, `CONFIRMS`, `REFUTES`, `ENABLES`, `DEPENDS_ON`

## 迁移阶段

### Phase 1: 环境配置更新（已完成）

✅ **1.1 更新 LLM Provider: MIMO → GLM**
- `.env.example`: `LIUSHA_LLM_FALLBACK=glm`
- 移除 `XIAOMI_API_KEY`，添加 `GLM_API_KEY`

### Phase 2: API 层适配（待实现）

**2.1 新增知识图谱 API 端点**

```go
// internal/httpapi/knowledge_graph_handler.go

// GET /api/v1/tasks/{taskId}/graph
// 返回完整的知识图谱（nodes + edges）
func (h *KnowledgeGraphHandler) GetGraph(c *gin.Context) {
    taskID := c.Param("taskId")
    
    nodes, _ := h.store.ListNodes(ctx, taskID, knowledgegraph.NodeFilter{})
    edges, _ := h.store.ListEdges(ctx, taskID, knowledgegraph.EdgeFilter{})
    
    c.JSON(200, GraphResponse{
        Nodes: nodes,
        Edges: edges,
    })
}

// GET /api/v1/tasks/{taskId}/objectives
// 列出所有目标

// GET /api/v1/tasks/{taskId}/actions
// 列出所有动作

// GET /api/v1/tasks/{taskId}/results
// 列出所有结果（验证通过的发现）
```

**2.2 保留旧 API 用于向后兼容**
- 保留 `/api/v1/tasks/{taskId}/findings`
- 内部从 `wm_node` 中筛选 `kind='result'` 的节点转换为 Finding 格式

### Phase 3: E2E 测试重构（核心工作）

**3.1 重构数据模型**

```go
// cmd/e2e/models.go

type TaskGraph struct {
    TaskID      string
    Objectives  []Node
    Actions     []Node
    Observations []Node
    Results     []Node
    Edges       []Edge
}

type Node struct {
    ID        string
    Kind      string // objective/action/observation/result
    TaskID    string
    State     *string
    Content   json.RawMessage
    CreatedAt time.Time
}

type Edge struct {
    TaskID string
    SrcID  string
    Rel    string // GENERATES/CONFIRMS/ENABLES/DEPENDS_ON
    DstID  string
}
```

**3.2 重构验收标准**

```go
// 旧的验收标准：finding 数量
type profile struct {
    minFindings int  // ❌ 旧
}

// 新的验收标准：知识图谱完整性
type profile struct {
    minObjectives   int     // 至少产生多少个目标
    minActions      int     // 至少执行多少个动作
    minResults      int     // 至少验证多少个结果
    mustHavePaths   []Path  // 必须存在的路径（如 objective → action → observation → result）
}

type Path struct {
    From string // node kind
    Via  string // relation
    To   string // node kind
}

// 示例：SQL注入测试
profiles["sqli"] = profile{
    name: "sqli",
    minObjectives: 1,  // "测试 SQL 注入"
    minActions: 3,     // "扫描参数" + "测试注入点" + "验证漏洞"
    minResults: 1,     // 至少发现1个注入点
    mustHavePaths: []Path{
        {From: "objective", Via: "GENERATES", To: "action"},
        {From: "action", Via: "GENERATES", To: "observation"},
        {From: "observation", Via: "CONFIRMS", To: "result"},
    },
}
```

**3.3 重构轮询逻辑**

```go
// cmd/e2e/poller.go

func pollTaskCompletion(ctx context.Context, taskID string, p profile) error {
    deadline := time.Now().Add(pollDeadline())
    
    for time.Now().Before(deadline) {
        graph, err := client.GetTaskGraph(taskID)
        if err != nil {
            return err
        }
        
        // 检查是否满足验收标准
        if meetsAcceptanceCriteria(graph, p) {
            return nil
        }
        
        // 检查任务是否卡住
        if isStuck(graph) {
            return fmt.Errorf("task stuck: %v", graph.DiagnoseIssue())
        }
        
        time.Sleep(5 * time.Second)
    }
    
    return fmt.Errorf("timeout")
}

func meetsAcceptanceCriteria(graph TaskGraph, p profile) bool {
    objectives := filterByKind(graph.Nodes, "objective")
    actions := filterByKind(graph.Nodes, "action")
    results := filterByKind(graph.Nodes, "result")
    
    if len(objectives) < p.minObjectives {
        return false
    }
    if len(actions) < p.minActions {
        return false
    }
    if len(results) < p.minResults {
        return false
    }
    
    // 验证必须存在的路径
    for _, path := range p.mustHavePaths {
        if !graphHasPath(graph, path) {
            return false
        }
    }
    
    return true
}
```

**3.4 更新测试场景**

```go
// cmd/e2e/profiles.go

var passiveProfiles = map[string]profile{
    "sqli": {
        name:  "sqli",
        brief: "测试 DVWA SQL 注入，登录后设置 security=low",
        minObjectives: 1,
        minActions: 5,    // 扫描、测试、验证、利用、报告
        minResults: 1,    // 至少发现1个注入点
        mustHavePaths: []Path{
            {From: "objective", Via: "GENERATES", To: "action"},
            {From: "action", Via: "GENERATES", To: "observation"},
            {From: "observation", Via: "CONFIRMS", To: "result"},
        },
    },
    
    "xss": {
        name:  "xss",
        brief: "测试 DVWA XSS，检测 reflected/stored/DOM 三种类型",
        minObjectives: 1,
        minActions: 8,    // 扫描表单 + 测试注入点 + 验证触发
        minResults: 1,
        mustHavePaths: []Path{
            {From: "objective", Via: "GENERATES", To: "action"},
            {From: "action", Via: "GENERATES", To: "observation"},
            {From: "observation", Via: "CONFIRMS", To: "result"},
        },
    },
    
    "bac": {
        name:  "bac",
        brief: "测试 DVWA 访问控制，检测未授权/垂直越权/水平越权",
        minObjectives: 1,
        minActions: 10,   // 枚举端点 + 测试匿名访问 + 测试越权
        minResults: 1,
        mustHavePaths: []Path{
            {From: "objective", Via: "GENERATES", To: "action"},
            {From: "action", Via: "GENERATES", To: "observation"},
            {From: "observation", Via: "CONFIRMS", To: "result"},
        },
    },
}
```

### Phase 4: 兼容性处理

**4.1 双模式运行（过渡期）**

```bash
# scripts/dev/e2e.sh 支持模式切换
export LIUSHA_E2E_MODE="${LIUSHA_E2E_MODE:-graph}"  # graph | legacy

if [ "$LIUSHA_E2E_MODE" = "legacy" ]; then
    # 使用旧的 finding-based 验收
    go run ./cmd/e2e/legacy "$@"
else
    # 使用新的 graph-based 验收
    go run ./cmd/e2e "$@"
fi
```

**4.2 数据迁移脚本**

如果需要从旧数据迁移：

```sql
-- 将 finding 转换为 result 节点
INSERT INTO wm_node (id, task_id, kind, state, content, created_at, updated_at)
SELECT 
    id,
    task_id,
    'result' as kind,
    NULL as state,
    jsonb_build_object(
        'title', title,
        'severity', severity,
        'confidence', confidence,
        'summary', summary
    ) as content,
    created_at,
    updated_at
FROM finding
WHERE NOT EXISTS (SELECT 1 FROM wm_node WHERE wm_node.id = finding.id);
```

## 实施时间线

- **Week 1**: Phase 2 (API 层)
  - Day 1-2: 实现知识图谱 API 端点
  - Day 3-4: 测试和文档
  - Day 5: Code Review

- **Week 2-3**: Phase 3 (E2E 重构)
  - Day 1-3: 重构数据模型和轮询逻辑
  - Day 4-7: 更新所有测试场景
  - Day 8-10: 端到端测试和调试

- **Week 4**: Phase 4 (兼容性)
  - Day 1-2: 双模式支持
  - Day 3-4: 文档和培训
  - Day 5: 最终验收

## 验收标准

✅ **功能完整性**
- [ ] 所有 passive profiles 通过（sqli/xss/bac/lfi/upload/brute）
- [ ] 所有 active profiles 通过（full/privesc）
- [ ] 知识图谱 API 返回正确数据

✅ **性能**
- [ ] E2E 测试总时间 ≤ 原时间的 120%
- [ ] 单个 profile 平均执行时间 < 5 分钟

✅ **可维护性**
- [ ] 代码覆盖率 ≥ 80%
- [ ] 文档完整（API 文档 + E2E 使用指南）
- [ ] CI/CD 集成成功

## 风险和缓解

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| 新 API 不稳定 | 高 | 先在开发环境充分测试；保留旧 API 作为后备 |
| 验收标准定义不准 | 中 | 与团队充分讨论；从实际运行数据中调整阈值 |
| 迁移时间过长 | 中 | 分阶段迁移；保持双模式运行 |
| GLM API 配额不足 | 低 | 监控用量；准备备用 provider |

## 参考资料

- [知识图谱架构文档](./KNOWLEDGE_GRAPH.md)
- [新 API 设计](./API_DESIGN.md)
- [测试策略](./TEST_STRATEGY.md)
