# Phase 1: 知识图谱 API - 完成总结

## ✅ 已完成的工作

### 1. 实现了 3 个 HTTP API 端点

#### GET /api/v1/tasks/{taskId}/stats
**用途**：快速统计（e2e 轮询专用）

**响应**：
```json
{
  "objectives": 2,
  "actions": 5,
  "observations": 5,
  "evaluations": 3,
  "results": 2
}
```

**特点**：
- 避免传输大量节点数据
- 专为 e2e 测试轮询设计
- O(n) 时间复杂度，遍历所有节点统计

#### GET /api/v1/tasks/{taskId}/nodes?kind=objective
**用途**：按类型筛选节点

**查询参数**：
- `kind`（可选）：objective | action | observation | evaluation | result

**响应**：
```json
{
  "nodes": [
    {
      "id": "node-123",
      "task_id": "task-456",
      "kind": "objective",
      "content": {...},
      "created_at": "2024-01-01T00:00:00Z",
      ...
    }
  ]
}
```

#### GET /api/v1/tasks/{taskId}/graph
**用途**：返回完整知识图谱

**响应**：
```json
{
  "nodes": [...],
  "edges": [
    {
      "task_id": "task-456",
      "src_id": "node-1",
      "rel": "GENERATES",
      "dst_id": "node-2",
      "created_at": "2024-01-01T00:00:00Z"
    }
  ]
}
```

---

## 📁 新增/修改的文件

### 新增文件
1. **internal/httpapi/knowledge_graph_handler.go** (113 行)
   - 定义 KnowledgeGraphAPI 接口
   - 实现 3 个 HTTP handler

2. **internal/httpapi/knowledge_graph_handler_test.go** (154 行)
   - 单元测试覆盖 3 个端点
   - 使用 mock 实现，无需数据库

### 修改文件
1. **internal/knowledgegraph/adapter.go** (+93 行)
   - `ListNodesForAPI()` - 按 task_id 和可选 kind 查询节点
   - `ListEdgesForAPI()` - 按 task_id 查询边（通过节点过滤）
   - `GetStatsForAPI()` - 返回节点类型统计

2. **internal/httpapi/server.go** (+7 行)
   - 在 Deps 中添加 `KnowledgeGraph` 字段
   - 注册 3 个路由

3. **cmd/api/main.go** (+5 行)
   - 初始化 GraphStore 和 KnowledgeGraph adapter
   - 在 httpapi.Deps 中注入

4. **scripts/dev/e2e.sh** (+3 行)
   - 导出 GLM_API_KEY
   - 导出 LIUSHA_LLM_FALLBACK
   - 导出 LIUSHA_LLM_KEY_SECRET

---

## 🔧 技术实现细节

### 1. 边查询的实现挑战

**问题**：`core.GraphEdgeQuery` 没有 `Filters` 字段，无法直接按 task_id 过滤边

**解决方案**：
```go
// 两步查询
1. 查询该 task 的所有节点，收集节点 ID
2. 查询所有边，过滤出源节点或目标节点在节点集合中的边
```

**性能考虑**：
- 对于单个 task 的边数量（通常 < 1000），性能可接受
- 未来可优化：在数据库层面添加 task_id 索引

### 2. 接口设计

**为什么用窄接口 KnowledgeGraphAPI？**
- 依赖倒置：httpapi 不依赖具体的 knowledgegraph 实现
- 易测试：可以用 mock 实现，无需数据库
- 清晰边界：只暴露 HTTP API 需要的方法

### 3. 统计实现

**为什么不直接查数据库聚合？**
```go
// 当前实现：查所有节点 → 内存统计
nodes, _ := ListNodesForAPI(ctx, taskID, "")
for _, node := range nodes {
    switch node.Kind {
    case core.KindObjective: stats["objectives"]++
    // ...
    }
}
```

**原因**：
- 复用现有的 ListNodesForAPI
- 代码简单，易维护
- 性能足够（单个 task 节点数 < 10000）

**未来优化**：
```sql
-- 如果性能成为瓶颈，可以用 SQL 聚合
SELECT kind, COUNT(*) 
FROM wm_node 
WHERE metadata->>'task_id' = $1 
GROUP BY kind;
```

---

## ✅ 测试验收

### 单元测试
```bash
$ go test -v ./internal/httpapi -run "TestGetTask"
=== RUN   TestGetTaskStats
--- PASS: TestGetTaskStats (0.00s)
=== RUN   TestGetTaskNodes
--- PASS: TestGetTaskNodes (0.00s)
=== RUN   TestGetTaskGraph
--- PASS: TestGetTaskGraph (0.00s)
PASS
ok      github.com/V3teran/liusha/internal/httpapi      0.018s
```

### 编译验收
```bash
$ go build -o /tmp/liusha-api ./cmd/api
# 编译通过，无错误
```

---

## 📊 下一步：Phase 2（E2E 测试重构）

### 预期工作量：1-2 周

#### Week 1: 重构验收标准
- [ ] 新建 `cmd/e2e/models.go`（GraphStats, Path）
- [ ] 新建 `cmd/e2e/poller.go`（轮询逻辑）
- [ ] 新建 `cmd/e2e/client.go`（HTTP 客户端）
- [ ] 重构 `cmd/e2e/profiles.go`（新验收标准）

#### Week 2: 更新所有 Profile
- [ ] 更新 13 个 passive profiles
- [ ] 更新 5 个 active profiles
- [ ] 端到端测试和调试

---

## 🎯 关键设计决策回顾

### 1. 为什么是 5 个节点类型？
```
objective → action → observation → evaluation → result
   ↓          ↓           ↓            ↓           ↓
 目标      动作        观察         评估       确认发现
Planner  Executor    Executor   Evaluator  (写 finding 表)
```

**evaluation 的关键作用**：
- 晋升门：只有通过验证的 observation 才能晋升为 result
- 防误报：避免 Executor "猜"出的漏洞直接进库
- 可追溯：每个 result 都能追溯到验证过程

### 2. 为什么验收标准要看知识图谱？

**旧标准的盲区**：
```
✗ 只看 finding 数量
→ LLM 可能"猜"对答案但没有推理链
→ 无法诊断卡在哪一步
```

**新标准的优势**：
```
✓ 验证完整认知循环
→ 确保每个 result 都有推理链
→ 出错时能追溯到具体环节
```

### 3. API 设计哲学

**最小化原则**：
- `/stats` - 只返回统计数字（轻量）
- `/nodes` - 可选过滤（按需）
- `/graph` - 完整数据（重量）

**向后兼容**：
- 旧的 `/tasks/{id}/findings` 端点保留
- Phase 3 可以让它从 `wm_node` 读取（kind='result'）
- 前端无感知切换

---

## 📖 参考文档

- [完整迁移计划](docs/E2E_MIGRATION_PLAN_UPDATED.md)
- [Phase 0 总结](PHASE0_SUMMARY.md)
- [数据库迁移 0134](db/migrations/0134_add_framework_node_kinds.up.sql)

---

## 🚀 如何使用新 API

### 本地测试
```bash
# 1. 启动服务
./scripts/dev/run-svc.sh

# 2. 运行一个扫描任务
curl -X POST http://localhost:8090/chat \
  -H "X-API-Key: changeme-dev-key" \
  -H "Content-Type: application/json" \
  -d '{"brief": "测试 DVWA XSS"}'

# 返回：{"conversation_id": "xxx", "task_id": "yyy"}

# 3. 轮询统计
curl -H "X-API-Key: changeme-dev-key" \
  http://localhost:8090/api/v1/tasks/{task_id}/stats

# 4. 查看完整图谱
curl -H "X-API-Key: changeme-dev-key" \
  http://localhost:8090/api/v1/tasks/{task_id}/graph

# 5. 只看 objective 节点
curl -H "X-API-Key: changeme-dev-key" \
  "http://localhost:8090/api/v1/tasks/{task_id}/nodes?kind=objective"
```

### E2E 集成（Phase 2）
```go
// cmd/e2e/poller.go
func pollTaskCompletion(ctx context.Context, taskID string) error {
    for {
        stats, _ := client.GetTaskStats(taskID)
        
        if stats.Objectives >= 1 && 
           stats.Actions >= 5 && 
           stats.Results >= 1 {
            return nil // PASS
        }
        
        time.Sleep(15 * time.Second)
    }
}
```

---

**Phase 1 完成时间**：2024-XX-XX
**下一步**：开始 Phase 2 E2E 测试重构
