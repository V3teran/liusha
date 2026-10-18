// Package insight 提供运行期洞察（Insight）的领域模型与持久化：
// agent 执行过程中沉淀的经验记录，按 assignment 归档、按优先级/类别检索。
package insight

import (
	"time"

	"github.com/V3teran/liusha/internal/explorationgraph"
)

// Category 是洞察的信息分类（领域中立，无冗余）
type Category string

const (
	// CategoryEntity 实体信息
	// - 探索对象及其内容：主机、服务、端点、数据、配置
	// - 合并原 target（目标）+ data（数据）
	CategoryEntity Category = "entity"

	// CategoryAccess 访问方式
	// - 如何访问实体：凭证、认证、授权、入口
	// - 原 credential 改名
	CategoryAccess Category = "access"

	// CategoryStructure 结构信息
	// - 系统的组织方式：架构、拓扑、层次、依赖
	// - 原 infrastructure 改名
	CategoryStructure Category = "structure"

	// CategoryBehavior 行为信息
	// - 系统的运作方式：逻辑、流程、机制、交互
	// - 原 business 改名
	CategoryBehavior Category = "behavior"

	// CategoryIssue 问题与障碍
	// - 异常、问题、障碍、限制
	// - 合并原 anomaly + obstacle
	CategoryIssue Category = "issue"

	// CategoryNote 备注笔记
	// - 无法归类的其他信息、备注、观察
	CategoryNote Category = "note"
)

// Priority 类型使用 explorationgraph.Priority（统一定义）
type Priority = explorationgraph.Priority

// Priority 常量（重新导出以保持兼容性）
const (
	PriorityCritical = explorationgraph.PriorityCritical
	PriorityHigh     = explorationgraph.PriorityHigh
	PriorityMedium   = explorationgraph.PriorityMedium
	PriorityLow      = explorationgraph.PriorityLow
)

// Confidence 是置信度
type Confidence string

// ConfidenceProbable 等枚举定义。
const (
	// ConfidenceConfirmed 已确认；ConfidenceProbable 很可能；ConfidencePossible 可能。
	ConfidenceConfirmed Confidence = "confirmed"
	ConfidenceProbable  Confidence = "probable" // ConfidenceProbable 很可能
	ConfidencePossible  Confidence = "possible" // 可能
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

	// 严重性（仅 category=issue 时有意义）
	// - 区别于 priority（优先级）：severity 是技术评估，priority 是业务决策
	// - 例如：低严重性 + 高暴露 = 高优先级
	Severity *string `json:"severity,omitempty"` // low/medium/high/critical

	// 内容
	Summary string   `json:"summary"`
	Body    string   `json:"body,omitempty"`
	Tags    []string `json:"tags,omitempty"`

	// 追溯
	SourceTaskID     string `json:"source_task_id"`
	SourceAgentRunID string `json:"source_agent_run_id,omitempty"` // 产出该情报的认知轮次 agent_run.id

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
