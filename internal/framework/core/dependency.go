package core

import (
	"fmt"
)

// DependencyResolver 是依赖解析器。
type DependencyResolver struct {
	graph *GraphImpl
}

// NewDependencyResolver 创建依赖解析器。
func NewDependencyResolver(graph *GraphImpl) *DependencyResolver {
	return &DependencyResolver{graph: graph}
}

// IsSatisfied 判断节点的依赖是否满足。
func (r *DependencyResolver) IsSatisfied(nodeID string) bool {
	deps := r.graph.GetDependencies(nodeID)

	for _, depID := range deps {
		node, err := r.graph.GetNode(depID)
		if err != nil {
			return false
		}

		if node.State != NodeStateCompleted {
			return false
		}
	}

	return true
}

// DetectCycle 检测循环依赖。
func (r *DependencyResolver) DetectCycle() error {
	r.graph.mu.RLock()
	defer r.graph.mu.RUnlock()

	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	for nodeID := range r.graph.nodes {
		if !visited[nodeID] {
			if cycle := r.detectCycleDFS(nodeID, visited, recStack); len(cycle) > 0 {
				return ErrCyclicDependency{Cycle: cycle}
			}
		}
	}

	return nil
}

// detectCycleDFS DFS 检测循环。
func (r *DependencyResolver) detectCycleDFS(nodeID string, visited, recStack map[string]bool) []string {
	visited[nodeID] = true
	recStack[nodeID] = true

	// 遍历依赖的节点（即入边）
	for _, depID := range r.graph.dependencies[nodeID] {
		if !visited[depID] {
			if cycle := r.detectCycleDFS(depID, visited, recStack); len(cycle) > 0 {
				return append(cycle, nodeID)
			}
		} else if recStack[depID] {
			// 发现循环
			return []string{depID, nodeID}
		}
	}

	recStack[nodeID] = false
	return nil
}

// TopologicalSort 拓扑排序（返回可执行顺序）。
func (r *DependencyResolver) TopologicalSort() ([]string, error) {
	// 先检测循环
	if err := r.DetectCycle(); err != nil {
		return nil, err
	}

	r.graph.mu.RLock()
	defer r.graph.mu.RUnlock()

	// Kahn 算法
	inDegree := make(map[string]int)
	for nodeID := range r.graph.nodes {
		inDegree[nodeID] = len(r.graph.dependencies[nodeID])
	}

	// 找到所有入度为 0 的节点
	queue := make([]string, 0)
	for nodeID, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, nodeID)
		}
	}

	result := make([]string, 0, len(r.graph.nodes))

	for len(queue) > 0 {
		// 取出队首
		nodeID := queue[0]
		queue = queue[1:]
		result = append(result, nodeID)

		// 减少依赖此节点的节点的入度
		for _, depID := range r.graph.dependents[nodeID] {
			inDegree[depID]--
			if inDegree[depID] == 0 {
				queue = append(queue, depID)
			}
		}
	}

	// 检查是否所有节点都已排序
	if len(result) != len(r.graph.nodes) {
		return nil, fmt.Errorf("topological sort failed: graph has cycles")
	}

	return result, nil
}

// GetExecutionLevels 获取执行层级（同层节点可并发）。
func (r *DependencyResolver) GetExecutionLevels() ([][]string, error) {
	// 先检测循环
	if err := r.DetectCycle(); err != nil {
		return nil, err
	}

	r.graph.mu.RLock()
	defer r.graph.mu.RUnlock()

	inDegree := make(map[string]int)
	for nodeID := range r.graph.nodes {
		inDegree[nodeID] = len(r.graph.dependencies[nodeID])
	}

	levels := make([][]string, 0)

	for len(inDegree) > 0 {
		// 找到当前层（入度为 0 的节点）
		currentLevel := make([]string, 0)
		for nodeID, degree := range inDegree {
			if degree == 0 {
				currentLevel = append(currentLevel, nodeID)
			}
		}

		if len(currentLevel) == 0 {
			return nil, fmt.Errorf("cannot find next level: graph has cycles")
		}

		levels = append(levels, currentLevel)

		// 从图中移除当前层的节点
		for _, nodeID := range currentLevel {
			delete(inDegree, nodeID)

			// 减少依赖此节点的节点的入度
			for _, depID := range r.graph.dependents[nodeID] {
				if _, exists := inDegree[depID]; exists {
					inDegree[depID]--
				}
			}
		}
	}

	return levels, nil
}

// GetParallelGroups 获取并发组（按 ParallelGroup 字段分组）。
func (r *DependencyResolver) GetParallelGroups() map[string][]string {
	r.graph.mu.RLock()
	defer r.graph.mu.RUnlock()

	groups := make(map[string][]string)

	for nodeID, node := range r.graph.nodes {
		if node.ParallelGroup != "" {
			groups[node.ParallelGroup] = append(groups[node.ParallelGroup], nodeID)
		} else {
			// 没有指定并发组的节点单独执行
			groups[nodeID] = []string{nodeID}
		}
	}

	return groups
}

// GetReadyNodes 获取所有依赖已满足的节点。
func (r *DependencyResolver) GetReadyNodes() []string {
	r.graph.mu.RLock()
	defer r.graph.mu.RUnlock()

	ready := make([]string, 0)

	for nodeID, node := range r.graph.nodes {
		// 只考虑 pending 或 ready 状态
		if node.State != NodeStatePending && node.State != NodeStateReady {
			continue
		}

		// 检查依赖
		if r.IsSatisfied(nodeID) {
			ready = append(ready, nodeID)
		}
	}

	return ready
}

// GetBlockedNodes 获取所有被阻塞的节点（依赖失败）。
func (r *DependencyResolver) GetBlockedNodes() []string {
	r.graph.mu.RLock()
	defer r.graph.mu.RUnlock()

	blocked := make([]string, 0)

	for nodeID := range r.graph.nodes {
		if r.isBlockedByFailedDependency(nodeID) {
			blocked = append(blocked, nodeID)
		}
	}

	return blocked
}

// isBlockedByFailedDependency 检查节点是否因依赖失败而阻塞。
func (r *DependencyResolver) isBlockedByFailedDependency(nodeID string) bool {
	deps := r.graph.dependencies[nodeID]

	for _, depID := range deps {
		node, err := r.graph.GetNode(depID)
		if err != nil {
			continue
		}

		if node.State == NodeStateFailed || node.State == NodeStateBlocked {
			return true
		}
	}

	return false
}

// ComputeCriticalPath 计算关键路径（最长依赖链）。
func (r *DependencyResolver) ComputeCriticalPath() ([]string, int64, error) {
	order, err := r.TopologicalSort()
	if err != nil {
		return nil, 0, err
	}

	r.graph.mu.RLock()
	defer r.graph.mu.RUnlock()

	// 计算每个节点的最早开始时间
	earliestStart := make(map[string]int64)
	predecessor := make(map[string]string)

	for _, nodeID := range order {
		node := r.graph.nodes[nodeID]
		duration := node.Metadata.DurationMs
		if duration == 0 {
			duration = 1000 // 默认 1 秒
		}

		maxStart := int64(0)
		var predID string

		// 找到所有依赖的最大完成时间
		for _, depID := range r.graph.dependencies[nodeID] {
			depNode := r.graph.nodes[depID]
			depDuration := depNode.Metadata.DurationMs
			if depDuration == 0 {
				depDuration = 1000
			}

			depFinish := earliestStart[depID] + depDuration
			if depFinish > maxStart {
				maxStart = depFinish
				predID = depID
			}
		}

		earliestStart[nodeID] = maxStart
		if predID != "" {
			predecessor[nodeID] = predID
		}
	}

	// 找到完成时间最晚的节点（关键路径终点）
	var endNode string
	maxFinish := int64(0)
	for _, nodeID := range order {
		node := r.graph.nodes[nodeID]
		duration := node.Metadata.DurationMs
		if duration == 0 {
			duration = 1000
		}
		finish := earliestStart[nodeID] + duration
		if finish > maxFinish {
			maxFinish = finish
			endNode = nodeID
		}
	}

	// 回溯关键路径
	path := make([]string, 0)
	current := endNode
	for current != "" {
		path = append([]string{current}, path...)
		current = predecessor[current]
	}

	return path, maxFinish, nil
}
