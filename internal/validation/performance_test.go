package validation

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/stretchr/testify/require"
)

// BenchmarkBasicFlow_LangGraph 基准测试：LangGraph 风格基础流程
func BenchmarkBasicFlow_LangGraph(b *testing.B) {
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		graph := NewStateGraph("scan target system")
		_, err := graph.Run(ctx)
		require.NoError(b, err)
	}
}

// BenchmarkBasicFlow_KnowledgeGraph 基准测试：知识图谱风格基础流程
func BenchmarkBasicFlow_KnowledgeGraph(b *testing.B) {
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		graphStore := core.NewInMemoryGraphStore()
		orchestrator := NewKnowledgeGraphOrchestrator(fmt.Sprintf("task-%d", i), graphStore)

		ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := orchestrator.Run(ctx2)
		cancel()
		require.NoError(b, err)
	}
}

// BenchmarkParallel_LangGraph 基准测试：LangGraph 并行执行
func BenchmarkParallel_LangGraph(b *testing.B) {
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		graph := NewStateGraph("scan target system")
		_, err := graph.RunParallel(ctx, 3)
		require.NoError(b, err)
	}
}

// BenchmarkParallel_KnowledgeGraph 基准测试：知识图谱并行执行
func BenchmarkParallel_KnowledgeGraph(b *testing.B) {
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		graphStore := core.NewInMemoryGraphStore()
		orchestrator := NewKnowledgeGraphOrchestrator(fmt.Sprintf("task-%d", i), graphStore)

		ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := orchestrator.RunParallel(ctx2, 3)
		cancel()
		require.NoError(b, err)
	}
}

// TestPerformanceComparison 性能对比测试（单次运行，详细输出）
func TestPerformanceComparison(t *testing.T) {
	ctx := context.Background()

	t.Log("\n=== 性能对比测试 ===\n")

	// 测试 1: 顺序执行
	t.Run("顺序执行 (10 Actions)", func(t *testing.T) {
		// LangGraph
		start := time.Now()
		graph := NewStateGraph("scan target system")
		state, err := graph.Run(ctx)
		require.NoError(t, err)
		langDuration := time.Since(start)

		t.Logf("\n[LangGraph 风格]")
		t.Logf("  总耗时: %v", langDuration)
		t.Logf("  Actions: %d/%d", state.Stats.CompletedActions, state.Stats.TotalActions)
		t.Logf("  漏洞: %d", state.Stats.TotalFindings)

		// Knowledge Graph
		start = time.Now()
		graphStore := core.NewInMemoryGraphStore()
		orchestrator := NewKnowledgeGraphOrchestrator("perf-test-1", graphStore)
		ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = orchestrator.Run(ctx2)
		cancel()
		require.NoError(t, err)
		kgDuration := time.Since(start)
		stats := orchestrator.GetStats()

		t.Logf("\n[知识图谱风格]")
		t.Logf("  总耗时: %v", kgDuration)
		t.Logf("  轮询次数: ~%d", int(kgDuration.Milliseconds()/100))
		t.Logf("  Actions: %d/%d", stats.CompletedActions, stats.TotalActions)
		t.Logf("  漏洞: %d", stats.TotalFindings)

		t.Logf("\n[对比]")
		t.Logf("  知识图谱 / LangGraph: %.2fx", float64(kgDuration)/float64(langDuration))
		t.Logf("  开销: %v", kgDuration-langDuration)
	})

	// 测试 2: 并行执行
	t.Run("并行执行 (10 Actions, 3 workers)", func(t *testing.T) {
		// LangGraph
		start := time.Now()
		graph := NewStateGraph("scan target system")
		state, err := graph.RunParallel(ctx, 3)
		require.NoError(t, err)
		langDuration := time.Since(start)

		t.Logf("\n[LangGraph 风格]")
		t.Logf("  总耗时: %v", langDuration)
		t.Logf("  加速比: %.2fx (vs 顺序)", float64(10*100*time.Millisecond)/float64(langDuration))
		t.Logf("  Actions: %d/%d", state.Stats.CompletedActions, state.Stats.TotalActions)

		// Knowledge Graph
		start = time.Now()
		graphStore := core.NewInMemoryGraphStore()
		orchestrator := NewKnowledgeGraphOrchestrator("perf-test-2", graphStore)
		ctx2, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = orchestrator.RunParallel(ctx2, 3)
		cancel()
		require.NoError(t, err)
		kgDuration := time.Since(start)
		stats := orchestrator.GetStats()

		t.Logf("\n[知识图谱风格]")
		t.Logf("  总耗时: %v", kgDuration)
		t.Logf("  轮询开销: ~%d 次 * 3 workers", int(kgDuration.Milliseconds()/100))
		t.Logf("  Actions: %d/%d", stats.CompletedActions, stats.TotalActions)

		t.Logf("\n[对比]")
		t.Logf("  知识图谱 / LangGraph: %.2fx", float64(kgDuration)/float64(langDuration))
		t.Logf("  开销: %v", kgDuration-langDuration)
	})
}

// TestMemoryUsage 内存使用对比
func TestMemoryUsage(t *testing.T) {
	ctx := context.Background()

	t.Log("\n=== 内存使用对比 ===\n")

	// LangGraph: 100 个 Actions
	t.Run("LangGraph (100 Actions)", func(t *testing.T) {
		graph := NewStateGraph("scan target system")

		// 手动生成 100 个 Actions
		graph.state.mu.Lock()
		for i := 0; i < 100; i++ {
			graph.state.PendingActions = append(graph.state.PendingActions, Action{
				ID:        fmt.Sprintf("action-%d", i+1),
				Type:      "scan",
				Target:    fmt.Sprintf("target-%d", i+1),
				CreatedAt: time.Now(),
			})
		}
		graph.state.Stats.TotalActions = 100
		graph.state.mu.Unlock()

		start := time.Now()
		_, err := graph.Run(ctx)
		require.NoError(t, err)
		duration := time.Since(start)

		t.Logf("  总耗时: %v", duration)
		t.Logf("  平均每 Action: %v", duration/100)
	})

	// Knowledge Graph: 100 个 Actions
	t.Run("Knowledge Graph (100 Actions)", func(t *testing.T) {
		graphStore := core.NewInMemoryGraphStore()

		// 手动创建 100 个 Actions
		for i := 0; i < 100; i++ {
			action := &core.GraphNode{
				ID:      fmt.Sprintf("mem-action-%d", i+1),
				Kind:    "action",
				Content: []byte(fmt.Sprintf(`{"type":"scan","target":"target-%d"}`, i+1)),
				State:   "open",
				Metadata: map[string]interface{}{
					"task_id": "mem-test",
				},
			}
			err := graphStore.CreateNode(ctx, action)
			require.NoError(t, err)
		}

		orchestrator := &KnowledgeGraphOrchestrator{
			taskID:     "mem-test",
			graphStore: graphStore,
			stats: ExecutionStats{
				StartTime:    time.Now(),
				TotalActions: 100,
			},
		}

		start := time.Now()
		ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := orchestrator.Run(ctx2)
		cancel()
		require.NoError(t, err)
		duration := time.Since(start)

		t.Logf("  总耗时: %v", duration)
		t.Logf("  平均每 Action: %v", duration/100)
		t.Logf("  轮询次数: ~%d", int(duration.Milliseconds()/100))
	})
}
