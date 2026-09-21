# 架构修复计划

## 修复优先级

### ✅ 已完成

#### 持久化层统一
- 创建 `internal/framework/persistence` 包
  - GraphStore: 知识图谱存储接口
  - EventStore: 事件流存储接口  
  - MemoryStore: 内存实现（开发/测试用）

#### P0 高优先级问题

✅ **1. evaluator/verifier.go 类型错误**
- 位置: `internal/evaluator/verifier.go:167`
- 修复: 改为 `(node, attempt Attempt, result Result)`

✅ **2. AdapterStore.pool 未初始化**
- 位置: `internal/knowledgegraph/adapter.go`
- 修复: 保留 pool 字段但添加注释说明仅在 NewStore 中设置

✅ **3. Action状态更新无CAS保证**
- 位置: `internal/executor/executor_agent.go:executeAction()`
- 修复: 
  - 添加 `GraphStore.CompareAndSwapState` 接口方法
  - 添加 `AdapterStore.CompareAndSwapActionState` 方法
  - ExecutorAgent 使用 CAS 防止并发执行同一 Action
  - 实现了内存版和 PostgreSQL 版的 CAS

✅ **4. Planner 死锁风险**
- 位置: `internal/planner/planner_agent.go:planActions()`
- 修复: 改为检查 executableActions 而不是仅检查 openActions
- 如果所有 open actions 都被依赖阻塞，记录警告并尝试生成新规划

✅ **5. Node.CanExecute() 方法存在性检查**
- 状态: 已确认存在于 `internal/knowledgegraph/model.go:175-191`
- 无需修复

✅ **9. 事件发布顺序**
- 位置: `internal/executor/executor_agent.go:executeAction()`
- 修复: 先发布所有 AttemptGenerated 事件，最后发布 ActionCompleted

### 🔄 进行中

#### P1 中优先级问题

⏳ **6. 类型系统统一**
- 问题: Framework 和业务层类型重复
- 计划: 统一使用 `core.NodeKind`, `core.ActionState` 等
- 需要重构:
  - knowledgegraph.State → core.ActionState
  - knowledgegraph.Confidence → core.ObservationConfidence
  - knowledgegraph.Priority → core.Priority

⏳ **7. 事件Payload类型安全**
- 问题: `event.Payload` 是 `map[string]interface{}`
- 计划: 定义强类型事件结构
- 需要重构所有事件发布和订阅代码

⏳ **10. GraphStore 接口增强**
- 问题: 缺少批量操作、事务支持
- 计划: 扩展接口定义
- 已添加: CompareAndSwapState
- 待添加: BatchCreate, Transaction, ComplexQuery

### 🟢 待处理

#### P2 低优先级问题

📋 **11. 错误处理统一**
- 制定错误处理规范
- 定义 ErrorLevel (Fatal/Recoverable/Ignorable)

📋 **老架构残留清理**
- 清理所有废弃代码
- 更新注释和文档
- 确保所有代码使用新架构

## 实施进度

1. ✅ 持久化层统一
2. ✅ 修复 P0 问题（1-5, 9）
3. 🔄 类型系统统一（6）
4. 🔄 事件系统改进（7）
5. 🔄 接口增强（10）
6. 📋 清理老架构残留
7. 📋 错误处理规范（11）

## 验证标准

- ✅ 所有代码编译通过
- ⏳ 单元测试通过
- ⏳ 集成测试通过
- ⏳ 无编译警告
- ⏳ 无老架构代码残留
- ⏳ 文档更新完成

## 已修复的关键问题

### 并发安全性
- ✅ ExecutorAgent 使用 CAS 保证同一 Action 不会被多个 executor 并发执行
- ✅ 状态转换原子性得到保证

### 死锁预防
- ✅ Planner 现在检查可执行性而不是仅检查 open 状态
- ✅ 避免了所有 action 被依赖阻塞导致的死锁

### 事件时序
- ✅ 先发布 Attempt，后发布 Completed，保证 Evaluator 收到完整信息

### 持久化抽象
- ✅ 统一的持久化层接口
- ✅ 内存和 PostgreSQL 实现
- ✅ 支持测试和生产环境

## 下一步工作

1. 运行测试套件验证修复
2. 开始类型系统统一重构
3. 清理老架构残留代码
4. 更新架构文档

