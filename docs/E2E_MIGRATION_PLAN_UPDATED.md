# E2E 测试迁移计划（更新版）

## 背景

当前 e2e 测试基于 **finding 表行数** 验收，需要迁移到 **知识图谱架构** 验收。

## 架构对比

### 旧验收标准（Finding-based）
```go
type profile struct {
    minFindings int  // 发现的漏洞数量 ≥ minFindings
}
```
- **问题**：只看结果，不看过程
- **盲区**：LLM 可能"猜"出漏洞但没有完整推理链

### 新验收标准（Knowledge Graph-based）
```go
type profile struct {
    minObjectives   int     // 至少产生多少个目标
    minActions      int     // 至少执行多少个动作
    minResults      int     // 至少验证多少个结果
    mustHavePaths   []Path  // 必须存在的路径
}

type Path struct {
    From string // "objective"
    Via  string // "GENERATES"
    To   string // "action"
}
```
- **优势**：验证完整认知循环
- **覆盖**：objective → action → observation → evaluation → result

## 数据库支持

已完成（migration 0134）：
```sql
kind IN (
    'objective',      -- 目标（Planner 生成）
    'action',         -- 动作（Executor 执行）
    'observation',    -- 观察（Executor 记录）
    'evaluation',     -- 评估（Evaluator 验证）✅
    'result'          -- 结果（确认发现）
)
```

## 迁移阶段

### ✅ Phase 0: 紧急修复（已完成）

**问题**：
- e2e.sh 未导出 GLM_API_KEY，子进程读不到
- MIMO API Key 已弃用，需切换到 GLM

**修复**：
```bash
# scripts/dev/e2e.sh（已修改）
export GLM_API_KEY="${GLM_API_KEY}"
export LIUSHA_LLM_FALLBACK="${LIUSHA_LLM_FALLBACK:-glm}"
```

**验收**：
```bash
# 确认 .env.local 包含：
GLM_API_KEY=your-actual-glm-key-here
LIUSHA_LLM_FALLBACK=glm
LIUSHA_LLM_KEY_SECRET=your-32-bytes-hex

# 跑一个简单 profile
./scripts/dev/e2e.sh active:xss
```

---

### 📋 Phase 1: 知识图谱 API（1 周）

**新增 API 端点**：

```go
// internal/httpapi/knowledge_graph_handler.go

// GET /api/v1/tasks/{taskId}/graph
// 返回完整知识图谱（nodes + edges）
type GraphResponse struct {
    Nodes []knowledgegraph.Node `json:"nodes"`
    Edges []knowledgegraph.Edge `json:"edges"`
}

// GET /api/v1/tasks/{taskId}/nodes?kind=objective
// 按类型筛选节点
type NodesResponse struct {
    Nodes []knowledgegraph.Node `json:"nodes"`
}

// GET /api/v1/tasks/{taskId}/stats
// 快速统计（e2e 轮询专用，避免传输大量数据）
type StatsResponse struct {
    Objectives   int `json:"objectives"`
    Actions      int `json:"actions"`
    Observations int `json:"observations"`
    Evaluations  int `json:"evaluations"`
    Results      int `json:"results"`
}
```

**实现清单**：
- [ ] internal/httpapi/knowledge_graph_handler.go（新建）
- [ ] 注册路由到 cmd/api/main.go
- [ ] 单元测试 knowledge_graph_handler_test.go
- [ ] API 文档（docs/api/knowledge_graph.md）

**验收标准**：
```bash
# 手动测试
curl -H "X-API-Key: xxx" http://localhost:8090/api/v1/tasks/{taskId}/stats
# 预期输出：
# {"objectives":1,"actions":5,"observations":5,"evaluations":3,"results":2}
```

---

### 📋 Phase 2: E2E 测试重构（1-2 周）

#### 2.1 重构数据模型

```go
// cmd/e2e/models.go（新建）

type GraphStats struct {
    Objectives   int
    Actions      int
    Observations int
    Evaluations  int
    Results      int
}

type Path struct {
    From string // node kind: "objective"
    Via  string // relation: "GENERATES"
    To   string // node kind: "action"
}
```

#### 2.2 重构验收标准

```go
// cmd/e2e/profiles.go（改造）

type profile struct {
    name           string
    defaultSamples string
    
    // 新验收标准
    minObjectives int
    minActions    int
    minResults    int
    mustHavePaths []Path  // 必须存在的路径
    
    credsForHost func(host string) []credentialEntry
}

// 示例：SQLi profile
var profiles = map[string]profile{
    "sqli": {
        name:           "sqli",
        defaultSamples: "examples/sample_sqli_raw.json",
        minObjectives: 1,   // "测试 SQL 注入"
        minActions:    5,   // "扫描参数" + "测试注入点" + "验证漏洞" + ...
        minResults:    1,   // 至少发现1个注入点
        mustHavePaths: []Path{
            {From: "objective", Via: "GENERATES", To: "action"},
            {From: "action", Via: "GENERATES", To: "observation"},
            {From: "evaluation", Via: "CONFIRMS", To: "result"},
        },
    },
}
```

#### 2.3 重构轮询逻辑

```go
// cmd/e2e/poller.go（新建）

func pollTaskCompletion(ctx context.Context, taskID string, p profile) error {
    deadline := time.Now().Add(pollDeadline())
    
    for time.Now().Before(deadline) {
        // 调用新 API 获取统计
        stats, err := client.GetTaskStats(taskID)
        if err != nil {
            return err
        }
        
        // 检查是否满足验收标准
        if meetsAcceptanceCriteria(stats, p) {
            return nil
        }
        
        // 检查任务是否卡住
        if isStuck(stats) {
            return fmt.Errorf("task stuck: no progress")
        }
        
        time.Sleep(15 * time.Second)
    }
    
    return fmt.Errorf("timeout")
}

func meetsAcceptanceCriteria(stats GraphStats, p profile) bool {
    if stats.Objectives < p.minObjectives {
        return false
    }
    if stats.Actions < p.minActions {
        return false
    }
    if stats.Results < p.minResults {
        return false
    }
    
    // TODO: 验证 mustHavePaths（需要调用 /graph 查询边）
    
    return true
}

func isStuck(stats GraphStats) bool {
    // 如果有 objective 但没有 action，说明 Planner 生成目标后 Executor 没动
    if stats.Objectives > 0 && stats.Actions == 0 {
        return true
    }
    
    // 如果有很多 observation 但没有 evaluation，说明 Evaluator 卡住了
    if stats.Observations > 5 && stats.Evaluations == 0 {
        return true
    }
    
    return false
}
```

#### 2.4 更新所有 Profile

**Passive profiles（13 个）**：
- [ ] bac（访问控制）
- [ ] sqli（SQL 注入）
- [ ] xss（跨站脚本）
- [ ] brute（暴力破解）
- [ ] lfi（文件包含）
- [ ] upload（文件上传）
- [ ] csrf
- [ ] api
- [ ] cryptography
- [ ] redirect
- [ ] authbypass
- [ ] csp
- [ ] exec（命令注入）

**Active profiles（5 个）**：
- [ ] active:full
- [ ] active:xss
- [ ] active:privesc
- [ ] active:bac
- [ ] active:adhoc

**实施清单**：
- [ ] cmd/e2e/models.go（新建）
- [ ] cmd/e2e/poller.go（新建）
- [ ] cmd/e2e/profiles.go（重构）
- [ ] cmd/e2e/runner.go（改用新 poller）
- [ ] cmd/e2e/client.go（新增 GetTaskStats 方法）

---

### 📋 Phase 3: 向后兼容（可选，3 天）

**保留旧 API 供前端过渡**：

```go
// internal/httpapi/finding_handler.go
// GET /api/v1/tasks/{taskId}/findings
// 内部从 wm_node 筛选 kind='result' 转换

func (h *FindingHandler) List(c *gin.Context) {
    taskID := c.Param("taskId")
    
    // 从知识图谱查询 result 节点
    nodes, err := h.graph.ListNodes(ctx, taskID, knowledgegraph.NodeFilter{
        Kind: core.KindResult,
    })
    if err != nil {
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }
    
    // 转换为 Finding 格式
    findings := make([]finding.Finding, len(nodes))
    for i, node := range nodes {
        findings[i] = nodeToFinding(node)
    }
    
    c.JSON(200, findings)
}

func nodeToFinding(node knowledgegraph.Node) finding.Finding {
    var content map[string]interface{}
    _ = json.Unmarshal(node.Content, &content)
    
    return finding.Finding{
        ID:         node.ID,
        TaskID:     node.TaskID,
        Kind:       "vulnerability", // 从 content 解析
        Severity:   content["severity"].(string),
        Title:      content["title"].(string),
        Summary:    content["summary"].(string),
        CreatedAt:  node.CreatedAt,
    }
}
```

**实施清单**：
- [ ] 改造 internal/httpapi/finding_handler.go
- [ ] 前端无感知切换

---

## 实施时间线

- **Week 1**: Phase 1（知识图谱 API）
  - Day 1-2: 实现 3 个 API 端点
  - Day 3-4: 单元测试 + API 文档
  - Day 5: Code Review + 集成测试

- **Week 2-3**: Phase 2（E2E 重构）
  - Day 1-2: 重构数据模型 + 轮询逻辑
  - Day 3-5: 更新 13 个 passive profiles
  - Day 6-8: 更新 5 个 active profiles
  - Day 9-10: 端到端测试和调试

- **Week 4**: Phase 3（兼容性，可选）
  - Day 1-2: 旧 API 适配新数据源
  - Day 3-4: 前端测试
  - Day 5: 最终验收

## 验收标准

### 功能完整性
- [ ] 所有 passive profiles 通过（13 个）
- [ ] 所有 active profiles 通过（5 个）
- [ ] 知识图谱 API 返回正确数据

### 性能
- [ ] E2E 测试总时间 ≤ 原时间的 120%
- [ ] 单个 profile 平均执行时间 < 5 分钟

### 可维护性
- [ ] 代码覆盖率 ≥ 80%
- [ ] 文档完整（API 文档 + E2E 使用指南）
- [ ] CI/CD 集成成功

## 风险和缓解

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| 新 API 不稳定 | 高 | 先在开发环境充分测试；Phase 3 保留旧 API 作为后备 |
| 验收标准定义不准 | 中 | 与团队充分讨论；从实际运行数据中调整阈值 |
| 迁移时间过长 | 中 | 分阶段迁移；Phase 0 紧急修复已完成，系统可用 |
| GLM API 配额不足 | 低 | 监控用量；准备备用 provider |

## 参考资料

- [知识图谱架构文档](./KNOWLEDGE_GRAPH.md)
- [新 API 设计](./API_DESIGN.md)
- [测试策略](./TEST_STRATEGY.md)
