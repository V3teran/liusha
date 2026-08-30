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

## 🚧 阶段 2: Executor 微观监察（进行中）

**目标**: 实现 Executor 每 5 步自我评估的监察循环

### 当前状态

**已有基础**:
- ✅ `executor/config.go` - MonitorConfig 定义
- ✅ `executor/self_monitor.go` - selfEvaluate() 实现
- ✅ `executor/run_with_monitoring.go` - monitorLoop() 框架
- ✅ `executor/event_loop.go` - eventLoop() 框架

**待实现**:
- [ ] 启动三协程模式（executeLoop + monitorLoop + eventLoop）
- [ ] 连接 monitorLoop 到 selfEvaluate()
- [ ] 连接 eventLoop 到 EventBus
- [ ] 实现 correctionChan 通信
- [ ] 实现 Kill/Steer 注入逻辑

### 实施计划

#### Step 2.1: 修改 Run 方法
- [ ] 添加 monitorEnabled 判断
- [ ] 实现 runWithMonitoring() 三协程启动
- [ ] 保留 runSingleThreaded() 向后兼容

#### Step 2.2: 实现 monitorLoop
- [ ] 每 N 步触发评估
- [ ] 调用 selfEvaluate()
- [ ] 根据评估结果决策：
  - 状态 = off_track + 严重度 = high → Kill
  - 状态 = off_track + 严重度 = low/medium → Steer
  - 状态 = on_track → 继续

#### Step 2.3: 实现 eventLoop
- [ ] 订阅 eventBus (action.killed / action.steered)
- [ ] 接收 Planner 的 Kill/Steer 事件
- [ ] 转发到 executeLoop

#### Step 2.4: 实现 executeLoop 响应
- [ ] 监听 correctionChan
- [ ] 注入 [STEERING] 消息
- [ ] 检查 stopped 标志

---

## ⏳ 阶段 3: Planner 宏观监察（待开始）

**目标**: 实现 Planner 每 6 分钟全局评估

### 待实施内容

- [ ] Planner.Start() 添加评估定时器（6分钟）
- [ ] 实现 periodicEvaluation() 方法
- [ ] 调用 evaluateGlobal() 获取决策
- [ ] Kill/Steer 方法发布到 eventbus.Bus
- [ ] 依赖注入 actionBus

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

| 阶段 | 状态 | 进度 | 预计完成 |
|------|------|------|----------|
| P0-1: Move→Action | ✅ 完成 | 100% | 2026-08-30 |
| P0-2: Executor监察 | 🚧 进行中 | 0% | - |
| P0-3: Planner监察 | ⏳ 待开始 | 0% | - |
| P1-4: Hypothesis/Evidence | ⏳ 待开始 | 0% | - |
| P2-5: ENABLES关系 | ⏳ 待开始 | 0% | - |
| P2-6: Complexity动态 | ⏳ 待开始 | 0% | - |
| P3-7: 命名优化 | ⏳ 待开始 | 0% | - |

**总进度**: 1/7 阶段完成（14%）

---

**更新时间**: 2026-08-30  
**状态**: 阶段1完成，开始阶段2
