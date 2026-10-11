# 🎯 E2E 测试失败根本原因分析报告

**日期**: 2024-10-11  
**测试**: active:xss  
**状态**: ❌ 失败（未达标）

---

## 📊 表面现象

| 指标 | 要求 | 实际 | 状态 |
|------|------|------|------|
| objectives | ≥1 | 1 | ✅ |
| actions | ≥5 | 4 | ❌ |
| results | ≥3 | 0 | ❌ |
| findings | >0 | 0 | ❌ |

---

## 🔍 调查过程

### 错误线索 1：GLM API 429 错误 ❌ 误判

**现象**：日志中出现 3 次 429 Too Many Requests

**调查结果**：✅ 重试机制工作正常
- evaluator: 1次429 → 重试成功（最终1成功/2总调用）
- executor: 2次429 → 重试成功（最终40成功/42总调用）

**结论**：429 不是根本原因！

---

### 错误线索 2：测试超时 ❌ 误判

**现象**：测试在 6 分钟后退出

**调查结果**：
- 配置超时：40 分钟
- 实际运行：9 分 45 秒
- 停滞判定：需连续 10 分钟无进展才算卡住

**结论**：不是超时导致的退出！

---

### 错误线索 3：Planner 卡死 ⚠️ 部分正确

**现象**：
- Planner agent_run 状态: running
- 创建时间: 13:38:54
- 最后更新: 13:38:55
- 空闲时长: 17+ 分钟

**调查结果**：
```
Planner 判断: "4 个在途 Action 已完整覆盖探测面，
应等待本轮探测结果落地后，再根据实际过滤行为规划后续动作。"

强制重规划未生成新 Action，但图上仍有未完结工作——等待其产出，不收敛
```

**分析**：Planner 在"等待" actions 完成，但为什么 actions 永远不完成？

---

## 🎯 真正的根本原因

### 发现 1：2 个 Actions 卡在 `running` 状态

```sql
SELECT id, state, updated_at 
FROM exploration_node 
WHERE kind='action' AND state='running';

-- 结果：
-- DOM XSS (38bd16b7): running, 最后更新 13:42:32
-- Reflected XSS (59b9c4f4): running, 最后更新 13:42:32
```

### 发现 2：这些 Actions 没有对应的 agent_run 记录

```sql
SELECT COUNT(*) FROM agent_run 
WHERE task_id='50a63546-6b92-478b-b400-13896c956ea7' 
  AND role='executor';

-- 结果：0 条记录
```

### 发现 3：但 Executor 实际上在运行！

```
日志显示：
- DOM XSS action: 14 次迭代（最后 21:46:32）
- Reflected XSS action: 12 次迭代（最后 21:46:39）
```

### 🔴 **核心问题：架构设计缺陷**

**Executor 不创建 agent_run 记录！**

#### 证据：
1. ✅ 整个任务只有 1 条 agent_run（planner）
2. ✅ Executor 日志显示执行了 26+ 次迭代
3. ✅ 代码中没有 executor 创建 agent_run 的逻辑

#### 后果：
```
Planner 等待 "未完结工作" 完成
    ↓
Planner 检查 exploration_node.state
    ↓
Actions 卡在 `running` 状态（从未更新为 done）
    ↓
Planner 永远等待
    ↓
无法生成第 5 个 action
    ↓
测试无法达标
```

---

## 💡 **为什么 Actions 的状态不更新？**

### 可能原因：

#### 1. Executor 完成后没有更新 exploration_node 状态
- Executor 执行完 14 次迭代
- 但 exploration_node.state 仍然是 `running`
- 没有调用更新状态为 `done` 的逻辑

#### 2. Evaluator 没有生成 result 节点
- Executor 生成了 2 个 observations
- Evaluator 验证中（日志显示 "observation 验证中"）
- 但没有生成 result 节点

#### 3. 状态机卡住
- Action → Observation → (Evaluator验证) → Result
- 链条在 Evaluator → Result 这一步断了

---

## 🔬 **更深层的问题：Evaluator 为什么没有生成 Result？**

让我检查 Evaluator 的执行情况：

### Evaluator LLM 调用统计：
- 总调用：2 次
- 成功：1 次
- 429 错误：1 次（已重试成功）

### Evaluator 日志：
```
21:45:02 - 发现待裁决 observation (1个)
21:45:02 - observation 验证中，跳过并发触发
21:45:02 - 启动验证任务

... 重复多次 ...

21:46:57 - 发现待裁决 observation
21:46:57 - observation 验证中，跳过并发触发
21:46:57 - 启动验证任务
```

**Evaluator 一直在尝试验证，但从未完成！**

---

## 🎯 **最终结论**

### 根本原因层次：

#### Level 1（表象）：
- ❌ 测试未达标（actions 4/5, results 0/3）

#### Level 2（直接原因）：
- ❌ Planner 没有生成第 5 个 action
- ❌ Evaluator 没有生成 result 节点

#### Level 3（根本原因）：
- ❌ **Evaluator 验证逻辑卡住** - 一直重试验证同一个 observation
- ❌ **Actions 状态不更新** - Executor 完成后状态仍是 running
- ❌ **Planner "基于证据" 策略** - 等待未完结工作，但工作永远不完结

#### Level 4（架构问题）：
- ❌ **Executor 不创建 agent_run** - 无法追踪执行状态
- ❌ **Evaluator 验证死循环** - 没有超时或失败机制
- ❌ **状态更新机制缺失** - Actions 完成后状态不自动更新

---

## 💡 **需要修复的问题**

### Priority 1：让 Evaluator 能够完成验证
```
问题：Evaluator 一直重试同一个 observation
需要：
1. 添加验证超时机制
2. 添加验证失败处理
3. 生成 result 节点（即使验证失败）
```

### Priority 2：修复 Action 状态更新
```
问题：Executor 完成后 action 状态仍是 running
需要：
1. Executor 完成后更新 exploration_node.state = 'done'
2. 或添加状态自动更新机制
```

### Priority 3：改进 Planner "等待" 逻辑
```
问题：Planner 无限等待未完结工作
需要：
1. 添加等待超时
2. 在超时后强制生成新 actions
3. 或改为"乐观规划"策略
```

### Priority 4：恢复 Executor agent_run 记录
```
问题：无法追踪 executor 执行状态
需要：
1. Executor 创建 agent_run 记录
2. 或使用其他机制追踪执行状态
```

---

## 📝 **总结**

**429 错误不是问题** - 重试机制工作正常  
**真正的问题** - Evaluator 验证死循环 + Action 状态不更新  
**导致** - Planner 永远等待 → 无法生成新 actions → 测试失败

**这是一个架构/逻辑问题，不是 API 限制问题！**

---

**报告生成时间**: 2024-10-11 22:00  
**报告生成人**: Claude (Kiro)
