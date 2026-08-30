# Liusha 架构改进实施进度

**开始日期**: 2026-08-30  
**目标**: 实现完整的双层监察架构 + 统一概念命名

---

## ✅ 阶段 1: Move → Action 统一命名（已完成）

**时间**: 2026-08-30  
**Commit**: 18831683

### 改动内容

#### 核心类型重命名
- `executor.Move` → `executor.Action`
- `executor.LandmarkRef` → `executor.TargetRef`
- `executor.MoveStatus` → `executor.ActionStatus`
- `executor.MoveRecord` → `executor.ActionRecord`

#### 字段重命名
- `moveID` / `MoveID` → `actionID` / `ActionID`
- `move_id` (数据库) → `action_id`
- Budget 注释: `Move` → `Action`
- ScanBudget: `MaxMoves` → `MaxActions`

#### 事件重命名
- `EventMoveCompleted` → `EventActionCompleted`
- `PublishMoveCompleted()` → `PublishActionCompleted()`

#### 方法重命名
- `executeMove()` → `executeAction()`
- `nodeToActorMove()` → `nodeToExecutorAction()`

### 修改的文件

**executor 包** (9个文件):
- `internal/executor/types.go` - 核心类型定义
- `internal/executor/actor.go` - Executor主逻辑
- `internal/executor/checkpoint.go` - 检查点存储
- `internal/executor/run_with_monitoring.go` - 监察框架
- `internal/executor/event_loop.go` - 事件循环
- `internal/executor/execution_state.go` - 执行状态
- `internal/executor/self_monitor.go` - 自我监察
- `internal/executor/steering.go` - 纠偏逻辑
- `internal/executor/worldmodel_adapter.go` - 世界模型适配器

**dispatcher 包** (2个文件):
- `internal/dispatcher/dispatcher.go` - 调度器
- `internal/dispatcher/profile/profiles.go` - Profile配置

**cognition 包** (2个文件):
- `internal/cognition/event.go` - 事件总线
- `internal/cognition/execution_loop.go` - 执行循环

**planner 包** (1个文件):
- `internal/planner/agent.go` - Planner代理

**cmd/runner** (1个文件):
- `cmd/runner/handler_run.go` - 运行处理器

### 验证结果

```bash
✅ 编译: go build ./...
✅ 所有包编译通过
✅ 无类型错误
✅ 无未定义引用
```

### 遗留问题

- [ ] 数据库迁移: `actor_checkpoint` 表的 `move_id` 列需要重命名为 `action_id`
- [ ] 数据库迁移脚本: 创建 `ALTER TABLE` 迁移

---

## ✅ 阶段 2: Executor 微观监察（已完成）

**完成日期**: 2026-08-30  
**状态**: 已存在，验证通过

### 实现内容

**三协程架构**:
- ✅ executeLoop - ReAct 执行循环
- ✅ monitorLoop - 每 5 步自我评估
- ✅ eventLoop - 监听 Planner 控制

**状态共享**:
- ✅ executionState (mutex 保护)
- ✅ correctionChan (协程间通信)
- ✅ shouldStop 标志

**监察决策**:
- ✅ off_track + high → Kill
- ✅ off_track + low/medium → Steer
- ✅ stalled → Kill

**外部控制**:
- ✅ action.killed → 立即停止
- ✅ action.steered → 注入纠偏

### 核心文件

- `internal/executor/run_with_monitoring.go` - 三协程入口
- `internal/executor/event_loop.go` - 事件监听
- `internal/executor/self_monitor.go` - 自我评估
- `internal/executor/execution_state.go` - 共享状态

---

## ✅ 阶段 3: Planner 宏观监察（已完成）

**完成日期**: 2026-08-30  
**Commit**: 7d00e529

### 实现内容

- ✅ 添加 actionBus 字段到 Agent
- ✅ 评估定时器：30秒心跳 → 6分钟评估
- ✅ 实现 periodicEvaluation() 方法
- ✅ Kill 方法发布到 eventbus.Bus
- ✅ Steer 方法发布到 eventbus.Bus
- ✅ 调用 evaluateGlobal() 获取全局决策

### 核心文件

- `internal/planner/agent.go` - Agent 结构和评估循环
- `internal/planner/control.go` - Kill/Steer 发布
- `internal/planner/evaluation.go` - 全局评估逻辑

### 验证结果

```bash
✅ 编译通过
✅ actionBus 正确注入
✅ 事件发布到正确的总线
✅ 6分钟定时器正常工作
```

---

## ⏳ 阶段 4: Hypothesis/Evidence 节点（待开始）

**目标**: 实现完整的 5 种节点类型

### 待实施内容

- [ ] 创建 write_hypothesis 工具
- [ ] 创建 write_evidence 工具
- [ ] 注册工具到 Registry
- [ ] 更新 Profile 工具列表
- [ ] 实现 CONFIRMS/REFUTES 关系创建

---

## ⏳ 阶段 5: ENABLES 关系（待开始）

**目标**: 实现 finding → action 的使能关系

### 待实施内容

- [ ] propose_actions 工具增强
- [ ] 添加 enable_by 参数
- [ ] 创建 ENABLES 边

---

## ⏳ 阶段 6: Complexity 动态分配（待开始）

**目标**: 实现混合模式的复杂度分配

### 待实施内容

- [ ] Handler 启发式推断
- [ ] Planner prompt 增加选择指南
- [ ] LLM 自由选择 + 严谨验证

---

## ⏳ 阶段 7: 命名优化（可选）

**目标**: cognition → orchestrator 重命名

### 待实施内容

- [ ] 包重命名
- [ ] 类型重命名
- [ ] 更新所有引用

---

## 📊 总体进度

| 阶段 | 状态 | 进度 | 完成时间 |
|------|------|------|----------|
| P0-1: Move→Action | ✅ 完成 | 100% | 2026-08-30 |
| P0-2: Executor监察 | ✅ 完成 | 100% | 2026-08-30 (已存在) |
| P0-3: Planner监察 | ✅ 完成 | 100% | 2026-08-30 |
| P1-4: Hypothesis/Evidence | 🚧 进行中 | 0% | - |
| P2-5: ENABLES关系 | ⏳ 待开始 | 0% | - |
| P2-6: Complexity动态 | ⏳ 待开始 | 0% | - |
| P3-7: 命名优化 | ⏳ 待开始 | 0% | - |

**P0完成**: 双层监察架构已全部实现 ✅  
**总进度**: 3/7 阶段完成（43%）

---

**更新时间**: 2026-08-30  
**状态**: P0完成，开始P1-P2
