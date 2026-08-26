# 待办事项

**最后更新**: 2026-08-27

---

## 🚀 P0: 立即执行

### ❌ 无

所有关键修正已完成并提交。

---

## 📋 P1: 短期（1-2 周）

### 1. 运行 P2 验证脚本

**优先级**: 🔴 高  
**状态**: 待执行  
**负责人**: 待分配

**任务**：
```bash
# 前置条件：PostgreSQL 运行在 localhost:5432
createdb liusha_test
./scripts/p2_runtime_verification.sh
```

**验证内容**：
- PostgreSQL 连接
- Migrations 执行
- 表结构验证（lead + wm_node + wm_edge）
- Lead 黑板隔离测试（4 个测试）
- 世界模型隔离测试（3 个测试）

**完成标准**：
- ✅ 所有集成测试通过
- ✅ 无数据库错误
- ✅ 隔离边界验证通过

---

### 2. 重新实现 Ledger 适配层

**优先级**: 🟡 中  
**状态**: 待设计  
**负责人**: 待分配

**背景**：
- 旧 ledger 适配层依赖已废弃的 planning.Move
- 新世界模型已完成，需要基于新接口重新实现

**设计方向**：
```go
// internal/worldmodel/ledger_adapter.go
type LedgerStore interface {
    RecordMove(ctx context.Context, move Node) error
    RecordObservation(ctx context.Context, obs Node) error
    RecordDiscovery(ctx context.Context, disc Node) error
    QueryHistory(ctx context.Context, taskID string) ([]Node, error)
}

func (s *Store) AsLedgerStore() LedgerStore {
    return &ledgerAdapter{s}
}
```

**完成标准**：
- ✅ 实现 AsLedgerStore 接口
- ✅ Actor 基础设施可以使用 ledger 功能
- ✅ 单元测试覆盖

---

## 📊 P2: 中期（1 个月）

### 1. 性能验证

**优先级**: 🟢 中低  
**状态**: 待计划  
**负责人**: 待分配

**测试场景**：
1. 大规模 task 隔离性能（1000+ task 并发）
2. Lead 黑板并发读写（100+ QPS）
3. 世界模型查询优化（复杂图查询）

**完成标准**：
- ✅ 性能基准报告
- ✅ 瓶颈识别 + 优化方案
- ✅ 监控告警配置

---

### 2. 世界模型可视化优化

**优先级**: 🟢 低  
**状态**: 待设计  
**负责人**: 待分配

**背景**：
- 当前 attack_graph API 已支持 4 种 NodeKind
- 前端需要适配新的 DTO 结构

**任务**：
1. 前端适配新 API
2. 可视化 4 种节点类型（objective/move/observation/discovery）
3. 边关系渲染（produces/supports/blocks/enables）
4. 状态可视化（Move: open/running/done，Discovery: confidence）

**完成标准**：
- ✅ 前端完整渲染攻击图
- ✅ 支持交互式查询（点击节点查看详情）
- ✅ 实时更新（SSE 推送）

---

## 🔬 P3: 长期（3 个月+）

### 1. 多租户优化

**优先级**: 🟢 低  
**状态**: 待研究  
**负责人**: 待分配

**方向**：
- task_id 分区优化（PostgreSQL 分区表）
- Lead 黑板索引优化（assignment_id + created_at 复合索引）
- 世界模型查询缓存（Redis 缓存热点图）

---

### 2. 分布式调度

**优先级**: 🟢 低  
**状态**: 待研究  
**负责人**: 待分配

**方向**：
- ExecutionLoop 水平扩展
- Move 分布式锁（Redis）
- EventBus 跨进程广播

---

### 3. AI 增强

**优先级**: 🟢 低  
**状态**: 待研究  
**负责人**: 待分配

**方向**：
- PlannerAgent 的 few-shot 学习
- 世界模型知识蒸馏
- 自动 Complexity 评估

---

## ✅ 已完成

### 2026-08-27: 架构一致性重构

- ✅ Actor 层废除 MoveKind，统一使用 Complexity
- ✅ 明确 Assignment vs Task 隔离边界
- ✅ Lead 黑板语义修正（长期记忆）
- ✅ 表名修正（leads → lead）
- ✅ 集成测试编写
- ✅ 文档完善（ARCHITECTURE.md + DATA-FLOW.md）
- ✅ 提交代码（commit 481ed0a3）

### 2026-08-26: 世界模型统一重构

- ✅ 数据库 Migration 0121（wm_node + wm_edge + wm_verification）
- ✅ 核心包重构（worldmodel/cognition/verifier/planneragent）
- ✅ cmd/runner 适配
- ✅ API 层更新（attack_graph_handler）
- ✅ 单元测试通过（verifier: 5/5）
- ✅ 编译验证通过
- ✅ 文档完善（COMPLETION-SUMMARY.md）

---

## 📌 注意事项

### 不要做的事情

1. ❌ **不要混用 Assignment 和 Task**
   - Lead 黑板用 `assignment_id`
   - 世界模型用 `task_id`

2. ❌ **不要再提 MoveKind**
   - Actor 层已废除 MoveKind
   - 统一使用 Complexity 驱动

3. ❌ **不要再写"短期记忆"**
   - Lead 黑板是长期持久化
   - 无 TTL，按 assignment 隔离

4. ❌ **不要强制统一 Finding 和 Discovery**
   - Discovery = 认知层（世界模型节点）
   - Finding = 业务层（漏洞报告）
   - 两者有映射关系但不等价

---

## 📚 参考文档

- [ARCHITECTURE.md](../docs/ARCHITECTURE.md) - 架构文档
- [DATA-FLOW.md](../docs/DATA-FLOW.md) - 数据流图
- [REFACTOR-SUMMARY-20260827.md](../docs/REFACTOR-SUMMARY-20260827.md) - 重构总结
- [COMPLETION-SUMMARY.md](../docs/COMPLETION-SUMMARY.md) - 世界模型重构总结
- [P2 验证脚本](../scripts/p2_runtime_verification.sh) - 运行时验证
