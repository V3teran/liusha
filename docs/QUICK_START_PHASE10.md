# Phase 10 快速启动指南

> **目标**：让业务 Agent 实现 Framework 接口，预计 2-3 天完成。

---

## 📋 前置检查

在开始之前，确认以下条件：

- [ ] 已完成 Phase 1-9（Framework 核心已实现）
- [ ] 熟悉现有业务代码（`internal/planner`, `internal/executor`, 等）
- [ ] 理解 `internal/framework/core.Agent` 接口

---

## 🎯 Phase 10 目标

创建 4 个适配器，让业务 Agent 可以被 Framework Runtime 管理：

```
业务 Agent          →    适配器         →   Framework 接口
──────────────────       ────────────       ───────────────
planner.Agent       →  PlannerAdapter   →  core.Agent ✅
executor.Pool       →  ExecutorAdapter  →  core.Agent ✅
monitor.Agent       →  MonitorAdapter   →  core.Agent ✅
evaluator.Agent     →  EvaluatorAdapter →  core.Agent ✅
```

---

## 🚀 实施步骤

### Step 1：创建目录结构（1 分钟）

```bash
# 在项目根目录执行
mkdir -p internal/framework/adapters
cd internal/framework/adapters
```

### Step 2：实现 PlannerAdapter（30 分钟）

创建 `planner_adapter.go`：

```go
// internal/framework/adapters/planner_adapter.go
package adapters

import (
	"context"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/planner"
)

// PlannerAdapter 把业务 Planner 适配成 framework.Agent。
type PlannerAdapter struct {
	agent *planner.Agent
}

// NewPlannerAdapter 创建 Planner 适配器。
func NewPlannerAdapter(agent *planner.Agent) *PlannerAdapter {
	return &PlannerAdapter{agent: agent}
}

// Name 实现 core.Agent 接口。
func (a *PlannerAdapter) Name() string {
	return "planner"
}

// Run 实现 core.Agent 接口，调用业务 Agent 的原有逻辑。
func (a *PlannerAdapter) Run(ctx context.Context) error {
	return a.agent.Start(ctx)
}

// Stop 实现 core.Agent 接口。
func (a *PlannerAdapter) Stop(ctx context.Context) error {
	// Planner 目前没有显式停止逻辑，依赖 ctx 取消
	return nil
}

// 编译时检查接口实现
var _ core.Agent = (*PlannerAdapter)(nil)
```

**验证**：

```bash
go build ./internal/framework/adapters
```

### Step 3：实现 MonitorAdapter（30 分钟）

创建 `monitor_adapter.go`：

```go
// internal/framework/adapters/monitor_adapter.go
package adapters

import (
	"context"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/monitor"
)

// MonitorAdapter 把业务 Monitor 适配成 framework.Agent。
type MonitorAdapter struct {
	agent *monitor.Agent
}

// NewMonitorAdapter 创建 Monitor 适配器。
func NewMonitorAdapter(agent *monitor.Agent) *MonitorAdapter {
	return &MonitorAdapter{agent: agent}
}

// Name 实现 core.Agent 接口。
func (a *MonitorAdapter) Name() string {
	return "monitor"
}

// Run 实现 core.Agent 接口。
func (a *MonitorAdapter) Run(ctx context.Context) error {
	return a.agent.Start(ctx)
}

// Stop 实现 core.Agent 接口。
func (a *MonitorAdapter) Stop(ctx context.Context) error {
	return nil
}

// 编译时检查接口实现
var _ core.Agent = (*MonitorAdapter)(nil)
```

**验证**：

```bash
go build ./internal/framework/adapters
```

### Step 4：实现 ExecutorAdapter（45 分钟）

创建 `executor_adapter.go`：

```go
// internal/framework/adapters/executor_adapter.go
package adapters

import (
	"context"

	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/framework/core"
)

// ExecutorAdapter 把业务 Executor Pool 适配成 framework.Agent。
//
// 注意：Executor 是按需调用的，不是持续运行的 Agent。
// 这个适配器主要用于统一生命周期管理（如启动 Pool 的 workers）。
type ExecutorAdapter struct {
	pool *executor.Pool
}

// NewExecutorAdapter 创建 Executor 适配器。
func NewExecutorAdapter(pool *executor.Pool) *ExecutorAdapter {
	return &ExecutorAdapter{pool: pool}
}

// Name 实现 core.Agent 接口。
func (a *ExecutorAdapter) Name() string {
	return "executor_pool"
}

// Run 实现 core.Agent 接口。
// Executor Pool 的 workers 在创建时已启动，这里只需要等待 ctx 取消。
func (a *ExecutorAdapter) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// Stop 实现 core.Agent 接口。
func (a *ExecutorAdapter) Stop(ctx context.Context) error {
	// Pool 的停止由业务层控制（因为是按需调用）
	return nil
}

// 编译时检查接口实现
var _ core.Agent = (*ExecutorAdapter)(nil)
```

**说明**：Executor Pool 不是持续运行的 Agent，而是按需调用的工具。适配器只是为了统一生命周期管理。

**验证**：

```bash
go build ./internal/framework/adapters
```

### Step 5：实现 EvaluatorAdapter（30 分钟）

创建 `evaluator_adapter.go`：

```go
// internal/framework/adapters/evaluator_adapter.go
package adapters

import (
	"context"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/framework/core"
)

// EvaluatorAdapter 把业务 Evaluator 适配成 framework.Agent。
//
// 注意：Evaluator 是按需调用的，不是持续运行的 Agent。
type EvaluatorAdapter struct {
	agent *evaluator.Agent
}

// NewEvaluatorAdapter 创建 Evaluator 适配器。
func NewEvaluatorAdapter(agent *evaluator.Agent) *EvaluatorAdapter {
	return &EvaluatorAdapter{agent: agent}
}

// Name 实现 core.Agent 接口。
func (a *EvaluatorAdapter) Name() string {
	return "evaluator"
}

// Run 实现 core.Agent 接口。
// Evaluator 是按需调用的，这里只需要等待 ctx 取消。
func (a *EvaluatorAdapter) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// Stop 实现 core.Agent 接口。
func (a *EvaluatorAdapter) Stop(ctx context.Context) error {
	return nil
}

// 编译时检查接口实现
var _ core.Agent = (*EvaluatorAdapter)(nil)
```

**验证**：

```bash
go build ./internal/framework/adapters
```

### Step 6：编写单元测试（1 小时）

创建 `adapters_test.go`：

```go
// internal/framework/adapters/adapters_test.go
package adapters

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/monitor"
	"github.com/V3teran/liusha/internal/planner"
)

// TestAdaptersImplementAgentInterface 验证所有适配器都实现了 core.Agent 接口。
func TestAdaptersImplementAgentInterface(t *testing.T) {
	var _ core.Agent = (*PlannerAdapter)(nil)
	var _ core.Agent = (*MonitorAdapter)(nil)
	var _ core.Agent = (*ExecutorAdapter)(nil)
	var _ core.Agent = (*EvaluatorAdapter)(nil)
}

// TestPlannerAdapterName 测试 PlannerAdapter.Name()。
func TestPlannerAdapterName(t *testing.T) {
	// 注意：这里需要真实的 planner.Agent，可能需要 mock
	// 简化测试，只验证适配器结构
	adapter := &PlannerAdapter{}
	if got := adapter.Name(); got != "planner" {
		t.Errorf("Name() = %q, want %q", got, "planner")
	}
}

// TestMonitorAdapterName 测试 MonitorAdapter.Name()。
func TestMonitorAdapterName(t *testing.T) {
	adapter := &MonitorAdapter{}
	if got := adapter.Name(); got != "monitor" {
		t.Errorf("Name() = %q, want %q", got, "monitor")
	}
}

// TestExecutorAdapterName 测试 ExecutorAdapter.Name()。
func TestExecutorAdapterName(t *testing.T) {
	adapter := &ExecutorAdapter{}
	if got := adapter.Name(); got != "executor_pool" {
		t.Errorf("Name() = %q, want %q", got, "executor_pool")
	}
}

// TestEvaluatorAdapterName 测试 EvaluatorAdapter.Name()。
func TestEvaluatorAdapterName(t *testing.T) {
	adapter := &EvaluatorAdapter{}
	if got := adapter.Name(); got != "evaluator" {
		t.Errorf("Name() = %q, want %q", got, "evaluator")
	}
}

// TestAdapterLifecycle 测试适配器的生命周期（Run/Stop）。
func TestAdapterLifecycle(t *testing.T) {
	tests := []struct {
		name    string
		adapter core.Agent
	}{
		// 注意：这些测试需要 mock 或真实的 Agent 实例
		// 这里只是框架示例
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			// Run 应该在 ctx 取消后返回
			if err := tt.adapter.Run(ctx); err != context.DeadlineExceeded && err != context.Canceled {
				t.Errorf("Run() error = %v, want context error", err)
			}
		})
	}
}
```

**运行测试**：

```bash
go test ./internal/framework/adapters -v
```

### Step 7：创建包文档（15 分钟）

创建 `doc.go`：

```go
// Package adapters 提供业务 Agent 到 Framework Agent 接口的适配器。
//
// 适配器模式让现有的业务 Agent（Planner、Executor、Monitor、Evaluator）
// 可以被 Framework Runtime 统一管理，而无需修改业务代码。
//
// 用法示例：
//
//	// 创建业务 Agent
//	plannerAgent := planner.New(config)
//
//	// 包装成 Framework Agent
//	adapter := adapters.NewPlannerAdapter(plannerAgent)
//
//	// 注册到 Framework Runtime
//	runtime.RegisterAgent(adapter)
package adapters
```

---

## ✅ 验收标准

完成以下检查项：

### 编译检查

```bash
# 所有适配器编译通过
go build ./internal/framework/adapters

# 整个项目编译通过
go build ./...
```

### 接口检查

```bash
# 运行单元测试，验证接口实现
go test ./internal/framework/adapters -v

# 预期输出：
# === RUN   TestAdaptersImplementAgentInterface
# --- PASS: TestAdaptersImplementAgentInterface (0.00s)
# === RUN   TestPlannerAdapterName
# --- PASS: TestPlannerAdapterName (0.00s)
# ...
# PASS
```

### 代码审查

- [ ] 每个适配器都有清晰的文档注释
- [ ] `Name()` 返回有意义的名称
- [ ] `Run()` 正确调用业务 Agent 的逻辑
- [ ] `Stop()` 处理清理逻辑（如果需要）
- [ ] 有编译时接口检查：`var _ core.Agent = (*XxxAdapter)(nil)`

---

## 📊 进度追踪

| 任务 | 预计时间 | 状态 |
|------|---------|------|
| 创建目录结构 | 1 分钟 | ⬜ |
| PlannerAdapter | 30 分钟 | ⬜ |
| MonitorAdapter | 30 分钟 | ⬜ |
| ExecutorAdapter | 45 分钟 | ⬜ |
| EvaluatorAdapter | 30 分钟 | ⬜ |
| 单元测试 | 1 小时 | ⬜ |
| 包文档 | 15 分钟 | ⬜ |
| **总计** | **~3.5 小时** | |

---

## 🎉 完成后

- [ ] 提交代码：`git commit -m "feat: Phase 10 - Agent adapters"`
- [ ] 更新 README：说明适配器的作用
- [ ] 通知团队：Phase 10 完成，进入 Phase 11

---

## 🔄 下一步：Phase 11

完成 Phase 10 后，立即开始 [Phase 11：Runtime 集成](./PHASE_10_13_INTEGRATION_PLAN.md#phase-11业务层使用-framework-runtime2周)。

---

## 💡 常见问题

### Q1: Executor 和 Evaluator 不是持续运行的，为什么也要适配？

**A**: 适配器的目的是统一生命周期管理。虽然它们是按需调用的，但 Framework Runtime 可以：
- 统一启动/停止所有组件
- 统一日志和监控
- 统一错误处理

### Q2: 适配器是否会影响性能？

**A**: 不会。适配器只是一层薄包装，几乎零开销。

### Q3: 如果业务 Agent 需要新方法怎么办？

**A**: 保持业务 Agent 的完整接口。适配器只是让它们能被 Framework 管理，不影响业务方法调用。

---

## 📚 参考资料

- [Framework Core 接口文档](../internal/framework/core/agent.go)
- [业务 Planner 实现](../internal/planner/agent.go)
- [业务 Executor 实现](../internal/executor/pool.go)
- [Phase 10-13 完整计划](./PHASE_10_13_INTEGRATION_PLAN.md)
