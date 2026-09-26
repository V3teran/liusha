package main

import "fmt"

// GraphStats 是节点类型统计（从新 API 获取）
type GraphStats struct {
	Objectives   int `json:"objectives"`
	Actions      int `json:"actions"`
	Observations int `json:"observations"`
	Results      int `json:"results"`
}

// Path 是必须存在的推理路径（用于验收标准）
type Path struct {
	From string // 源节点类型: "objective"
	Via  string // 关系类型: "GENERATES"
	To   string // 目标节点类型: "action"
}

// AcceptanceCriteria 是新的验收标准（替代旧的 minFindings）
type AcceptanceCriteria struct {
	MinObjectives int    // 至少产生多少个目标
	MinActions    int    // 至少执行多少个动作
	MinResults    int    // 至少验证多少个结果
	MustHavePaths []Path // 必须存在的路径（可选）
}

// IsMetBy 检查统计数据是否满足验收标准
func (ac AcceptanceCriteria) IsMetBy(stats GraphStats) bool {
	if stats.Objectives < ac.MinObjectives {
		return false
	}
	if stats.Actions < ac.MinActions {
		return false
	}
	if stats.Results < ac.MinResults {
		return false
	}

	// TODO: 验证 MustHavePaths（需要查询边，Phase 2.2 实现）

	return true
}

// DiagnosticMessage 返回诊断消息（未满足时）
func (ac AcceptanceCriteria) DiagnosticMessage(stats GraphStats) string {
	var msg string

	if stats.Objectives < ac.MinObjectives {
		msg += fmt.Sprintf("目标不足: %d/%d; ", stats.Objectives, ac.MinObjectives)
	}
	if stats.Actions < ac.MinActions {
		msg += fmt.Sprintf("动作不足: %d/%d; ", stats.Actions, ac.MinActions)
	}
	if stats.Results < ac.MinResults {
		msg += fmt.Sprintf("结果不足: %d/%d; ", stats.Results, ac.MinResults)
	}

	if msg == "" {
		return "所有标准已满足"
	}

	return msg
}

// IsStuck 检测任务是否卡住（无进展）
func IsStuck(stats GraphStats) bool {
	// 如果有 objective 但没有 action，说明 Planner 生成目标后 Executor 没动
	if stats.Objectives > 0 && stats.Actions == 0 {
		return true
	}

	return false
}

// StuckReason 返回卡住的原因
func StuckReason(stats GraphStats) string {
	if stats.Objectives > 0 && stats.Actions == 0 {
		return fmt.Sprintf("Planner 已生成 %d 个目标，但 Executor 未执行任何动作", stats.Objectives)
	}

	return ""
}
