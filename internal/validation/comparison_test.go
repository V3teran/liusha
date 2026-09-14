package validation

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScenario1_BasicFlow 场景1：基础流程
// Planner 生成 10 个 Actions → Executor 顺序执行 → Evaluator 发现 1 个漏洞
func TestScenario1_BasicFlow(t *testing.T) {
	ctx := context.Background()

	t.Run("LangGraph style", func(t *testing.T) {
		start := time.Now()

		// 创建 StateGraph
		graph := NewStateGraph("scan target system")

		// 运行
		finalState, err := graph.Run(ctx)
		require.NoError(t, err)

		duration := time.Since(start)

		// 验证结果
		assert.Equal(t, 10, finalState.Stats.TotalActions, "应生成 10 个 Actions")
		assert.Equal(t, 10, finalState.Stats.CompletedActions, "应完成 10 个 Actions")
		assert.Equal(t, 1, finalState.Stats.TotalFindings, "应发现 1 个漏洞")

		t.Logf("LangGraph style 完成:")
		t.Logf("  - 总耗时: %v", duration)
		t.Logf("  - Actions: %d/%d", finalState.Stats.CompletedActions, finalState.Stats.TotalActions)
		t.Logf("  - 漏洞: %d", finalState.Stats.TotalFindings)
	})

	t.Run("Knowledge Graph style", func(t *testing.T) {
		// 创建内存 GraphStore（用于测试）
		graphStore := core.NewInMemoryGraphStore()

		start := time.Now()

		// 创建知识图谱编排器
		orchestrator := NewKnowledgeGraphOrchestrator("test-task-1", graphStore)

		// 运行
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		err := orchestrator.Run(ctx)
		require.NoError(t, err)

		duration := time.Since(start)
		stats := orchestrator.GetStats()

		// 验证结果
		assert.Equal(t, 10, stats.TotalActions, "应生成 10 个 Actions")
		assert.Equal(t, 10, stats.CompletedActions, "应完成 10 个 Actions")
		assert.Equal(t, 1, stats.TotalFindings, "应发现 1 个漏洞")

		t.Logf("Knowledge Graph style 完成:")
		t.Logf("  - 总耗时: %v", duration)
		t.Logf("  - 轮询次数: ~%d", int(duration.Milliseconds()/100))
		t.Logf("  - Actions: %d/%d", stats.CompletedActions, stats.TotalActions)
		t.Logf("  - 漏洞: %d", stats.TotalFindings)
	})
}

// TestScenario2_ParallelExecution 场景2：并行执行
// Planner 生成 10 个 Actions → 3 个 Executors 并行执行
func TestScenario2_ParallelExecution(t *testing.T) {
	ctx := context.Background()

	t.Run("LangGraph style", func(t *testing.T) {
		start := time.Now()

		// 创建 StateGraph
		graph := NewStateGraph("scan target system")

		// 并行运行（3 个 workers）
		finalState, err := graph.RunParallel(ctx, 3)
		require.NoError(t, err)

		duration := time.Since(start)

		// 验证结果
		assert.Equal(t, 10, finalState.Stats.TotalActions)
		assert.Equal(t, 10, finalState.Stats.CompletedActions)

		t.Logf("LangGraph style 并行执行:")
		t.Logf("  - 总耗时: %v", duration)
		t.Logf("  - 加速比: ~%.1fx", float64(10*100*time.Millisecond)/float64(duration))
		t.Logf("  - Actions: %d/%d", finalState.Stats.CompletedActions, finalState.Stats.TotalActions)
	})

	t.Run("Knowledge Graph style", func(t *testing.T) {
		// 创建内存 GraphStore
		graphStore := core.NewInMemoryGraphStore()

		start := time.Now()

		// 创建知识图谱编排器
		orchestrator := NewKnowledgeGraphOrchestrator("test-task-2", graphStore)

		// 并行运行（3 个 workers）
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()

		err := orchestrator.RunParallel(ctx, 3)
		require.NoError(t, err)

		duration := time.Since(start)
		stats := orchestrator.GetStats()

		// 验证结果
		assert.Equal(t, 10, stats.TotalActions)
		assert.Equal(t, 10, stats.CompletedActions)

		t.Logf("Knowledge Graph style 并行执行:")
		t.Logf("  - 总耗时: %v", duration)
		t.Logf("  - 轮询开销: ~%d 次 * 3 workers", int(duration.Milliseconds()/100))
		t.Logf("  - Actions: %d/%d", stats.CompletedActions, stats.TotalActions)
	})
}

// TestScenario3_DynamicReplanning 场景3：动态重规划
// Planner 生成 5 Actions → Evaluator 发现漏洞 → Planner 生成 5 新 Actions
func TestScenario3_DynamicReplanning(t *testing.T) {
	ctx := context.Background()

	t.Run("LangGraph style", func(t *testing.T) {
		start := time.Now()

		// 创建 StateGraph（初始 5 个 Actions）
		graph := NewStateGraph("scan target system")
		graph.state.PendingActions = generateActions(5)
		graph.state.Stats.TotalActions = 5

		// 第一轮：执行 5 个 Actions
		state1, err := graph.Run(ctx)
		require.NoError(t, err)

		// 模拟 Evaluator 发现漏洞，触发重规划
		state1.mu.Lock()
		state1.PendingActions = append(state1.PendingActions, generateActions(5)...)
		state1.Stats.TotalActions = 10
		state1.mu.Unlock()

		// 第二轮：执行新增 5 个 Actions
		graph.state = state1
		finalState, err := graph.Run(ctx)
		require.NoError(t, err)

		duration := time.Since(start)

		// 验证结果
		assert.Equal(t, 10, finalState.Stats.TotalActions)
		assert.Equal(t, 10, finalState.Stats.CompletedActions)

		t.Logf("LangGraph style 动态重规划:")
		t.Logf("  - 总耗时: %v", duration)
		t.Logf("  - Actions: %d/%d", finalState.Stats.CompletedActions, finalState.Stats.TotalActions)
	})

	t.Run("Knowledge Graph style", func(t *testing.T) {
		// 创建内存 GraphStore
		graphStore := core.NewInMemoryGraphStore()

		start := time.Now()

		// 创建知识图谱编排器（不调用 initializeActions）
		orchestrator := &KnowledgeGraphOrchestrator{
			taskID:     "test-task-3",
			graphStore: graphStore,
			stats: ExecutionStats{
				StartTime:    time.Now(),
				TotalActions: 5, // 初始 5 个
			},
		}

		// 手动创建 Objective 和 5 个 Actions
		ctx2, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()

		objective := &core.GraphNode{
			ID:      fmt.Sprintf("obj-%s", orchestrator.taskID),
			Kind:    "objective",
			Content: []byte(`{"description":"scan target"}`),
			State:   "active",
			Metadata: map[string]interface{}{
				"task_id": orchestrator.taskID,
			},
		}
		err := graphStore.CreateNode(ctx2, objective)
		require.NoError(t, err)

		for i := 0; i < 5; i++ {
			action := &core.GraphNode{
				ID:      fmt.Sprintf("action-%d", i+1),
				Kind:    "action",
				Content: []byte(fmt.Sprintf(`{"type":"scan","target":"target-%d"}`, i+1)),
				State:   "open",
				Metadata: map[string]interface{}{
					"task_id": orchestrator.taskID,
				},
			}
			err := graphStore.CreateNode(ctx2, action)
			require.NoError(t, err)
		}

		// 启动轮询（后台运行）
		done := make(chan error, 1)
		go func() {
			done <- orchestrator.Run(ctx2)
		}()

		// 等待 0.6 秒，模拟前 5 个 Actions 完成
		time.Sleep(600 * time.Millisecond)

		// 动态添加 5 个新 Actions（模拟重规划）
		for i := 5; i < 10; i++ {
			action := &core.GraphNode{
				ID:      fmt.Sprintf("action-%d", i+1),
				Kind:    "action",
				Content: []byte(fmt.Sprintf(`{"type":"scan","target":"target-%d"}`, i+1)),
				State:   "open",
				Metadata: map[string]interface{}{
					"task_id": orchestrator.taskID,
				},
			}
			err := graphStore.CreateNode(ctx2, action)
			require.NoError(t, err)
		}

		orchestrator.mu.Lock()
		orchestrator.stats.TotalActions = 10
		orchestrator.mu.Unlock()

		// 等待完成
		err = <-done
		require.NoError(t, err)

		duration := time.Since(start)
		stats := orchestrator.GetStats()

		// 验证结果
		assert.Equal(t, 10, stats.TotalActions)
		assert.Equal(t, 10, stats.CompletedActions)

		t.Logf("Knowledge Graph style 动态重规划:")
		t.Logf("  - 总耗时: %v", duration)
		t.Logf("  - 轮询次数: ~%d", int(duration.Milliseconds()/100))
		t.Logf("  - Actions: %d/%d", stats.CompletedActions, stats.TotalActions)
		t.Logf("  - 优势: 轮询自动发现新 Actions，无需重启")
	})
}

// generateActions 生成测试 Actions
func generateActions(count int) []Action {
	actions := make([]Action, count)
	for i := 0; i < count; i++ {
		actions[i] = Action{
			ID:        fmt.Sprintf("action-%d", i+1),
			Type:      "scan",
			Target:    fmt.Sprintf("target-%d", i+1),
			CreatedAt: time.Now(),
		}
	}
	return actions
}

// TestComparison_Summary 对比总结
func TestComparison_Summary(t *testing.T) {
	t.Log("\n=== LangGraph Style vs Knowledge Graph Style 对比 ===")
	t.Log("\n【架构对比】")
	t.Log("LangGraph Style:")
	t.Log("  - 内存状态驱动")
	t.Log("  - 同步执行流")
	t.Log("  - 条件边路由")
	t.Log("  - 无持久化")
	t.Log("\nKnowledge Graph Style:")
	t.Log("  - PostgreSQL 知识图谱")
	t.Log("  - 轮询驱动（2秒间隔）")
	t.Log("  - 图遍历查询")
	t.Log("  - 持久化存储")

	t.Log("\n【功能完整性】")
	t.Log("✓ 两种方式都能实现:")
	t.Log("  - 基础流程（Planner → Executor → Evaluator）")
	t.Log("  - 并行执行（多个 Executor 竞争）")
	t.Log("  - 动态重规划（运行时添加 Actions）")

	t.Log("\n【性能对比】")
	t.Log("LangGraph Style:")
	t.Log("  - 低延迟（无轮询开销）")
	t.Log("  - 内存高效")
	t.Log("  - 适合短期任务")
	t.Log("\nKnowledge Graph Style:")
	t.Log("  - 轮询开销（每 2 秒查询）")
	t.Log("  - 数据库 I/O")
	t.Log("  - 适合长期任务 + 可观测性")

	t.Log("\n【知识图谱独有优势】")
	t.Log("✓ 持久化：任务状态永久存储")
	t.Log("✓ 可观测性：可视化知识图谱")
	t.Log("✓ 分布式：多实例共享状态")
	t.Log("✓ 崩溃恢复：从数据库恢复")
	t.Log("✓ 历史追溯：完整执行历史")

	t.Log("\n【建议】")
	t.Log("- 如果只需要编排逻辑，LangGraph style 足够")
	t.Log("- 如果需要持久化 + 可观测性，Knowledge Graph 是核心价值")
	t.Log("- 两种方式可以共存：短期任务用 LangGraph，长期任务用 Knowledge Graph")
}
