package middleware

import (
	"context"

	"github.com/V3teran/liusha/internal/framework/core"
)

// DefaultHumanApprovalPolicy 是默认的审批策略。
type DefaultHumanApprovalPolicy struct {
	// 需要审批的节点类型
	requiredTypes map[string]bool

	// 默认审批人列表
	defaultApprovers []string
}

// NewDefaultApprovalPolicy 创建默认审批策略。
func NewDefaultApprovalPolicy(requiredTypes []string, defaultApprovers []string) *DefaultHumanApprovalPolicy {
	typeMap := make(map[string]bool)
	for _, t := range requiredTypes {
		typeMap[t] = true
	}

	return &DefaultHumanApprovalPolicy{
		requiredTypes:    typeMap,
		defaultApprovers: defaultApprovers,
	}
}

// RequiresApproval 判断节点是否需要人工审批。
func (p *DefaultHumanApprovalPolicy) RequiresApproval(ctx context.Context, node core.Node) bool {
	// 检查节点类型
	if p.requiredTypes[node.Type] {
		return true
	}

	// 检查节点标签
	if node.Metadata.Labels != nil {
		if requiresApproval, ok := node.Metadata.Labels["requires_approval"]; ok {
			return requiresApproval == "true"
		}
	}

	return false
}

// Approvers 获取审批人列表。
func (p *DefaultHumanApprovalPolicy) Approvers(ctx context.Context, node core.Node) ([]string, error) {
	// 优先使用节点指定的审批人
	if node.Metadata.Labels != nil {
		if approvers, ok := node.Metadata.Labels["approvers"]; ok {
			// 假设格式为 "user1,user2,user3"
			return []string{approvers}, nil
		}
	}

	// 使用默认审批人
	return p.defaultApprovers, nil
}

// TimeBasedApprovalPolicy 基于时间的审批策略（如工作时间外需要审批）。
type TimeBasedApprovalPolicy struct {
	// 工作时间（小时，0-23）
	workHourStart int
	workHourEnd   int

	// 默认审批人
	approvers []string
}

// NewTimeBasedApprovalPolicy 创建基于时间的审批策略。
func NewTimeBasedApprovalPolicy(workHourStart, workHourEnd int, approvers []string) *TimeBasedApprovalPolicy {
	return &TimeBasedApprovalPolicy{
		workHourStart: workHourStart,
		workHourEnd:   workHourEnd,
		approvers:     approvers,
	}
}

// RequiresApproval 判断是否需要审批（工作时间外需要）。
func (p *TimeBasedApprovalPolicy) RequiresApproval(ctx context.Context, node core.Node) bool {
	// 获取当前时间
	// hour := time.Now().Hour()

	// 工作时间外需要审批
	// if hour < p.workHourStart || hour >= p.workHourEnd {
	// 	return true
	// }

	// 简化：始终检查节点标签
	if node.Metadata.Labels != nil {
		if requiresApproval, ok := node.Metadata.Labels["requires_approval"]; ok {
			return requiresApproval == "true"
		}
	}

	return false
}

// Approvers 获取审批人列表。
func (p *TimeBasedApprovalPolicy) Approvers(ctx context.Context, node core.Node) ([]string, error) {
	return p.approvers, nil
}

// AlwaysApproveRule 始终自动审批（测试用）。
type AlwaysApproveRule struct{}

// ShouldAutoApprove 判断是否自动审批。
func (r *AlwaysApproveRule) ShouldAutoApprove(ctx context.Context, req HumanInputRequest) bool {
	return true
}

// AutoApprove 执行自动审批。
func (r *AlwaysApproveRule) AutoApprove(ctx context.Context, req HumanInputRequest) (*HumanInputResponse, error) {
	return &HumanInputResponse{
		RequestID: req.ID,
		TaskID:    req.TaskID,
		NodeID:    req.NodeID,
		Value:     "批准",
		Approved:  true,
		Submitter: "system(auto-approve)",
	}, nil
}

// ConditionalApprovalRule 条件审批规则。
type ConditionalApprovalRule struct {
	// 自动审批的条件函数
	condition func(HumanInputRequest) bool
}

// NewConditionalApprovalRule 创建条件审批规则。
func NewConditionalApprovalRule(condition func(HumanInputRequest) bool) *ConditionalApprovalRule {
	return &ConditionalApprovalRule{
		condition: condition,
	}
}

// ShouldAutoApprove 判断是否自动审批。
func (r *ConditionalApprovalRule) ShouldAutoApprove(ctx context.Context, req HumanInputRequest) bool {
	if r.condition == nil {
		return false
	}
	return r.condition(req)
}

// AutoApprove 执行自动审批。
func (r *ConditionalApprovalRule) AutoApprove(ctx context.Context, req HumanInputRequest) (*HumanInputResponse, error) {
	return &HumanInputResponse{
		RequestID: req.ID,
		TaskID:    req.TaskID,
		NodeID:    req.NodeID,
		Value:     "批准",
		Approved:  true,
		Submitter: "system(auto-approve)",
	}, nil
}
