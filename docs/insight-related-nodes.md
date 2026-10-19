# Insight Related Nodes 功能使用指南

## 概述

`insight.related_nodes` 字段用于追溯情报来源，记录产生该情报的探索图节点 ID。

## 数据模型

```go
type Insight struct {
    // ... 其他字段
    RelatedNodes []string `json:"related_nodes,omitempty"` // 关联的探索图节点 ID
}
```

## 使用方法

### 1. Agent 使用 write_insight 工具

```json
{
  "tool": "write_insight",
  "args": {
    "category": "behavior",
    "summary": "发现管理员后台存在未授权访问",
    "body": "在探索过程中发现 /admin 路径无需认证即可访问",
    "priority": "high",
    "confidence": "confirmed",
    "tags": ["auth", "admin"],
    "related_nodes": ["obs-1234", "result-5678"]
  }
}
```

### 2. 程序化创建

```go
import "github.com/V3teran/liusha/internal/insight"

store := insight.NewStore(pool)

ins := insight.Insight{
    Category:     insight.CategoryIssue,
    Priority:     insight.PriorityHigh,
    Confidence:   insight.ConfidenceConfirmed,
    Summary:      "发现 SQL 注入漏洞",
    Body:         "登录接口存在 SQL 注入",
    RelatedNodes: []string{"action-auth-test", "result-sqli"},
    SourceTaskID: taskID,
}

err := store.Append(ctx, assignmentID, ins)
```

### 3. 按节点查询情报

```go
// 查询包含特定节点的所有情报
insights, err := store.ListByRelatedNode(ctx, assignmentID, "obs-1234", 10)
if err != nil {
    log.Fatal(err)
}

for _, ins := range insights {
    fmt.Printf("情报: %s (节点: %v)\n", ins.Summary, ins.RelatedNodes)
}
```

## 应用场景

### 1. 探索图可视化
- 给定一个探索节点，显示从它派生的所有情报
- 构建节点 → 情报的追溯链

### 2. 情报溯源
- 查看某个情报是基于哪些观察/结果/行动产生的
- 验证情报的证据链

### 3. 影响分析
- 删除探索节点时，找到受影响的情报
- 节点更新时，触发相关情报的重新评估

## 性能优化

已为 `related_nodes` 添加 GIN 索引（迁移 0170）：

```sql
CREATE INDEX idx_insight_related_nodes 
ON insight USING GIN (related_nodes);
```

查询性能：
- 无索引：O(n) 全表扫描
- GIN 索引：O(log n) 索引查找

## 数据示例

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "category": "issue",
  "priority": "high",
  "confidence": "confirmed",
  "summary": "发现 XSS 漏洞",
  "body": "搜索框未对用户输入进行转义",
  "tags": ["xss", "frontend"],
  "related_nodes": [
    "obs-search-form",
    "action-xss-test-payload",
    "result-alert-executed"
  ],
  "source_task_id": "task-123",
  "source_agent_run_id": "run-456",
  "created_at": "2026-10-19T10:00:00Z"
}
```

## 注意事项

1. **节点 ID 格式**
   - 建议格式：`{type}-{desc}`，如 `obs-1234`、`action-test`、`result-sqli`
   - 保持一致性便于追溯

2. **数组为空**
   - 默认值为空数组 `[]`
   - 不强制要求填充（某些情报可能无具体节点关联）

3. **节点生命周期**
   - 节点删除不会自动清理 `related_nodes`
   - 需要业务层处理孤儿引用（如有需要）

## 测试

运行集成测试：

```bash
go test -v ./internal/insight -run TestStore_ListByRelatedNode
```

## 迁移

已应用的迁移：
- `0169_finalize_agent_run_naming.up.sql` - 添加字段
- `0170_insight_related_nodes_index.up.sql` - 添加 GIN 索引

检查迁移状态：

```bash
make migrate
```

## 相关文档

- [WORK_SUMMARY.md](./WORK_SUMMARY.md) - 完整的工作总结
- [db/migrations/0169_finalize_agent_run_naming.up.sql](./db/migrations/0169_finalize_agent_run_naming.up.sql) - 字段迁移
- [db/migrations/0170_insight_related_nodes_index.up.sql](./db/migrations/0170_insight_related_nodes_index.up.sql) - 索引优化
