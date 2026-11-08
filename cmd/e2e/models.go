package main

import "fmt"

// GraphStats 是节点类型统计（从新 API 获取）
type GraphStats struct {
	Objectives   int `json:"objectives"`
	Actions      int `json:"actions"`
	Observations int `json:"observations"`
	Results      int `json:"results"`
}

// AcceptanceCriteria 是 e2e 剧本的验收门槛：探索图各类节点数的下限（验证完整认知循环）。
type AcceptanceCriteria struct {
	MinObjectives int // 至少产生多少个目标
	MinActions    int // 至少执行多少个动作
	MinResults    int // 至少验证多少个结果
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
