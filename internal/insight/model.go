package insight

import (
	"time"

	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// Category 是洞察的信息分类
type Category string

const (
	// 信息类
	CategoryTarget         Category = "target"         // 目标信息
	CategoryCredential     Category = "credential"     // 凭证信息
	CategoryInfrastructure Category = "infrastructure" // 基础设施
	CategoryBusiness       Category = "business"       // 业务逻辑
	CategoryData           Category = "data"           // 数据特征

	// 发现类
	CategoryResult Category = "result" // 结果（最终确认）

	// 其他
	CategoryObstacle Category = "obstacle" // 障碍
	CategoryNote     Category = "note"     // 笔记
)

// Priority 类型使用 knowledgegraph.Priority（统一定义）
type Priority = knowledgegraph.Priority

// Priority 常量（重新导出以保持兼容性）
const (
	PriorityCritical = knowledgegraph.PriorityCritical
	PriorityHigh     = knowledgegraph.PriorityHigh
	PriorityMedium   = knowledgegraph.PriorityMedium
	PriorityLow      = knowledgegraph.PriorityLow
)

// Confidence 是置信度
type Confidence string

const (
	ConfidenceConfirmed Confidence = "confirmed" // 已确认
	ConfidenceProbable  Confidence = "probable"  // 很可能
	ConfidencePossible  Confidence = "possible"  // 可能
)

// Insight 是一条洞察记录
//
// 设计理念：
// - Insight 是 Agent 执行过程中的发现和洞察
// - 作为黑板系统，供多个 Agent 读写和共享
// - 带有分类、优先级、置信度等元数据
// - 支持溯源（谁写的、什么时候写的）
type Insight struct {
	ID           string `json:"id"`
	AssignmentID string `json:"assignment_id"`

	// 三维分类
	Category   Category   `json:"category"`
	Priority   Priority   `json:"priority"`
	Confidence Confidence `json:"confidence"`

	// 内容
	Summary string   `json:"summary"`
	Body    string   `json:"body,omitempty"`
	Tags    []string `json:"tags,omitempty"`

	// 追溯
	SourceTaskID  string `json:"source_task_id"`
	SourceAgentID string `json:"source_agent_id,omitempty"`

	// 时间
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// IsCritical 判断是否为关键优先级
func (i *Insight) IsCritical() bool {
	return i.Priority == PriorityCritical
}

// IsHighPriority 判断是否为高优先级或关键
func (i *Insight) IsHighPriority() bool {
	return i.Priority == PriorityCritical || i.Priority == PriorityHigh
}

// IsConfirmed 判断是否已确认
func (i *Insight) IsConfirmed() bool {
	return i.Confidence == ConfidenceConfirmed
}
