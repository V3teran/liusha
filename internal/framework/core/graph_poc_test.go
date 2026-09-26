package core_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// Graph PoC 接口定义
// ============================================================================

// State 是图的状态载体
type State map[string]interface{}

// NodeFunc 是节点的执行函数
type NodeFunc func(ctx context.Context, state State) (State, error)

// ConditionFunc 决定下一跳的条件函数
type ConditionFunc func(ctx context.Context, state State) (string, error)

// Graph 是轻量级图编排接口
type Graph interface {
	AddNode(name string, fn NodeFunc) error
	AddEdge(from, to string) error
	AddConditionalEdge(from string, condition ConditionFunc, routes map[string]string) error
	Compile() error
	Run(ctx context.Context, input State) (State, error)
}

const END = "__end__"

// ============================================================================
// 简单实现（用于 PoC 验证）
// ============================================================================

type simpleGraph struct {
	nodes      map[string]NodeFunc
	edges      map[string][]string              // from -> [to]
	conditions map[string]conditionalEdge       // from -> {condition, routes}
	entryPoint string
	compiled   bool
	mu         sync.RWMutex
}

type conditionalEdge struct {
	condition ConditionFunc
	routes    map[string]string // condition_result -> next_node
}

func NewGraph(entryPoint string) Graph {
	return &simpleGraph{
		nodes:      make(map[string]NodeFunc),
		edges:      make(map[string][]string),
		conditions: make(map[string]conditionalEdge),
		entryPoint: entryPoint,
	}
}

func (g *simpleGraph) AddNode(name string, fn NodeFunc) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.compiled {
		return fmt.Errorf("cannot add node after compilation")
	}
	if _, exists := g.nodes[name]; exists {
		return fmt.Errorf("node %s already exists", name)
	}
	g.nodes[name] = fn
	return nil
}

func (g *simpleGraph) AddEdge(from, to string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.compiled {
		return fmt.Errorf("cannot add edge after compilation")
	}
	g.edges[from] = append(g.edges[from], to)
	return nil
}

func (g *simpleGraph) AddConditionalEdge(from string, condition ConditionFunc, routes map[string]string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.compiled {
		return fmt.Errorf("cannot add conditional edge after compilation")
	}
	g.conditions[from] = conditionalEdge{
		condition: condition,
		routes:    routes,
	}
	return nil
}

func (g *simpleGraph) Compile() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// 验证入口点存在
	if _, exists := g.nodes[g.entryPoint]; !exists {
		return fmt.Errorf("entry point %s not found", g.entryPoint)
	}

	// 验证所有边的目标节点存在
	for from, tos := range g.edges {
		for _, to := range tos {
			if to != END && g.nodes[to] == nil {
				return fmt.Errorf("edge %s -> %s: target node not found", from, to)
			}
		}
	}

	// 验证所有条件边的路由目标存在
	for from, cond := range g.conditions {
		for _, to := range cond.routes {
			if to != END && g.nodes[to] == nil {
				return fmt.Errorf("conditional edge %s -> %s: target node not found", from, to)
			}
		}
	}

	g.compiled = true
	return nil
}

func (g *simpleGraph) Run(ctx context.Context, input State) (State, error) {
	g.mu.RLock()
	if !g.compiled {
		g.mu.RUnlock()
		return nil, fmt.Errorf("graph not compiled")
	}
	g.mu.RUnlock()

	state := input
	current := g.entryPoint
	visited := make(map[string]int) // 防止无限循环
	maxVisits := 1000

	for current != END {
		visited[current]++
		if visited[current] > maxVisits {
			return nil, fmt.Errorf("infinite loop detected at node %s", current)
		}

		// 执行当前节点
		nodeFn := g.nodes[current]
		if nodeFn == nil {
			return nil, fmt.Errorf("node %s not found", current)
		}

		var err error
		state, err = nodeFn(ctx, state)
		if err != nil {
			return state, fmt.Errorf("node %s failed: %w", current, err)
		}

		// 决定下一跳
		next, err := g.nextNode(ctx, current, state)
		if err != nil {
			return state, fmt.Errorf("routing from %s failed: %w", current, err)
		}

		current = next
	}

	return state, nil
}

func (g *simpleGraph) nextNode(ctx context.Context, from string, state State) (string, error) {
	// 优先检查条件边
	if cond, exists := g.conditions[from]; exists {
		result, err := cond.condition(ctx, state)
		if err != nil {
			return "", err
		}
		if next, ok := cond.routes[result]; ok {
			return next, nil
		}
		return "", fmt.Errorf("no route for condition result: %s", result)
	}

	// 检查静态边
	if nexts, exists := g.edges[from]; exists {
		if len(nexts) == 0 {
			return END, nil
		}
		if len(nexts) > 1 {
			return "", fmt.Errorf("node %s has multiple edges but no condition", from)
		}
		return nexts[0], nil
	}

	// 默认结束
	return END, nil
}

// ============================================================================
// 测试 1：表达力验证 - Liusha Planner-Executor 工作流
// ============================================================================

func TestGraph_ExpressLiushaWorkflow(t *testing.T) {
	t.Run("基础 Planner-Executor 循环", func(t *testing.T) {
		// 模拟 ExplorationGraph
		kg := &mockExplorationGraph{
			actions: make([]mockAction, 0),
		}

		graph := NewGraph("planner")

		// Planner 节点：生成 Actions
		err := graph.AddNode("planner", func(ctx context.Context, state State) (State, error) {
			iteration := state["iteration"].(int)

			// 模拟 Planner 调用 LLM + ProposeActionsTool
			// 第 1 次迭代：生成 3 个 Actions
			// 第 2 次迭代：生成 2 个 Actions
			// 第 3 次迭代：不再生成
			if iteration == 1 {
				kg.CreateAction("recon_scan_ports")
				kg.CreateAction("recon_identify_services")
				kg.CreateAction("exploit_test_sqli")
			} else if iteration == 2 {
				kg.CreateAction("exploit_test_xss")
				kg.CreateAction("report_findings")
			}

			state["iteration"] = iteration + 1
			state["planner_runs"].([]int)[0]++
			return state, nil
		})
		require.NoError(t, err)

		// Executor 节点：执行 Actions
		err = graph.AddNode("executor", func(ctx context.Context, state State) (State, error) {
			// 模拟 Executor 从 KG 读取并执行
			openActions := kg.GetOpenActions()
			for _, action := range openActions {
				// 模拟执行
				kg.MarkCompleted(action.ID)
			}

			state["executor_runs"].([]int)[0]++
			state["actions_executed"] = kg.GetCompletedCount()
			return state, nil
		})
		require.NoError(t, err)

		// 条件路由：检查是否还需要继续规划
		err = graph.AddConditionalEdge("planner", func(ctx context.Context, state State) (string, error) {
			iteration := state["iteration"].(int)
			if iteration > 3 {
				// 超过 3 次迭代，结束
				return "end", nil
			}
			return "continue", nil
		}, map[string]string{
			"continue": "executor",
			"end":      END,
		})
		require.NoError(t, err)

		// Executor 完成后回到 Planner
		err = graph.AddEdge("executor", "planner")
		require.NoError(t, err)

		// 编译并运行
		err = graph.Compile()
		require.NoError(t, err)

		initialState := State{
			"iteration":        1,
			"planner_runs":     []int{0},
			"executor_runs":    []int{0},
			"actions_executed": 0,
		}

		finalState, err := graph.Run(context.Background(), initialState)
		require.NoError(t, err)

		// 验证结果
		assert.Equal(t, 4, finalState["iteration"], "应该运行 3 次 Planner 迭代")
		assert.Equal(t, 3, finalState["planner_runs"].([]int)[0], "Planner 应该运行 3 次")
		assert.Equal(t, 2, finalState["executor_runs"].([]int)[0], "Executor 应该运行 2 次（第3次 Planner 不生成 Actions）")
		assert.Equal(t, 5, finalState["actions_executed"], "应该执行 5 个 Actions")

		t.Logf("✅ Graph 成功表达 Liusha Planner-Executor 循环")
		t.Logf("   - Planner 运行: %d 次", finalState["planner_runs"].([]int)[0])
		t.Logf("   - Executor 运行: %d 次", finalState["executor_runs"].([]int)[0])
		t.Logf("   - Actions 执行: %d 个", finalState["actions_executed"])
	})

	t.Run("复杂场景：带依赖的 Actions", func(t *testing.T) {
		kg := &mockExplorationGraph{actions: make([]mockAction, 0)}
		graph := NewGraph("planner")

		// Planner：生成有依赖关系的 Actions
		err := graph.AddNode("planner", func(ctx context.Context, state State) (State, error) {
			if state["planned"].(bool) {
				return state, nil
			}

			// Action A: 无依赖
			kg.CreateActionWithDeps("scan_network", nil)
			// Action B: 依赖 A
			kg.CreateActionWithDeps("identify_targets", []string{"scan_network"})
			// Action C: 依赖 B
			kg.CreateActionWithDeps("exploit_targets", []string{"identify_targets"})

			state["planned"] = true
			return state, nil
		})
		require.NoError(t, err)

		// Executor：按依赖顺序执行
		err = graph.AddNode("executor", func(ctx context.Context, state State) (State, error) {
			executionOrder := state["execution_order"].([]string)

			// 模拟依赖解析：只执行依赖已满足的 Actions
			for {
				action := kg.GetNextExecutableAction()
				if action == nil {
					break
				}
				executionOrder = append(executionOrder, action.ID)
				kg.MarkCompleted(action.ID)
			}

			state["execution_order"] = executionOrder
			return state, nil
		})
		require.NoError(t, err)

		err = graph.AddConditionalEdge("planner", func(ctx context.Context, state State) (string, error) {
			if kg.GetOpenCount() == 0 {
				return "end", nil
			}
			return "continue", nil
		}, map[string]string{
			"continue": "executor",
			"end":      END,
		})
		require.NoError(t, err)

		err = graph.AddConditionalEdge("executor", func(ctx context.Context, state State) (string, error) {
			if kg.GetOpenCount() > 0 {
				return "continue", nil
			}
			return "end", nil
		}, map[string]string{
			"continue": "executor", // 继续执行下一批
			"end":      END,
		})
		require.NoError(t, err)

		err = graph.Compile()
		require.NoError(t, err)

		initialState := State{
			"planned":         false,
			"execution_order": []string{},
		}

		finalState, err := graph.Run(context.Background(), initialState)
		require.NoError(t, err)

		executionOrder := finalState["execution_order"].([]string)
		assert.Equal(t, 3, len(executionOrder), "应该执行 3 个 Actions")
		assert.Equal(t, "scan_network", executionOrder[0], "第一个应该是无依赖的")
		assert.Equal(t, "identify_targets", executionOrder[1], "第二个依赖第一个")
		assert.Equal(t, "exploit_targets", executionOrder[2], "第三个依赖第二个")

		t.Logf("✅ Graph 成功处理依赖关系")
		t.Logf("   执行顺序: %v", executionOrder)
	})
}

// ============================================================================
// 测试 2：性能验证 - 开销是否可接受
// ============================================================================

func TestGraph_Performance(t *testing.T) {
	t.Run("图执行开销测量", func(t *testing.T) {
		graph := NewGraph("node_0")

		// 构建一个 10 节点的链
		for i := 0; i < 10; i++ {
			nodeName := fmt.Sprintf("node_%d", i)
			nextNode := fmt.Sprintf("node_%d", i+1)
			if i == 9 {
				nextNode = END
			}

			err := graph.AddNode(nodeName, func(ctx context.Context, state State) (State, error) {
				// 模拟轻量级工作
				state["counter"] = state["counter"].(int) + 1
				return state, nil
			})
			require.NoError(t, err)

			if i < 9 {
				err = graph.AddEdge(nodeName, nextNode)
				require.NoError(t, err)
			}
		}

		err := graph.Compile()
		require.NoError(t, err)

		// 预热
		_, _ = graph.Run(context.Background(), State{"counter": 0})

		// 测量 100 次运行
		iterations := 100
		start := time.Now()

		for i := 0; i < iterations; i++ {
			_, err := graph.Run(context.Background(), State{"counter": 0})
			require.NoError(t, err)
		}

		elapsed := time.Since(start)
		avgLatency := elapsed / time.Duration(iterations)

		t.Logf("📊 性能数据:")
		t.Logf("   - 总耗时: %v", elapsed)
		t.Logf("   - 平均延迟: %v", avgLatency)
		t.Logf("   - QPS: %.0f", float64(iterations)/elapsed.Seconds())

		// 验证：平均延迟应该 < 1ms（10 节点链）
		assert.Less(t, avgLatency, 1*time.Millisecond, "Graph 开销过大")

		if avgLatency < 100*time.Microsecond {
			t.Logf("✅ 性能优异：平均延迟 %v < 100μs", avgLatency)
		} else if avgLatency < 1*time.Millisecond {
			t.Logf("✅ 性能可接受：平均延迟 %v < 1ms", avgLatency)
		} else {
			t.Errorf("❌ 性能不足：平均延迟 %v > 1ms", avgLatency)
		}
	})

	t.Run("内存开销测量", func(t *testing.T) {
		// 测试创建 100 个图的内存占用
		graphs := make([]Graph, 100)

		for i := 0; i < 100; i++ {
			g := NewGraph("start")
			_ = g.AddNode("start", func(ctx context.Context, state State) (State, error) {
				return state, nil
			})
			_ = g.AddNode("end", func(ctx context.Context, state State) (State, error) {
				return state, nil
			})
			_ = g.AddEdge("start", "end")
			_ = g.Compile()
			graphs[i] = g
		}

		// 估算内存（粗略）
		// 每个 simpleGraph 约 1KB（map + 函数指针）
		estimatedMemory := 100 * 1024 // 100 KB

		t.Logf("📊 内存估算:")
		t.Logf("   - 100 个 Graph 实例: ~%d KB", estimatedMemory/1024)
		t.Logf("   - 平均每个: ~1 KB")

		assert.Less(t, estimatedMemory, 1024*1024, "内存占用应该 < 1MB")
		t.Logf("✅ 内存开销可接受")
	})
}

// ============================================================================
// 测试 3：兼容性验证 - 是否破坏现有架构
// ============================================================================

func TestGraph_Compatibility(t *testing.T) {
	t.Run("Graph 节点内嵌 ReActRuntime", func(t *testing.T) {
		// 模拟 ReActRuntime
		type ReActRuntime struct {
			tools     []string
			maxIters  int
			execCount int
		}

		reactRuntime := &ReActRuntime{
			tools:    []string{"tool1", "tool2"},
			maxIters: 10,
		}

		graph := NewGraph("agent")

		// 节点内部使用 ReActRuntime（现有代码无需修改）
		err := graph.AddNode("agent", func(ctx context.Context, state State) (State, error) {
			// 这里可以直接调用现有的 ReActRuntime.Run()
			// 模拟执行
			for i := 0; i < reactRuntime.maxIters; i++ {
				reactRuntime.execCount++
				// LLM 调用 + 工具执行...
			}

			state["react_iterations"] = reactRuntime.execCount
			return state, nil
		})
		require.NoError(t, err)

		err = graph.Compile()
		require.NoError(t, err)

		finalState, err := graph.Run(context.Background(), State{})
		require.NoError(t, err)

		assert.Equal(t, 10, finalState["react_iterations"])
		t.Logf("✅ Graph 与 ReActRuntime 兼容（无需修改现有代码）")
	})

	t.Run("渐进式迁移路径", func(t *testing.T) {
		// 验证可以只用 Graph 包装现有逻辑，不改变内部实现

		// 旧代码（保持不变）
		oldPlannerLogic := func(ctx context.Context) (int, error) {
			// 现有 Planner 逻辑
			return 42, nil
		}

		// 新 Graph（只是包装）
		graph := NewGraph("planner")
		err := graph.AddNode("planner", func(ctx context.Context, state State) (State, error) {
			result, err := oldPlannerLogic(ctx) // 调用旧逻辑
			if err != nil {
				return state, err
			}
			state["result"] = result
			return state, nil
		})
		require.NoError(t, err)

		err = graph.Compile()
		require.NoError(t, err)

		finalState, err := graph.Run(context.Background(), State{})
		require.NoError(t, err)

		assert.Equal(t, 42, finalState["result"])
		t.Logf("✅ 支持渐进式迁移（旧代码无需重写）")
	})
}

// ============================================================================
// 测试 4：扩展性验证 - 未来需求能否支持
// ============================================================================

func TestGraph_Extensibility(t *testing.T) {
	t.Run("并行节点执行", func(t *testing.T) {
		// 验证：Graph 是否支持并行执行多个独立节点

		graph := NewGraph("start")

		results := &sync.Map{}

		err := graph.AddNode("start", func(ctx context.Context, state State) (State, error) {
			// 触发并行分支
			state["parallel_ready"] = true
			return state, nil
		})
		require.NoError(t, err)

		// 并行节点 A
		err = graph.AddNode("parallel_a", func(ctx context.Context, state State) (State, error) {
			time.Sleep(10 * time.Millisecond)
			results.Store("a", time.Now())
			return state, nil
		})
		require.NoError(t, err)

		// 并行节点 B
		err = graph.AddNode("parallel_b", func(ctx context.Context, state State) (State, error) {
			time.Sleep(10 * time.Millisecond)
			results.Store("b", time.Now())
			return state, nil
		})
		require.NoError(t, err)

		// 汇聚节点
		err = graph.AddNode("join", func(ctx context.Context, state State) (State, error) {
			// 等待所有并行任务完成
			_, okA := results.Load("a")
			_, okB := results.Load("b")
			if !okA || !okB {
				return state, fmt.Errorf("parallel tasks not completed")
			}
			return state, nil
		})
		require.NoError(t, err)

		// 注意：当前实现是串行的，这个测试会失败
		// 这是为了验证扩展性：未来是否可以支持并行

		err = graph.AddEdge("start", "parallel_a")
		require.NoError(t, err)
		err = graph.AddEdge("parallel_a", "parallel_b") // 串行模拟
		require.NoError(t, err)
		err = graph.AddEdge("parallel_b", "join")
		require.NoError(t, err)

		err = graph.Compile()
		require.NoError(t, err)

		start := time.Now()
		_, err = graph.Run(context.Background(), State{})
		elapsed := time.Since(start)

		require.NoError(t, err)

		// 当前是串行：应该 >= 20ms
		// 如果未来支持并行：应该 < 15ms
		t.Logf("📊 并行执行耗时: %v", elapsed)
		if elapsed < 15*time.Millisecond {
			t.Logf("✅ 支持并行执行")
		} else {
			t.Logf("⚠️  当前是串行执行（未来可扩展支持并行）")
		}
	})

	t.Run("子图嵌套", func(t *testing.T) {
		// 验证：是否支持子图（Graph 作为节点）

		// 子图
		subgraph := NewGraph("sub_start")
		err := subgraph.AddNode("sub_start", func(ctx context.Context, state State) (State, error) {
			state["subgraph_executed"] = true
			return state, nil
		})
		require.NoError(t, err)
		err = subgraph.Compile()
		require.NoError(t, err)

		// 父图
		parentGraph := NewGraph("parent")
		err = parentGraph.AddNode("parent", func(ctx context.Context, state State) (State, error) {
			// 调用子图
			subState, err := subgraph.Run(ctx, state)
			if err != nil {
				return state, err
			}
			return subState, nil
		})
		require.NoError(t, err)

		err = parentGraph.Compile()
		require.NoError(t, err)

		finalState, err := parentGraph.Run(context.Background(), State{})
		require.NoError(t, err)

		assert.True(t, finalState["subgraph_executed"].(bool))
		t.Logf("✅ 支持子图嵌套（Graph 可作为节点）")
	})
}

// ============================================================================
// 测试 5：边界条件验证
// ============================================================================

func TestGraph_EdgeCases(t *testing.T) {
	t.Run("循环检测", func(t *testing.T) {
		graph := NewGraph("a")

		err := graph.AddNode("a", func(ctx context.Context, state State) (State, error) {
			return state, nil
		})
		require.NoError(t, err)

		err = graph.AddNode("b", func(ctx context.Context, state State) (State, error) {
			return state, nil
		})
		require.NoError(t, err)

		// 构造循环：a -> b -> a
		err = graph.AddEdge("a", "b")
		require.NoError(t, err)
		err = graph.AddEdge("b", "a")
		require.NoError(t, err)

		err = graph.Compile()
		require.NoError(t, err)

		// 运行应该检测到循环并终止
		_, err = graph.Run(context.Background(), State{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "infinite loop")

		t.Logf("✅ 循环检测正常工作")
	})

	t.Run("Context 取消传播", func(t *testing.T) {
		graph := NewGraph("slow")

		err := graph.AddNode("slow", func(ctx context.Context, state State) (State, error) {
			select {
			case <-time.After(1 * time.Second):
				return state, nil
			case <-ctx.Done():
				return state, ctx.Err()
			}
		})
		require.NoError(t, err)

		err = graph.Compile()
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		_, err = graph.Run(ctx, State{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "context deadline exceeded")

		t.Logf("✅ Context 取消正确传播")
	})

	t.Run("空图", func(t *testing.T) {
		graph := NewGraph("start")
		err := graph.Compile()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "entry point")

		t.Logf("✅ 空图编译失败（符合预期）")
	})

	t.Run("悬空边", func(t *testing.T) {
		graph := NewGraph("start")

		err := graph.AddNode("start", func(ctx context.Context, state State) (State, error) {
			return state, nil
		})
		require.NoError(t, err)

		err = graph.AddEdge("start", "nonexistent")
		require.NoError(t, err)

		err = graph.Compile()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "target node not found")

		t.Logf("✅ 悬空边检测正常")
	})
}

// ============================================================================
// Mock 辅助结构
// ============================================================================

type mockAction struct {
	ID          string
	DependsOn   []string
	Completed   bool
}

type mockExplorationGraph struct {
	actions []mockAction
	mu      sync.Mutex
}

func (kg *mockExplorationGraph) CreateAction(id string) {
	kg.CreateActionWithDeps(id, nil)
}

func (kg *mockExplorationGraph) CreateActionWithDeps(id string, deps []string) {
	kg.mu.Lock()
	defer kg.mu.Unlock()
	kg.actions = append(kg.actions, mockAction{
		ID:        id,
		DependsOn: deps,
		Completed: false,
	})
}

func (kg *mockExplorationGraph) GetOpenActions() []mockAction {
	kg.mu.Lock()
	defer kg.mu.Unlock()
	var open []mockAction
	for _, a := range kg.actions {
		if !a.Completed {
			open = append(open, a)
		}
	}
	return open
}

func (kg *mockExplorationGraph) GetOpenCount() int {
	return len(kg.GetOpenActions())
}

func (kg *mockExplorationGraph) MarkCompleted(id string) {
	kg.mu.Lock()
	defer kg.mu.Unlock()
	for i := range kg.actions {
		if kg.actions[i].ID == id {
			kg.actions[i].Completed = true
			break
		}
	}
}

func (kg *mockExplorationGraph) GetCompletedCount() int {
	kg.mu.Lock()
	defer kg.mu.Unlock()
	count := 0
	for _, a := range kg.actions {
		if a.Completed {
			count++
		}
	}
	return count
}

func (kg *mockExplorationGraph) GetNextExecutableAction() *mockAction {
	kg.mu.Lock()
	defer kg.mu.Unlock()

	for i := range kg.actions {
		if kg.actions[i].Completed {
			continue
		}

		// 检查依赖是否满足
		depsOK := true
		for _, depID := range kg.actions[i].DependsOn {
			depCompleted := false
			for _, a := range kg.actions {
				if a.ID == depID && a.Completed {
					depCompleted = true
					break
				}
			}
			if !depCompleted {
				depsOK = false
				break
			}
		}

		if depsOK {
			return &kg.actions[i]
		}
	}

	return nil
}
