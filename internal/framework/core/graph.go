package core

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
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

	// AddSubgraph 添加子图作为节点
	AddSubgraph(name string, subgraph Graph) error

	// AddLambda 添加轻量级转换节点（语法糖）
	AddLambda(name string, fn func(state GraphState) GraphState) error

	// AddEdge 添加边（from → to）
	AddEdge(from, to string) error

	// AddConditionalEdge 添加条件边
	// condition 函数返回路由键，routes 映射键到目标节点
	AddConditionalEdge(from string, condition ConditionFunc, routes map[string]string) error

	// AddConditionalRoutes 添加多条件路由
	// routes 按顺序评估，第一个满足条件的生效
	// defaultTarget 在所有条件都不满足时使用
	AddConditionalRoutes(from string, routes []ConditionalRoute, defaultTarget string) error

	// SetInterruptBefore 设置节点执行前中断点
	SetInterruptBefore(nodes ...string) error

	// SetInterruptAfter 设置节点执行后中断点
	SetInterruptAfter(nodes ...string) error

	// Compile 编译图（检查完整性、构建拓扑）
	Compile() error

	// Run 执行图
	Run(ctx context.Context, input GraphState) (GraphState, error)

	// Stream 执行图并返回事件流（实时输出节点执行进度）
	Stream(ctx context.Context, input GraphState) (<-chan *StreamEvent, error)

	// Send 动态发送消息到指定节点（用于 Fan-out/Fan-in、Map-Reduce 模式）
	// 注意：Send 只能在节点执行期间调用，且目标节点必须存在
	Send(nodeName string, state GraphState) error

	// Resume 从中断点恢复执行
	Resume(ctx context.Context, checkpoint *InterruptCheckpoint) (GraphState, error)

	// AsNode 将图转换为节点函数（用于嵌套）
	AsNode() NodeFunc
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

// PredicateFunc 是布尔谓词函数（用于多条件分支）
//
// 参数：
// - ctx：上下文
// - state：当前状态
//
// 返回：
// - bool：条件是否满足
// - error：判断错误
type PredicateFunc func(ctx context.Context, state GraphState) (bool, error)

// ConditionalRoute 定义单个条件路由规则
type ConditionalRoute struct {
	// Condition 条件谓词
	Condition PredicateFunc

	// Target 目标节点（条件满足时跳转）
	Target string
}

// InterruptType 定义中断类型
type InterruptType string

const (
	// InterruptBefore 节点执行前中断
	InterruptBefore InterruptType = "before"

	// InterruptAfter 节点执行后中断
	InterruptAfter InterruptType = "after"
)

// InterruptCheckpoint 是中断时的执行快照，用于恢复执行
type InterruptCheckpoint struct {
	// CurrentNode 当前节点（中断点）
	CurrentNode string

	// State 当前状态
	State GraphState

	// InterruptType 中断类型
	InterruptType InterruptType

	// Visited 已访问节点计数（防止无限循环）
	Visited map[string]int
}

// InterruptError 表示执行在中断点停止（非真正的错误）
type InterruptError struct {
	Checkpoint *InterruptCheckpoint
}

func (e *InterruptError) Error() string {
	return fmt.Sprintf("interrupted at node %s (%s)", e.Checkpoint.CurrentNode, e.Checkpoint.InterruptType)
}

// IsInterrupt 判断错误是否为中断
func IsInterrupt(err error) bool {
	_, ok := err.(*InterruptError)
	return ok
}

// GetInterruptCheckpoint 从中断错误中提取 InterruptCheckpoint
func GetInterruptCheckpoint(err error) *InterruptCheckpoint {
	if ie, ok := err.(*InterruptError); ok {
		return ie.Checkpoint
	}
	return nil
}

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

// MergeStrategy 定义状态合并策略
type MergeStrategy int

const (
	// MergeStrategyOverwrite 覆盖策略：后来的值覆盖之前的值（默认）
	MergeStrategyOverwrite MergeStrategy = iota

	// MergeStrategyKeepFirst 保留首个：保留第一个遇到的值，忽略后续
	MergeStrategyKeepFirst

	// MergeStrategyAppend 追加策略：将值追加到切片（要求值为切片类型）
	MergeStrategyAppend
)

// ReducerFunc 是自定义合并函数
// 参数：
// - key: 冲突的键
// - existing: 当前状态中的值
// - incoming: 待合并的新值
// 返回：
// - interface{}: 合并后的值
type ReducerFunc func(key string, existing, incoming interface{}) interface{}

// MergeOptions 定义合并选项
type MergeOptions struct {
	// Strategy 全局合并策略
	Strategy MergeStrategy

	// KeyReducers 按键自定义合并函数（优先级高于全局策略）
	KeyReducers map[string]ReducerFunc
}

// Merge 使用默认策略（覆盖）合并另一个 GraphState
func (s *GraphState) Merge(other *GraphState) {
	s.MergeWithOptions(other, nil)
}

// MergeWithOptions 使用指定选项合并另一个 GraphState
func (s *GraphState) MergeWithOptions(other *GraphState, options *MergeOptions) {
	s.mu.Lock()
	defer s.mu.Unlock()

	other.mu.RLock()
	defer other.mu.RUnlock()

	// 默认选项
	if options == nil {
		options = &MergeOptions{
			Strategy: MergeStrategyOverwrite,
		}
	}

	for k, incomingVal := range other.data {
		// 检查是否有自定义 reducer
		if reducer, exists := options.KeyReducers[k]; exists {
			if existingVal, hasExisting := s.data[k]; hasExisting {
				s.data[k] = reducer(k, existingVal, incomingVal)
			} else {
				s.data[k] = incomingVal
			}
			continue
		}

		// 使用全局策略
		switch options.Strategy {
		case MergeStrategyOverwrite:
			s.data[k] = incomingVal

		case MergeStrategyKeepFirst:
			if _, exists := s.data[k]; !exists {
				s.data[k] = incomingVal
			}

		case MergeStrategyAppend:
			if existingVal, exists := s.data[k]; exists {
				// 尝试追加到切片
				s.data[k] = appendToSlice(existingVal, incomingVal)
			} else {
				// 第一次出现，包装成切片
				s.data[k] = []interface{}{incomingVal}
			}
		}
	}
}

// appendToSlice 将值追加到切片
func appendToSlice(existing, incoming interface{}) interface{} {
	// 尝试转换为 []interface{}
	switch existingSlice := existing.(type) {
	case []interface{}:
		return append(existingSlice, incoming)
	default:
		// 如果不是切片，创建新切片
		return []interface{}{existing, incoming}
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

	// MergeOptions 并发执行时的状态合并选项（仅在 ExecutionModeConcurrent 时生效）
	MergeOptions *MergeOptions

	// Checkpointer 检查点管理器（可选）
	Checkpointer Checkpointer

	// CheckpointStrategy 检查点保存策略（可选）
	CheckpointStrategy CheckpointStrategy

	// TaskID 任务 ID（用于 Checkpoint）
	TaskID string

	// AutoCheckpoint 是否在每个节点后自动保存检查点
	AutoCheckpoint bool
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
		nodes:           make(map[string]NodeFunc),
		edges:           make(map[string][]string),
		conditions:      make(map[string]conditionalEdge),
		multiConditions: make(map[string]multiConditionalRouting),
		interruptBefore: make(map[string]bool),
		interruptAfter:  make(map[string]bool),
		entryPoint:      entryPoint,
		config:          config,
	}
}

// graph 是 Graph 的实现
type graph struct {
	nodes           map[string]NodeFunc
	edges           map[string][]string
	conditions      map[string]conditionalEdge
	multiConditions map[string]multiConditionalRouting
	interruptBefore map[string]bool // 节点执行前中断
	interruptAfter  map[string]bool // 节点执行后中断
	entryPoint      string
	config          *GraphConfig
	compiled        bool
	mu              sync.RWMutex

	// 动态消息队列（用于 Send API）
	messageQueue chan *pendingMessage
	queueMu      sync.Mutex
	executing    bool // 是否正在执行中

	// Checkpoint 相关
	checkpointStartTime time.Time
}

// pendingMessage 是待执行的消息
type pendingMessage struct {
	nodeName string
	state    GraphState
}

type conditionalEdge struct {
	condition ConditionFunc
	routes    map[string]string
}

type multiConditionalRouting struct {
	routes        []ConditionalRoute
	defaultTarget string
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

func (g *graph) AddSubgraph(name string, subgraph Graph) error {
	// 子图必须先编译
	if sg, ok := subgraph.(*graph); ok {
		sg.mu.RLock()
		compiled := sg.compiled
		sg.mu.RUnlock()

		if !compiled {
			return fmt.Errorf("subgraph must be compiled before adding")
		}
	}

	// 将子图转换为节点函数
	return g.AddNode(name, subgraph.AsNode())
}

func (g *graph) AddLambda(name string, fn func(state GraphState) GraphState) error {
	// Lambda 转换为 NodeFunc
	nodeFunc := func(ctx context.Context, state GraphState) (GraphState, error) {
		// 简单转换：忽略 context，不返回错误
		return fn(state), nil
	}
	return g.AddNode(name, nodeFunc)
}

func (g *graph) AsNode() NodeFunc {
	return func(ctx context.Context, state GraphState) (GraphState, error) {
		// 子图以当前状态为输入执行
		return g.Run(ctx, state)
	}
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

func (g *graph) AddConditionalRoutes(from string, routes []ConditionalRoute, defaultTarget string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.compiled {
		return fmt.Errorf("cannot add conditional routes after compilation")
	}

	if len(routes) == 0 {
		return fmt.Errorf("routes cannot be empty")
	}

	if defaultTarget == "" {
		return fmt.Errorf("defaultTarget is required")
	}

	g.multiConditions[from] = multiConditionalRouting{
		routes:        routes,
		defaultTarget: defaultTarget,
	}
	return nil
}

func (g *graph) SetInterruptBefore(nodes ...string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.compiled {
		return fmt.Errorf("cannot set interrupt after compilation")
	}

	for _, node := range nodes {
		g.interruptBefore[node] = true
	}
	return nil
}

func (g *graph) SetInterruptAfter(nodes ...string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.compiled {
		return fmt.Errorf("cannot set interrupt after compilation")
	}

	for _, node := range nodes {
		g.interruptAfter[node] = true
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

	// 4. 验证所有多条件路由的目标存在
	for from, mcr := range g.multiConditions {
		for i, route := range mcr.routes {
			if route.Target != END && g.nodes[route.Target] == nil {
				return fmt.Errorf("multi-conditional route %s [%d]-> %s: target node not found", from, i, route.Target)
			}
		}
		if mcr.defaultTarget != END && g.nodes[mcr.defaultTarget] == nil {
			return fmt.Errorf("multi-conditional route %s (default)-> %s: target node not found", from, mcr.defaultTarget)
		}
	}

	// 5. 检测循环（简单 DFS）
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

		// 检查多条件路由（标记为条件路径）
		if mcr, exists := g.multiConditions[node]; exists {
			for _, route := range mcr.routes {
				if route.Target != END {
					if err := dfs(route.Target, true); err != nil {
						return err
					}
				}
			}
			if mcr.defaultTarget != END {
				if err := dfs(mcr.defaultTarget, true); err != nil {
					return err
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

	// 初始化消息队列
	g.queueMu.Lock()
	g.messageQueue = make(chan *pendingMessage, 100)
	g.executing = true
	g.queueMu.Unlock()

	defer func() {
		g.queueMu.Lock()
		close(g.messageQueue)
		g.executing = false
		g.queueMu.Unlock()
	}()

	if mode == ExecutionModeConcurrent {
		return g.runConcurrentWithSend(ctx, input)
	}
	return g.runSequentialWithSend(ctx, input)
}

func (g *graph) Stream(ctx context.Context, input GraphState) (<-chan *StreamEvent, error) {
	g.mu.RLock()
	if !g.compiled {
		g.mu.RUnlock()
		return nil, fmt.Errorf("graph not compiled")
	}
	mode := g.config.ExecutionMode
	g.mu.RUnlock()

	// 创建事件通道（带缓冲，避免阻塞）
	eventChan := make(chan *StreamEvent, 100)

	// 在后台执行图，发射事件
	go func() {
		defer close(eventChan)

		var finalState GraphState
		var finalErr error

		if mode == ExecutionModeConcurrent {
			finalState, finalErr = g.runConcurrentWithEvents(ctx, input, eventChan)
		} else {
			finalState, finalErr = g.runSequentialWithEvents(ctx, input, eventChan)
		}

		// 发送最终状态事件
		if finalErr != nil {
			eventChan <- &StreamEvent{
				Type:      StreamEventTypeError,
				Data:      map[string]any{"error": finalErr.Error()},
				Timestamp: nowMillis(),
				Done:      true,
			}
		} else {
			eventChan <- &StreamEvent{
				Type:      StreamEventTypeState,
				Data:      map[string]any{"state": finalState, "final": true},
				Timestamp: nowMillis(),
				Done:      true,
			}
		}
	}()

	return eventChan, nil
}

func (g *graph) runSequentialWithEvents(ctx context.Context, input GraphState, events chan<- *StreamEvent) (GraphState, error) {
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

		// 检查节点执行前中断点
		if g.interruptBefore[current] {
			return state, &InterruptError{
				Checkpoint: &InterruptCheckpoint{
					CurrentNode:   current,
					State:         state,
					InterruptType: InterruptBefore,
					Visited:       visited,
				},
			}
		}

		// 发送节点开始事件
		events <- &StreamEvent{
			Type: StreamEventTypeNodeStart,
			Data: map[string]any{
				"node":  current,
				"state": state,
			},
			Timestamp: nowMillis(),
		}

		// 执行当前节点
		nodeFn := g.nodes[current]
		newState, err := nodeFn(ctx, state)
		if err != nil {
			// 发送错误事件
			events <- &StreamEvent{
				Type: StreamEventTypeError,
				Data: map[string]any{
					"node":  current,
					"error": err.Error(),
				},
				Timestamp: nowMillis(),
			}
			return state, fmt.Errorf("node %s failed: %w", current, err)
		}
		state = newState

		// 发送节点完成事件
		events <- &StreamEvent{
			Type: StreamEventTypeNodeEnd,
			Data: map[string]any{
				"node":  current,
				"state": state,
			},
			Timestamp: nowMillis(),
		}

		// 检查节点执行后中断点
		if g.interruptAfter[current] {
			return state, &InterruptError{
				Checkpoint: &InterruptCheckpoint{
					CurrentNode:   current,
					State:         state,
					InterruptType: InterruptAfter,
					Visited:       visited,
				},
			}
		}

		// 决定下一跳
		next, err := g.getNextNode(ctx, current, state)
		if err != nil {
			return state, err
		}
		current = next
	}

	return state, nil
}

func (g *graph) runConcurrentWithEvents(ctx context.Context, input GraphState, events chan<- *StreamEvent) (GraphState, error) {
	// 并发模式：拓扑排序 + 分层并行执行 + 实时事件

	layers, err := g.topologicalLayers()
	if err != nil {
		return GraphState{}, err
	}

	state := input.Clone()
	visited := make(map[string]bool)

	for iteration := 0; iteration < g.config.MaxIterations; iteration++ {
		select {
		case <-ctx.Done():
			return *state, ctx.Err()
		default:
		}

		currentLayer := g.findExecutableLayer(layers, visited)
		if len(currentLayer) == 0 {
			return *state, nil
		}

		// 并行执行当前层 + 发送事件
		layerResults := make([]GraphState, len(currentLayer))
		errChan := make(chan error, len(currentLayer))

		var wg sync.WaitGroup
		for i, nodeName := range currentLayer {
			wg.Add(1)
			go func(idx int, name string) {
				defer wg.Done()

				// 发送 NodeStart 事件
				events <- &StreamEvent{
					Type: StreamEventTypeNodeStart,
					Data: map[string]any{
						"node":  name,
						"state": state,
					},
					Timestamp: nowMillis(),
				}

				nodeState := state.Clone()
				nodeFn := g.nodes[name]

				result, err := nodeFn(ctx, *nodeState)
				if err != nil {
					// 发送 Error 事件
					events <- &StreamEvent{
						Type: StreamEventTypeError,
						Data: map[string]any{
							"node":  name,
							"error": err.Error(),
						},
						Timestamp: nowMillis(),
					}
					errChan <- fmt.Errorf("node %s failed: %w", name, err)
					return
				}

				layerResults[idx] = result

				// 发送 NodeEnd 事件
				events <- &StreamEvent{
					Type: StreamEventTypeNodeEnd,
					Data: map[string]any{
						"node":  name,
						"state": &result,
					},
					Timestamp: nowMillis(),
				}
			}(i, nodeName)
		}

		wg.Wait()
		close(errChan)

		if err := <-errChan; err != nil {
			return *state, err
		}

		// 合并结果
		for _, result := range layerResults {
			state.MergeWithOptions(&result, g.config.MergeOptions)
		}

		// 发送 State 事件
		events <- &StreamEvent{
			Type: StreamEventTypeState,
			Data: map[string]any{
				"state": state,
			},
			Timestamp: nowMillis(),
		}

		for _, nodeName := range currentLayer {
			visited[nodeName] = true
		}

		if len(visited) == len(g.nodes) {
			return *state, nil
		}
	}

	return *state, fmt.Errorf("max iterations (%d) reached", g.config.MaxIterations)
}

// nowMillis 返回当前时间戳（毫秒）
func nowMillis() int64 {
	return timeNow().UnixNano() / 1e6
}

// timeNow 是时间函数（便于测试）
var timeNow = func() time.Time {
	return time.Now()
}

func (g *graph) Send(nodeName string, state GraphState) error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// 检查图是否已编译
	if !g.compiled {
		return fmt.Errorf("graph not compiled")
	}

	// 检查目标节点是否存在
	if _, exists := g.nodes[nodeName]; !exists {
		return fmt.Errorf("node %s not found", nodeName)
	}

	// 检查是否在执行期间
	g.queueMu.Lock()
	defer g.queueMu.Unlock()

	if !g.executing {
		return fmt.Errorf("Send can only be called during graph execution")
	}

	// 将消息加入队列
	if g.messageQueue == nil {
		return fmt.Errorf("message queue not initialized")
	}

	msg := &pendingMessage{
		nodeName: nodeName,
		state:    state,
	}

	select {
	case g.messageQueue <- msg:
		return nil
	default:
		return fmt.Errorf("message queue is full")
	}
}

func (g *graph) runSequentialWithSend(ctx context.Context, input GraphState) (GraphState, error) {
	state := input
	current := g.entryPoint
	visited := make(map[string]int)

	// 用于收集所有结果（包括 Send 产生的）
	results := []GraphState{}

	// 初始化 checkpoint 开始时间
	g.checkpointStartTime = time.Now()

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

		// 检查节点执行前中断点
		if g.interruptBefore[current] {
			return state, &InterruptError{
				Checkpoint: &InterruptCheckpoint{
					CurrentNode:   current,
					State:         state,
					InterruptType: InterruptBefore,
					Visited:       visited,
				},
			}
		}

		// 执行当前节点
		nodeFn := g.nodes[current]
		newState, err := nodeFn(ctx, state)
		if err != nil {
			return state, fmt.Errorf("node %s failed: %w", current, err)
		}
		state = newState

		// 自动保存 checkpoint（如果配置了）
		if g.config.AutoCheckpoint && g.config.Checkpointer != nil {
			if err := g.saveCheckpoint(ctx, current, state); err != nil {
				// Checkpoint 失败不中断执行，仅记录错误
				// 可以通过 callback 通知
				_ = err
			}
		}

		// 检查节点执行后中断点
		if g.interruptAfter[current] {
			return state, &InterruptError{
				Checkpoint: &InterruptCheckpoint{
					CurrentNode:   current,
					State:         state,
					InterruptType: InterruptAfter,
					Visited:       visited,
				},
			}
		}

		// 决定下一跳
		next, err := g.getNextNode(ctx, current, state)
		if err != nil {
			return state, err
		}
		current = next
	}

	// 处理消息队列中的待处理消息
	for {
		select {
		case msg, ok := <-g.messageQueue:
			if !ok {
				// 队列已关闭
				goto done
			}

			// 执行 Send 的节点
			nodeFn := g.nodes[msg.nodeName]
			resultState, err := nodeFn(ctx, msg.state)
			if err != nil {
				return state, fmt.Errorf("send node %s failed: %w", msg.nodeName, err)
			}
			results = append(results, resultState)

		default:
			// 没有待处理消息
			goto done
		}
	}

done:
	// 如果有 Send 产生的结果，合并它们
	if len(results) > 0 {
		for _, r := range results {
			state.Merge(&r)
		}
	}

	return state, nil
}

func (g *graph) runConcurrentWithSend(ctx context.Context, input GraphState) (GraphState, error) {
	// 并发模式 + Send 消息队列（消息队列已在 Run 中初始化）

	layers, err := g.topologicalLayers()
	if err != nil {
		return GraphState{}, err
	}

	state := input.Clone()
	visited := make(map[string]bool)

	for iteration := 0; iteration < g.config.MaxIterations; iteration++ {
		select {
		case <-ctx.Done():
			return *state, ctx.Err()
		default:
		}

		currentLayer := g.findExecutableLayer(layers, visited)
		if len(currentLayer) == 0 {
			// 检查消息队列
			if len(g.messageQueue) > 0 {
				// 处理消息队列中的待执行节点
				msg := <-g.messageQueue
				nodeFn := g.nodes[msg.nodeName]
				result, err := nodeFn(ctx, msg.state)
				if err != nil {
					return *state, fmt.Errorf("message node %s failed: %w", msg.nodeName, err)
				}
				state.MergeWithOptions(&result, g.config.MergeOptions)
				continue
			}
			return *state, nil
		}

		// 并行执行当前层
		layerResults := make([]GraphState, len(currentLayer))
		var layerError error
		var layerErrorMu sync.Mutex

		var wg sync.WaitGroup
		for i, nodeName := range currentLayer {
			wg.Add(1)
			go func(idx int, name string) {
				defer wg.Done()

				nodeState := state.Clone()
				nodeFn := g.nodes[name]

				result, err := nodeFn(ctx, *nodeState)
				if err != nil {
					layerErrorMu.Lock()
					if layerError == nil {
						layerError = fmt.Errorf("node %s failed: %w", name, err)
					}
					layerErrorMu.Unlock()
					return
				}

				layerResults[idx] = result
			}(i, nodeName)
		}

		wg.Wait()

		if layerError != nil {
			return *state, layerError
		}

		// 合并结果
		for _, result := range layerResults {
			state.MergeWithOptions(&result, g.config.MergeOptions)
		}

		for _, nodeName := range currentLayer {
			visited[nodeName] = true
		}

		if len(visited) == len(g.nodes) {
			// 处理剩余消息
			for len(g.messageQueue) > 0 {
				msg := <-g.messageQueue
				nodeFn := g.nodes[msg.nodeName]
				result, err := nodeFn(ctx, msg.state)
				if err != nil {
					return *state, fmt.Errorf("message node %s failed: %w", msg.nodeName, err)
				}
				state.MergeWithOptions(&result, g.config.MergeOptions)
			}
			return *state, nil
		}
	}

	return *state, fmt.Errorf("max iterations (%d) reached", g.config.MaxIterations)
}

// saveCheckpoint 保存检查点
func (g *graph) saveCheckpoint(ctx context.Context, nodeName string, state GraphState) error {
	// 检查是否应该保存
	elapsed := time.Since(g.checkpointStartTime)
	if g.config.CheckpointStrategy != nil {
		if !g.config.CheckpointStrategy.ShouldSave(ctx, nodeName, elapsed) {
			return nil
		}
	}

	// 序列化状态
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// 创建检查点
	checkpoint := Checkpoint{
		ID:            CheckpointID(fmt.Sprintf("%s_%s_%d", g.config.TaskID, nodeName, time.Now().UnixNano())),
		TaskID:        g.config.TaskID,
		StateSnapshot: stateJSON,
		Phase:         nodeName,
		Labels: map[string]string{
			"node": nodeName,
			"type": "auto",
		},
		CreatedAt: time.Now(),
		SizeBytes: int64(len(stateJSON)),
	}

	// 保存检查点
	_, err = g.config.Checkpointer.Save(ctx, checkpoint)
	return err
}

// LoadFromCheckpoint 从检查点恢复图执行
func (g *graph) LoadFromCheckpoint(ctx context.Context, checkpointID CheckpointID) (GraphState, error) {
	if g.config.Checkpointer == nil {
		return GraphState{}, fmt.Errorf("no checkpointer configured")
	}

	// 加载检查点
	checkpoint, err := g.config.Checkpointer.Load(ctx, checkpointID)
	if err != nil {
		return GraphState{}, fmt.Errorf("failed to load checkpoint: %w", err)
	}

	// 反序列化状态
	var state GraphState
	if err := json.Unmarshal(checkpoint.StateSnapshot, &state); err != nil {
		return GraphState{}, fmt.Errorf("failed to unmarshal state: %w", err)
	}

	return state, nil
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

		// 检查节点执行前中断点
		if g.interruptBefore[current] {
			return state, &InterruptError{
				Checkpoint: &InterruptCheckpoint{
					CurrentNode:   current,
					State:         state,
					InterruptType: InterruptBefore,
					Visited:       visited,
				},
			}
		}

		// 执行当前节点
		nodeFn := g.nodes[current]
		newState, err := nodeFn(ctx, state)
		if err != nil {
			return state, fmt.Errorf("node %s failed: %w", current, err)
		}
		state = newState

		// 检查节点执行后中断点
		if g.interruptAfter[current] {
			return state, &InterruptError{
				Checkpoint: &InterruptCheckpoint{
					CurrentNode:   current,
					State:         state,
					InterruptType: InterruptAfter,
					Visited:       visited,
				},
			}
		}

		// 决定下一跳
		next, err := g.getNextNode(ctx, current, state)
		if err != nil {
			return state, err
		}
		current = next
	}

	return state, nil
}

func (g *graph) Resume(ctx context.Context, checkpoint *InterruptCheckpoint) (GraphState, error) {
	g.mu.RLock()
	if !g.compiled {
		g.mu.RUnlock()
		return GraphState{}, fmt.Errorf("graph not compiled")
	}
	mode := g.config.ExecutionMode
	g.mu.RUnlock()

	if mode == ExecutionModeConcurrent {
		return GraphState{}, fmt.Errorf("resume not supported in concurrent mode")
	}

	// 从 checkpoint 恢复状态
	state := checkpoint.State
	visited := checkpoint.Visited

	var current string
	skipFirstInterrupt := false

	// 根据中断类型决定从哪里继续
	switch checkpoint.InterruptType {
	case InterruptBefore:
		// 中断在节点执行前，现在执行该节点（跳过该节点的 before 中断检查）
		current = checkpoint.CurrentNode
		skipFirstInterrupt = true

	case InterruptAfter:
		// 中断在节点执行后，跳到下一个节点
		next, err := g.getNextNode(ctx, checkpoint.CurrentNode, state)
		if err != nil {
			return state, err
		}
		current = next

	default:
		return state, fmt.Errorf("unknown interrupt type: %s", checkpoint.InterruptType)
	}

	// 继续执行
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

		// 检查节点执行前中断点（除非是恢复点需要跳过）
		if !skipFirstInterrupt && g.interruptBefore[current] {
			return state, &InterruptError{
				Checkpoint: &InterruptCheckpoint{
					CurrentNode:   current,
					State:         state,
					InterruptType: InterruptBefore,
					Visited:       visited,
				},
			}
		}
		skipFirstInterrupt = false // 只跳过第一次

		// 执行当前节点
		nodeFn := g.nodes[current]
		newState, err := nodeFn(ctx, state)
		if err != nil {
			return state, fmt.Errorf("node %s failed: %w", current, err)
		}
		state = newState

		// 检查节点执行后中断点
		if g.interruptAfter[current] {
			return state, &InterruptError{
				Checkpoint: &InterruptCheckpoint{
					CurrentNode:   current,
					State:         state,
					InterruptType: InterruptAfter,
					Visited:       visited,
				},
			}
		}

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

		// 合并所有节点的结果（使用配置的合并策略）
		for _, result := range layerResults {
			state.MergeWithOptions(&result, g.config.MergeOptions)
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
	for _, mcr := range g.multiConditions {
		for _, route := range mcr.routes {
			if route.Target != END {
				inDegree[route.Target]++
			}
		}
		if mcr.defaultTarget != END {
			inDegree[mcr.defaultTarget]++
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

		// 处理多条件路由
		if mcr, exists := g.multiConditions[current]; exists {
			for _, route := range mcr.routes {
				if route.Target != END {
					inDegree[route.Target]--
					if inDegree[route.Target] == 0 {
						nextLayer := currentLayerNum + 1
						nodeLayer[route.Target] = nextLayer
						layers[nextLayer] = append(layers[nextLayer], route.Target)
						queue = append(queue, route.Target)
					}
				}
			}
			if mcr.defaultTarget != END {
				inDegree[mcr.defaultTarget]--
				if inDegree[mcr.defaultTarget] == 0 {
					nextLayer := currentLayerNum + 1
					nodeLayer[mcr.defaultTarget] = nextLayer
					layers[nextLayer] = append(layers[nextLayer], mcr.defaultTarget)
					queue = append(queue, mcr.defaultTarget)
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
	// 1. 检查多条件路由（优先级最高）
	if mcr, exists := g.multiConditions[current]; exists {
		// 按顺序评估每个条件，短路求值
		for i, route := range mcr.routes {
			satisfied, err := route.Condition(ctx, state)
			if err != nil {
				return "", fmt.Errorf("condition [%d] at node %s failed: %w", i, current, err)
			}
			if satisfied {
				return route.Target, nil
			}
		}
		// 所有条件都不满足，使用默认路由
		return mcr.defaultTarget, nil
	}

	// 2. 检查条件边（单条件路由，向后兼容）
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

	// 3. 检查普通边
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
