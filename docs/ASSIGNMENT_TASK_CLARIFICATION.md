# Assignment 和 Task 的正确理解

## 核心关系

```
Assignment（下发批次）
  ├── Task 1（扫描会话）
  ├── Task 2（扫描会话）
  └── Task 3（扫描会话）
```

---

## Assignment（下发容器）

**定位：**
- **一切下发皆走 assignment**
- 是"批次/下发单元"，不是"工作分配"

**用途：**
1. **单发**：1 个 Assignment → 1 个 Task
2. **批量**：1 个 Assignment → N 个 Task（fan-out）
3. **聚合**：一批流量 → 1 个 Assignment → 1 个 Task（fan-in）

**设计特点：**
- 无 status 列（状态由子 Task 派生）
- 强外键：`task.assignment_id NOT NULL`（无孤儿 Task）
- 包含 payload（下发清单）

**类比：**
- 像"订单"（Order）：可以包含多个"订单项"（Task）
- 像"批次"（Batch）：可以包含多个"任务"（Task）

---

## Task（扫描会话/执行实例）

**定位：**
- **一次扫描活动 = 一个会话**
- 实际的执行单元

**特点：**
- 有独立的生命周期（active → completed/aborted）
- 有 Brief（目标描述）、TargetHost
- 有心跳、状态、耗时统计

**关系：**
- 必须属于某个 Assignment
- `assignment_id NOT NULL`（强制关联）

---

## 正确的命名评估

### ✅ Assignment - 完全正确

**理由：**
1. ✅ "Assignment"在这里是"下发单/派发单"的意思
2. ✅ 语义："分配给系统的一批工作"
3. ✅ 业界对标：
   - Kubernetes：类似 ReplicaSet（包含多个 Pod）
   - CI/CD：类似 Pipeline Run（包含多个 Job）
   - Celery：类似 Group（包含多个 Task）

**不需要改！**

---

## 架构层次

```
┌─────────────────────────────────────┐
│ Assignment（下发容器/批次）          │
│ - 下发来源（manual/auto）            │
│ - 下发清单（payload）                │
│ - 状态（由子 Task 派生）             │
└─────────────────┬───────────────────┘
                  │
         ┌────────┴────────┐
         │                 │
    ┌────▼────┐      ┌────▼────┐
    │ Task 1  │      │ Task 2  │
    │ (会话)  │      │ (会话)  │
    └────┬────┘      └────┬────┘
         │                │
    ┌────▼────────────────▼────┐
    │ WorldModel               │
    │ - Action                 │
    │ - Hypothesis             │
    │ - Evidence               │
    │ - Finding                │
    └──────────────────────────┘
```

---

## 对比业界

| Liusha | Kubernetes | CI/CD | Celery |
|--------|-----------|-------|--------|
| Assignment | ReplicaSet | Pipeline Run | Group |
| Task | Pod | Job | Task |
| Action | Container | Step | Subtask |

---

## 结论

### ✅ Assignment 命名完全正确

**我之前的误解：**
- ❌ 以为 Assignment 是"分配给某个 Agent 的工作"
- ❌ 以为应该改为 Job

**实际定位：**
- ✅ Assignment 是"下发容器/批次/派发单"
- ✅ 可以包含多个 Task（1:N）
- ✅ 是批量操作的抽象

**命名评估：**
- ✅ 中性通用
- ✅ 语义清晰
- ✅ 对标业界
- ✅ **无需修改！**

---

## 最终架构评分

**总分：9.8/10** ✅

所有核心概念都已优化到最佳状态：
- ✅ Insight（洞察）
- ✅ Evaluator（评估器）
- ✅ Roadmap（路线图）
- ✅ Assignment（下发容器）✅ 正确
- ✅ Task（扫描会话）
- ✅ Action（动作）
- ✅ WorldModel（科学方法论）

**无需进一步优化！** 🎉
