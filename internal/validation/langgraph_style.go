package validation

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ============================================
// LangGraph 风格实现：State + 条件边
// ============================================

// AgentState 是共享状态（类似 LangGraph 的 StateGraph）
type AgentState struct {
	mu sync.RWMutex

	// 任务目标
	Objective string

	// 待执行的 Actions（动态生成）
	PendingActions []Action

	// 已执行的 Actions
	CompletedActions []Action

	// 观察结果
	Observations []Observation

	// 发现的漏洞
	Findings []Finding

	// 执行统计
	Stats ExecutionStats
}

// Action 动作
type Action struct {
	ID          string
	Type        string
	Target      string
	Parameters  map[string]interface{}
	DependsOn   []string
	CreatedAt   time.Time
}

// Observation 观察结果
type Observation struct {
	ID        string
	ActionID  string
	Type      string
	Data      map[string]interface{}
	CreatedAt time.Time
}

// Finding 漏洞发现
type Finding struct {
	ID            string
	ObservationID string
	Severity      string
	Description   string
	CreatedAt     time.Time
}

// ExecutionStats 执行统计
type ExecutionStats struct {
	TotalActions     int
	CompletedActions int
	FailedActions    int
	TotalFindings    int
	StartTime        time.Time
	EndTime          time.Time
}

// ============================================
// Node 函数（类似 LangGraph 的 node）
// ============================================

// NodeFunc 是节点函数类型
type NodeFunc func(ctx context.Context, state *AgentState) error

// PlannerNode Planner 节点：生成新的 Actions
func PlannerNode(ctx context.Context, state *AgentState) error {
	state.mu.Lock()
	defer state.mu.Unlock()

	// 模拟 LLM 规划（生成 10 个 Actions）
	newActions := make([]Action, 10)
	for i := 0; i < 10; i++ {
		newActions[i] = Action{
			ID:         fmt.Sprintf("action-%d", i+1),
			Type:       "scan",
			Target:     fmt.Sprintf("target-%d", i+1),
			Parameters: map[string]interface{}{"port": 80 + i},
			CreatedAt:  time.Now(),
		}
	}

	// 添加到待执行队列
	state.PendingActions = append(state.PendingActions, newActions...)
	state.Stats.TotalActions += len(newActions)

	return nil
}

// ExecutorNode Executor 节点：执行一个 Action
func ExecutorNode(ctx context.Context, state *AgentState) error {
	state.mu.Lock()

	// 检查是否有待执行的 Actions
	if len(state.PendingActions) == 0 {
		state.mu.Unlock()
		return nil
	}

	// 取出第一个 Action
	action := state.PendingActions[0]
	state.PendingActions = state.PendingActions[1:]
	state.mu.Unlock()

	// 模拟执行（耗时 100ms）
	time.Sleep(100 * time.Millisecond)

	// 生成观察结果
	obs := Observation{
		ID:        fmt.Sprintf("obs-%s", action.ID),
		ActionID:  action.ID,
		Type:      "scan_result",
		Data:      map[string]interface{}{"status": "success"},
		CreatedAt: time.Now(),
	}

	state.mu.Lock()
	state.CompletedActions = append(state.CompletedActions, action)
	state.Observations = append(state.Observations, obs)
	state.Stats.CompletedActions++
	state.mu.Unlock()

	return nil
}

// EvaluatorNode Evaluator 节点：评估观察结果
func EvaluatorNode(ctx context.Context, state *AgentState) error {
	state.mu.Lock()
	defer state.mu.Unlock()

	// 检查最新的观察结果（假设 10% 概率发现漏洞）
	if len(state.Observations) > 0 {
		obs := state.Observations[len(state.Observations)-1]

		// 模拟 LLM 评估
		if state.Stats.CompletedActions%10 == 0 {
			finding := Finding{
				ID:            fmt.Sprintf("finding-%d", len(state.Findings)+1),
				ObservationID: obs.ID,
				Severity:      "high",
				Description:   "SQL injection vulnerability",
				CreatedAt:     time.Now(),
			}
			state.Findings = append(state.Findings, finding)
			state.Stats.TotalFindings++
		}
	}

	return nil
}

// ============================================
// 条件函数（类似 LangGraph 的 conditional_edges）
// ============================================

// ShouldContinueExecution 判断是否继续执行
func ShouldContinueExecution(state *AgentState) string {
	state.mu.RLock()
	defer state.mu.RUnlock()

	// 如果还有待执行的 Actions，继续执行
	if len(state.PendingActions) > 0 {
		return "executor"
	}

	// 否则结束（不自动重新规划）
	return "end"
}

// ============================================
// StateGraph 执行器（类似 LangGraph 的 compiled graph）
// ============================================

// StateGraph LangGraph 风格的图执行器
type StateGraph struct {
	state *AgentState
	nodes map[string]NodeFunc
}

// NewStateGraph 创建 StateGraph
func NewStateGraph(objective string) *StateGraph {
	return &StateGraph{
		state: &AgentState{
			Objective:        objective,
			PendingActions:   []Action{},
			CompletedActions: []Action{},
			Observations:     []Observation{},
			Findings:         []Finding{},
			Stats: ExecutionStats{
				StartTime: time.Now(),
			},
		},
		nodes: map[string]NodeFunc{
			"planner":   PlannerNode,
			"executor":  ExecutorNode,
			"evaluator": EvaluatorNode,
		},
	}
}

// Run 运行图（类似 LangGraph 的 invoke）
func (g *StateGraph) Run(ctx context.Context) (*AgentState, error) {
	// 从 planner 开始
	currentNode := "planner"

	for currentNode != "end" {
		// 检查上下文
		select {
		case <-ctx.Done():
			return g.state, ctx.Err()
		default:
		}

		// 执行当前节点
		nodeFn, exists := g.nodes[currentNode]
		if !exists {
			return g.state, fmt.Errorf("node %q not found", currentNode)
		}

		if err := nodeFn(ctx, g.state); err != nil {
			return g.state, fmt.Errorf("node %q failed: %w", currentNode, err)
		}

		// 执行 evaluator（每次 executor 后）
		if currentNode == "executor" {
			if err := g.nodes["evaluator"](ctx, g.state); err != nil {
				return g.state, fmt.Errorf("evaluator failed: %w", err)
			}
		}

		// 条件分支：决定下一个节点
		currentNode = ShouldContinueExecution(g.state)
	}

	// 记录结束时间
	g.state.mu.Lock()
	g.state.Stats.EndTime = time.Now()
	g.state.mu.Unlock()

	return g.state, nil
}

// GetState 获取当前状态
func (g *StateGraph) GetState() *AgentState {
	return g.state
}

// ============================================
// 并行执行版本（支持多个 Executor 并行）
// ============================================

// RunParallel 并行运行（多个 Executor）
func (g *StateGraph) RunParallel(ctx context.Context, parallelism int) (*AgentState, error) {
	// 先运行 Planner 生成 Actions
	if err := g.nodes["planner"](ctx, g.state); err != nil {
		return g.state, fmt.Errorf("planner failed: %w", err)
	}

	// 启动多个 Executor goroutines
	var wg sync.WaitGroup
	errChan := make(chan error, parallelism)

	for i := 0; i < parallelism; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for {
				// 检查上下文
				select {
				case <-ctx.Done():
					return
				default:
				}

				// 执行一个 Action
				if err := g.nodes["executor"](ctx, g.state); err != nil {
					errChan <- fmt.Errorf("executor-%d failed: %w", workerID, err)
					return
				}

				// 执行 Evaluator
				if err := g.nodes["evaluator"](ctx, g.state); err != nil {
					errChan <- fmt.Errorf("evaluator-%d failed: %w", workerID, err)
					return
				}

				// 检查是否还有待执行的 Actions
				g.state.mu.RLock()
				hasMore := len(g.state.PendingActions) > 0
				g.state.mu.RUnlock()

				if !hasMore {
					return
				}
			}
		}(i)
	}

	// 等待所有 workers 完成
	wg.Wait()
	close(errChan)

	// 检查错误
	for err := range errChan {
		if err != nil {
			return g.state, err
		}
	}

	// 记录结束时间
	g.state.mu.Lock()
	g.state.Stats.EndTime = time.Now()
	g.state.mu.Unlock()

	return g.state, nil
}
