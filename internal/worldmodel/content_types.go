package worldmodel

import "time"

// ObjectiveContent 是 Objective 节点的内容结构。
type ObjectiveContent struct {
	Description string   `json:"description"` // 任务描述
	Target      string   `json:"target"`      // 目标系统/主机
	Scope       []string `json:"scope"`       // 范围限制
	Constraints []string `json:"constraints"` // 约束条件
}

// ActionContent 是 Action 节点的内容结构。
type ActionContent struct {
	Instruction string `json:"instruction"` // 指令描述
	Reasoning   string `json:"reasoning"`   // 为什么需要这个 action
}

// HypothesisContent 是 Hypothesis 节点的内容结构。
type HypothesisContent struct {
	Statement string `json:"statement"` // 假设陈述
	Evidence  string `json:"evidence"`  // 支撑证据
}

// FindingContent 是 Finding 节点的内容结构。
type FindingContent struct {
	Title       string    `json:"title"`       // 发现标题
	Description string    `json:"description"` // 详细描述
	Severity    string    `json:"severity"`    // 严重程度
	Evidence    []string  `json:"evidence"`    // 证据列表
	DetectedAt  time.Time `json:"detected_at"` // 发现时间
}

// LeadContent 是 Lead 节点的内容结构。
type LeadContent struct {
	Description string `json:"description"` // 线索描述
	Source      string `json:"source"`      // 来源
}
