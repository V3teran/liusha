package core

import (
	"context"
	"fmt"
	"sync"
)

// Graph 是声明式编排图，用于定义 Agent 执行流程。
//
// 核心概念：
// - Node：执行单元（通常是 Agent）
// - Edge：执行顺序（from → to）
// - State：节点间传递的数据
//
// 设计原则：
// - 图结构在编译时确定，运行时不可变
// - 节点内部逻辑动态（可根据 State 决定行为）
// - 支持条件路由（根据运行时状态选择下一跳）
// - 支持串行和并发两种执行模式
type Graph interface {
	// AddNode 添加节点
	AddNode(name string, fn NodeFunc) error

	// AddEdge 添加边（from → to）
	AddEdge(from, to string) error

	// AddConditionalEdge 添加条件边
	// condition 函数返回路由键，routes 映射键到目标节点
	AddConditionalEdge(from string, condition ConditionFunc, routes map[string]string) error

	// Compile 编译图（检查完整性、构建拓扑）
	Compile() error

	// Run 执行图
	Run(ctx context.Context, input GraphState) (GraphState, error)
}

// NodeFunc 是节点的执行函数
//
// 参数：
// - ctx：上下文（用于取消、超时）
// - state：输入状态（只读，节点不应修改它）
//
// 返回：
// - GraphState：输出状态（新的 GraphState，不修改输入）
// - error：执行错误
type NodeFunc func(ctx context.Context, state GraphState) (GraphState, error)

// ConditionFunc 是条件路由函数
//
// 参数：
// - ctx：上下文
// - state：当前状态
//
// 返回：
// - string：路由键（用于在 routes 中查找下一跳）
// - error：判断错误
type ConditionFunc func(ctx context.Context, state GraphState) (string, error)

// GraphState 是节点间传递的数据容器
//
// 设计说明：
// - 与 State[T] 是不同的概念
// - State[T] 用于任务状态持久化（数据库层）
// - GraphState 用于节点间临时数据传递（内存层）
type GraphState struct {
	data map[string]interface{}
	mu   sync.RWMutex
}

// NewGraphState 创建新的 GraphState
func NewGraphState() *GraphState {
	return &GraphState{
		data: make(map[string]interface{}),
	}
}

// Get 获取值
func (s *GraphState) Get(key string) (interface{}, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.data[key]
	return val, ok
}

// GetString 获取字符串值
func (s *GraphState) GetString(key string) (string, error) {
	val, ok := s.Get(key)
	if !ok {
		return "", fmt.Errorf("key %s not found", key)
	}
	str, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("key %s is not string", key)
	}
	return str, nil
}

// GetInt 获取整数值
func (s *GraphState) GetInt(key string) (int, error) {
	val, ok := s.Get(key)
	if !ok {
		return 0, fmt.Errorf("key %s not found", key)
	}
	num, ok := val.(int)
	if !ok {
		return 0, fmt.Errorf("key %s is not int", key)
	}
	return num, nil
}

// GetBool 获取布尔值
func (s *GraphState) GetBool(key string) (bool, error) {
	val, ok := s.Get(key)
	if !ok {
		return false, fmt.Errorf("key %s not found", key)
	}
	b, ok := val.(bool)
	if !ok {
		return false, fmt.Errorf("key %s is not bool", key)
	}
	return b, nil
}

// Set 设置值
func (s *GraphState) Set(key string, value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
}

// Clone 克隆 GraphState（用于并发执行）
func (s *GraphState) Clone() *GraphState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cloned := NewGraphState()
	for k, v := range s.data {
		cloned.data[k] = v
	}
	return cloned
}

// Merge 合并另一个 GraphState 的数据（用于并发执行后合并）
func (s *GraphState) Merge(other *GraphState) {
	s.mu.Lock()
	defer s.mu.Unlock()

	other.mu.RLock()
	defer other.mu.RUnlock()

	for k, v := range other.data {
		s.data[k] = v
	}
}

// Keys 返回所有键
func (s *GraphState) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	return keys
}

// END 是特殊的终止节点标识
const END = "__end__"

// ExecutionMode 是执行模式
type ExecutionMode int

const (
	// ExecutionModeSequential 串行模式（按拓扑顺序依次执行）
	ExecutionModeSequential ExecutionMode = iota

	// ExecutionModeConcurrent 并发模式（同层节点并行执行）
	ExecutionModeConcurrent
)

// GraphConfig 是图的配置
type GraphConfig struct {
	// ExecutionMode 执行模式（默认串行）
	ExecutionMode ExecutionMode

	// MaxIterations 最大迭代次数（防止无限循环，默认 1000）
	MaxIterations int
}

// DefaultGraphConfig 返回默认配置
func DefaultGraphConfig() *GraphConfig {
	return &GraphConfig{
		ExecutionMode: ExecutionModeSequential,
		MaxIterations: 1000,
	}
}

// NewGraph 创建新的图
func NewGraph(entryPoint string, config *GraphConfig) Graph {
	if config == nil {
		config = DefaultGraphConfig()
	}

	return &graph{
		nodes:      make(map[string]NodeFunc),
		edges:      make(map[string][]string),
		conditions: make(map[string]conditionalEdge),
		entryPoint: entryPoint,
		config:     config,
	}
}

// graph 是 Graph 的实现
type graph struct {
	nodes      map[string]NodeFunc
	edges      map[string][]string
	conditions map[string]conditionalEdge
	entryPoint string
	config     *GraphConfig
	compiled   bool
	mu         sync.RWMutex
}

type conditionalEdge struct {
	condition ConditionFunc
	routes    map[string]string
}

func (g *graph) AddNode(name string, fn NodeFunc) error {
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

func (g *graph) AddEdge(from, to string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.compiled {
		return fmt.Errorf("cannot add edge after compilation")
	}
	g.edges[from] = append(g.edges[from], to)
	return nil
}

func (g *graph) AddConditionalEdge(from string, condition ConditionFunc, routes map[string]string) error {
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

func (g *graph) Compile() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// 1. 验证入口点存在
	if _, exists := g.nodes[g.entryPoint]; !exists {
		return fmt.Errorf("entry point %s not found", g.entryPoint)
	}

	// 2. 验证所有边的目标节点存在
	for from, tos := range g.edges {
		for _, to := range tos {
			if to != END && g.nodes[to] == nil {
				return fmt.Errorf("edge %s -> %s: target node not found", from, to)
			}
		}
	}

	// 3. 验证所有条件边的路由目标存在
	for from, cond := range g.conditions {
		for key, to := range cond.routes {
			if to != END && g.nodes[to] == nil {
				return fmt.Errorf("conditional edge %s -[%s]-> %s: target node not found", from, key, to)
			}
		}
	}

	// 4. 检测循环（简单 DFS）
	if err := g.detectCycles(); err != nil {
		return err
	}

	g.compiled = true
	return nil
}

func (g *graph) detectCycles() error {
	// 环检测策略：
	// 1. 无条件环（纯普通边形成）→ 拒绝（必定死循环）
	// 2. 条件环（包含条件边）→ 允许（由 MaxIterations 保护）
	//
	// 业界实践：LangGraph、EINO 都支持条件循环
	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	var dfs func(node string, viaCondition bool) error
	dfs = func(node string, viaCondition bool) error {
		if recStack[node] {
			// 发现环：检查是否为条件环
			if !viaCondition {
				return fmt.Errorf("cycle detected at node %s (unconditional cycle not allowed)", node)
			}
			// 条件环：允许（由 MaxIterations 保护）
			return nil
		}
		if visited[node] {
			return nil
		}

		visited[node] = true
		recStack[node] = true

		// 检查普通边（无条件）
		for _, next := range g.edges[node] {
			if next != END {
				if err := dfs(next, viaCondition); err != nil {
					return err
				}
			}
		}

		// 检查条件边（标记为条件路径）
		if cond, exists := g.conditions[node]; exists {
			for _, next := range cond.routes {
				if next != END {
					if err := dfs(next, true); err != nil {
						return err
					}
				}
			}
		}

		recStack[node] = false
		return nil
	}

	return dfs(g.entryPoint, false)
}

func (g *graph) Run(ctx context.Context, input GraphState) (GraphState, error) {
	g.mu.RLock()
	if !g.compiled {
		g.mu.RUnlock()
		return GraphState{}, fmt.Errorf("graph not compiled")
	}
	mode := g.config.ExecutionMode
	g.mu.RUnlock()

	if mode == ExecutionModeConcurrent {
		return g.runConcurrent(ctx, input)
	}
	return g.runSequential(ctx, input)
}

func (g *graph) runSequential(ctx context.Context, input GraphState) (GraphState, error) {
	state := input
	current := g.entryPoint
	visited := make(map[string]int)

	for current != END {
		// 检查迭代次数
		visited[current]++
		if visited[current] > g.config.MaxIterations {
			return state, fmt.Errorf("max iterations exceeded at node %s", current)
		}

		// 检查上下文
		select {
		case <-ctx.Done():
			return state, ctx.Err()
		default:
		}

		// 执行当前节点
		nodeFn := g.nodes[current]
		newState, err := nodeFn(ctx, state)
		if err != nil {
			return state, fmt.Errorf("node %s failed: %w", current, err)
		}
		state = newState

		// 决定下一跳
		next, err := g.getNextNode(ctx, current, state)
		if err != nil {
			return state, err
		}
		current = next
	}

	return state, nil
}

func (g *graph) runConcurrent(ctx context.Context, input GraphState) (GraphState, error) {
	// 并发模式：拓扑排序 + 分层并行执行
	// 策略：识别没有依赖关系的节点，分层并行执行

	// 1. 计算每个节点的入度和拓扑层次
	layers, err := g.topologicalLayers()
	if err != nil {
		return GraphState{}, err
	}

	// 2. 按层执行（每层内并行，层间串行）
	state := input.Clone()
	visited := make(map[string]bool)

	for iteration := 0; iteration < g.config.MaxIterations; iteration++ {
		// 检查上下文取消
		select {
		case <-ctx.Done():
			return *state, ctx.Err()
		default:
		}

		// 找到当前可执行的层（所有前驱都已访问）
		currentLayer := g.findExecutableLayer(layers, visited)
		if len(currentLayer) == 0 {
			// 没有更多可执行节点，检查是否到达终点
			return *state, nil
		}

		// 并行执行当前层的所有节点
		layerResults := make([]GraphState, len(currentLayer))
		errChan := make(chan error, len(currentLayer))

		var wg sync.WaitGroup
		for i, nodeName := range currentLayer {
			wg.Add(1)
			go func(idx int, name string) {
				defer wg.Done()

				// 每个节点使用独立的 state 副本
				nodeState := state.Clone()
				nodeFn := g.nodes[name]

				result, err := nodeFn(ctx, *nodeState)
				if err != nil {
					errChan <- fmt.Errorf("node %s failed: %w", name, err)
					return
				}

				layerResults[idx] = result
			}(i, nodeName)
		}

		wg.Wait()
		close(errChan)

		// 检查是否有错误
		if err := <-errChan; err != nil {
			return *state, err
		}

		// 合并所有节点的结果
		for _, result := range layerResults {
			state.Merge(&result)
		}

		// 标记当前层的节点为已访问
		for _, nodeName := range currentLayer {
			visited[nodeName] = true
		}

		// 检查是否所有节点都已执行
		if len(visited) == len(g.nodes) {
			return *state, nil
		}
	}

	return *state, fmt.Errorf("max iterations (%d) reached", g.config.MaxIterations)
}

// topologicalLayers 计算拓扑层次（Kahn 算法变体）
// 返回值：map[层级][]节点名
func (g *graph) topologicalLayers() (map[int][]string, error) {
	// 计算入度
	inDegree := make(map[string]int)
	for name := range g.nodes {
		inDegree[name] = 0
	}

	// 统计每个节点的入度
	for _, tos := range g.edges {
		for _, to := range tos {
			if to != END {
				inDegree[to]++
			}
		}
	}
	for _, cond := range g.conditions {
		for _, to := range cond.routes {
			if to != END {
				inDegree[to]++
			}
		}
	}

	// 分层：入度为 0 的节点在第 0 层
	layers := make(map[int][]string)
	nodeLayer := make(map[string]int)

	// 初始化：入度为 0 的节点（通常是 entryPoint）
	queue := []string{}
	for name, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, name)
			nodeLayer[name] = 0
			layers[0] = append(layers[0], name)
		}
	}

	// BFS 分层
	processed := 0
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		processed++

		currentLayerNum := nodeLayer[current]

		// 处理所有出边
		for _, next := range g.edges[current] {
			if next != END {
				inDegree[next]--
				if inDegree[next] == 0 {
					nextLayer := currentLayerNum + 1
					nodeLayer[next] = nextLayer
					layers[nextLayer] = append(layers[nextLayer], next)
					queue = append(queue, next)
				}
			}
		}

		// 处理条件边
		if cond, exists := g.conditions[current]; exists {
			for _, next := range cond.routes {
				if next != END {
					inDegree[next]--
					if inDegree[next] == 0 {
						nextLayer := currentLayerNum + 1
						nodeLayer[next] = nextLayer
						layers[nextLayer] = append(layers[nextLayer], next)
						queue = append(queue, next)
					}
				}
			}
		}
	}

	// 检查是否有环（如果处理的节点数少于总节点数，说明有环）
	if processed < len(g.nodes) {
		return nil, fmt.Errorf("graph contains cycle, cannot perform topological sort")
	}

	return layers, nil
}

// findExecutableLayer 找到当前可执行的节点（所有前驱都已访问）
func (g *graph) findExecutableLayer(layers map[int][]string, visited map[string]bool) []string {
	for layer := 0; layer < len(layers); layer++ {
		nodes := layers[layer]
		executable := []string{}

		for _, name := range nodes {
			if visited[name] {
				continue
			}

			// 检查所有前驱是否都已访问
			allPredsVisited := true
			for pred := range g.nodes {
				if pred == name {
					continue
				}

				// 检查 pred -> name 是否有边
				hasPredEdge := false
				for _, to := range g.edges[pred] {
					if to == name {
						hasPredEdge = true
						break
					}
				}
				if !hasPredEdge {
					if cond, exists := g.conditions[pred]; exists {
						for _, to := range cond.routes {
							if to == name {
								hasPredEdge = true
								break
							}
						}
					}
				}

				if hasPredEdge && !visited[pred] {
					allPredsVisited = false
					break
				}
			}

			if allPredsVisited {
				executable = append(executable, name)
			}
		}

		if len(executable) > 0 {
			return executable
		}
	}

	return nil
}

func (g *graph) getNextNode(ctx context.Context, current string, state GraphState) (string, error) {
	// 1. 检查条件边（优先级高）
	if cond, exists := g.conditions[current]; exists {
		routeKey, err := cond.condition(ctx, state)
		if err != nil {
			return "", fmt.Errorf("condition at node %s failed: %w", current, err)
		}

		next, exists := cond.routes[routeKey]
		if !exists {
			return "", fmt.Errorf("no route for key %s at node %s", routeKey, current)
		}
		return next, nil
	}

	// 2. 检查普通边
	edges := g.edges[current]
	if len(edges) == 0 {
		// 没有出边，默认结束
		return END, nil
	}
	if len(edges) > 1 {
		return "", fmt.Errorf("node %s has multiple edges but no condition", current)
	}

	return edges[0], nil
}
