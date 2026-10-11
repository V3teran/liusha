# 🎯 全局问题修复报告

**日期**: 2024-10-11  
**范围**: 架构级问题修复 + 配置优化

---

## 📋 修复的问题清单

### 1. ✅ Evaluator 验证死锁（P0 - 致命）

#### 问题描述
- **现象**: Evaluator 一直重复 "observation 验证中，跳过并发触发"
- **根本原因**: `tryAcquire` → `sem <- struct{}{}` 的顺序导致死锁
  ```
  1. tryAcquire 成功，标记 verifying[id] = true
  2. sem <- struct{}{} 阻塞（信号量已满）
  3. defer release 永远不会执行
  4. 其他轮询一直看到 verifying[id] = true
  5. 死锁！
  ```

#### 修复方案
1. **调整锁顺序**: 先 tryAcquire，再在 goroutine 中获取信号量
2. **确保 defer 执行**: 在 goroutine 最外层 defer release
3. **增加并发数**: MaxConcurrent 从 3 → 10

#### 文件修改
- `internal/evaluator/agent.go` - dispatchVerification 函数
- `cmd/runner/cognition.go` - MaxConcurrent: 3 → 10

---

### 2. ✅ 删除最大迭代次数限制（P1 - 用户需求）

#### 问题描述
- 4 个 agent 都有 `max_iterations` 限制
- 用户不想设置上限，希望让 agent 自然完成

#### 修复方案
- 将所有 agent 的 `max_iterations` 设置为 0（表示不限制）

#### 文件修改
- `agents/executor.md`: max_iterations: 100 → 0
- `agents/evaluator.md`: max_iterations: 50 → 0
- `agents/planner.md`: max_iterations: 100 → 0
- `agents/monitor.md`: max_iterations: 20 → 0

---

### 3. ✅ 删除 E2E 测试 40 分钟超时（P1 - 用户需求）

#### 问题描述
- E2E 测试硬编码 40 分钟超时
- 用户认为不合理，希望删除限制

#### 修复方案
- `pollDeadline()` 默认返回 0（无超时）
- 保留环境变量 `LIUSHA_E2E_POLL_DEADLINE_SECONDS` 供需要时配置

#### 文件修改
- `cmd/e2e/main.go` - pollDeadline 函数
- `cmd/e2e/poller.go` - 支持 deadline = 0

---

## 🔍 举一反三：全局排查

### 类似死锁问题排查

#### 检查点 1: Planner Agent
```bash
grep -rn "tryAcquire\|mutex.*Lock" internal/planner/
```
**结果**: ✅ 无类似问题，Planner 不使用信号量

#### 检查点 2: Executor Engine
```bash
grep -rn "sem.*<-\|mutex.*Lock" internal/executor/
```
**结果**: ✅ 无类似问题，Executor 是同步执行

#### 检查点 3: Monitor Agent  
```bash
grep -rn "goroutine\|mutex" internal/monitor/
```
**结果**: ✅ 无类似问题，Monitor 是单线程

### 其他潜在问题

#### 问题 1: Action 状态不更新

**发现**: Executor 完成后，exploration_node.state 仍是 `running`

**根本原因**: 
- Executor 不创建 agent_run 记录
- Action 状态依赖人工或其他机制更新
- 没有自动状态流转

**需要进一步调查**:
1. Executor 执行完成后谁负责更新 action.state？
2. 是否应该在 Executor 内部更新？
3. 还是通过事件驱动更新？

#### 问题 2: Planner "等待未完结工作" 死锁

**发现**: Planner 判断应该等待现有 actions 完成，但 actions 永远不完成

**根本原因**: 
- Planner 基于 exploration_node.state 判断
- 但 state 不会自动更新
- 导致 Planner 永远等待

**可能的解决方案**:
1. 添加等待超时（如 5 分钟）
2. 检测 action 的 updated_at，超时则认为失败
3. 改为"乐观规划"策略，不等待完成

---

## 📊 修复效果预期

### Evaluator 死锁修复
- ✅ **立即生效**: 验证不再卡住
- ✅ **并发提升**: 3 → 10，吞吐量提升 3 倍
- ✅ **可靠性**: 即使信号量满，也不会死锁

### 迭代次数限制删除
- ✅ **Agent 自由度提升**: 可以自然完成复杂任务
- ⚠️ **潜在风险**: 可能出现无限循环
- 💡 **建议**: 保留其他超时机制（如整体任务超时）

### E2E 超时删除
- ✅ **测试灵活性**: 不受硬编码时间限制
- ✅ **长任务支持**: 可以运行超过 40 分钟的测试
- ⚠️ **潜在风险**: 测试可能永远不结束
- 💡 **建议**: 在 CI/CD 中设置外部超时

---

## 🔧 需要进一步修复的问题

### P0: Action 状态自动更新机制

**问题**: Executor 完成后 action.state 不更新

**建议方案**:
1. 在 Executor 完成时发布事件
2. 监听事件并更新 exploration_node.state
3. 或在 Executor 内部直接更新

### P1: Planner 等待超时机制

**问题**: Planner 可能永远等待未完成的 actions

**建议方案**:
1. 添加等待超时配置（如 5-10 分钟）
2. 检测 action.updated_at，超时认为失败
3. 强制重规划时生成新 actions

### P2: Agent Run 记录机制统一

**问题**: Executor 不创建 agent_run 记录

**建议方案**:
1. 统一所有 agent 都创建 agent_run
2. 或明确文档说明哪些 agent 创建记录
3. 提供其他方式追踪 executor 执行状态

---

## 📝 代码审查建议

### 信号量模式最佳实践

**错误模式** ❌:
```go
if !tryLock() {
    return
}
defer unlock()

sem <- struct{}{} // 可能阻塞，导致 unlock 永远不执行
defer func() { <-sem }()
```

**正确模式** ✅:
```go
if !tryLock() {
    return
}

go func() {
    defer unlock() // 确保在最外层

    select {
    case sem <- struct{}{}:
        defer func() { <-sem }()
    case <-ctx.Done():
        return
    }
    
    // 实际工作
}()
```

### 并发原则

1. **锁的粒度**: 尽量小，避免持有锁时阻塞
2. **Defer 顺序**: 关键资源的释放放在最外层
3. **信号量**: 不要在持有锁时获取信号量
4. **Context**: 所有阻塞操作支持 ctx.Done()

---

## 🎯 验证步骤

### 1. 验证 Evaluator 修复
```bash
# 重新运行之前失败的测试
./scripts/dev/e2e.sh active:xss

# 检查日志，应该看到：
# - "开始验证 observation" （不会卡住）
# - 验证完成后生成 result 节点
```

### 2. 验证迭代次数
```bash
# 检查 agent 配置
grep "max_iterations" agents/*.md

# 应该全部是 0
```

### 3. 验证超时删除
```bash
# 不设置环境变量运行测试
./scripts/dev/e2e.sh active:xss

# 应该没有 "轮询超时" 错误
```

---

## 📚 相关文档

1. **根因分析**: `docs/e2e-root-cause-analysis.md`
2. **测试报告**: `docs/e2e-test-final-report.md`
3. **架构审查**: `docs/schema-audit-2024.md`

---

**修复完成时间**: 2024-10-11 22:10  
**修复人**: Claude (Kiro)
