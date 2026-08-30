package lead

import "time"

// Category 是情报的信息分类
type Category string

const (
	// 信息类
	CategoryTarget        Category = "target"        // 目标信息
	CategoryCredential    Category = "credential"    // 凭证信息
	CategoryInfrastructure Category = "infrastructure" // 基础设施
	CategoryBusiness      Category = "business"      // 业务逻辑
	CategoryData          Category = "data"          // 数据特征

	// 发现类
	CategoryFinding       Category = "finding"       // 发现（漏洞/问题）

	// 其他
	CategoryObstacle      Category = "obstacle"      // 障碍
	CategoryNote          Category = "note"          // 笔记
)

// Priority 是优先级
type Priority string

const (
	PriorityCritical Priority = "critical" // 关键（P0）
	PriorityHigh     Priority = "high"     // 高（P1）
	PriorityMedium   Priority = "medium"   // 中（P2）
	PriorityLow      Priority = "low"      // 低（P3）
)

// Confidence 是置信度
type Confidence string

const (
	ConfidenceConfirmed Confidence = "confirmed" // 已确认
	ConfidenceProbable  Confidence = "probable"  // 很可能
	ConfidencePossible  Confidence = "possible"  // 可能
)

// Entry 是一条情报记录
type Entry struct {
	ID           string     `json:"id"`
	AssignmentID string     `json:"assignment_id"`

	// 三维分类
	Category     Category   `json:"category"`
	Priority     Priority   `json:"priority"`
	Confidence   Confidence `json:"confidence"`

	// 内容
	Summary      string     `json:"summary"`
	Body         string     `json:"body,omitempty"`
	Tags         []string   `json:"tags,omitempty"`

	// 追溯
	SourceTaskID  string    `json:"source_task_id"`
	SourceAgentID string    `json:"source_agent_id,omitempty"`

	// 时间
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// IsCritical 判断是否为关键优先级
func (e *Entry) IsCritical() bool {
	return e.Priority == PriorityCritical
}

// IsHighPriority 判断是否为高优先级或关键
func (e *Entry) IsHighPriority() bool {
	return e.Priority == PriorityCritical || e.Priority == PriorityHigh
}

// IsConfirmed 判断是否已确认
func (e *Entry) IsConfirmed() bool {
	return e.Confidence == ConfidenceConfirmed
}
