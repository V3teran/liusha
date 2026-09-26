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
func (p *DefaultHumanApprovalPolicy) RequiresApproval(ctx context.Context, node *core.GraphNode) bool {
	// 检查节点类型
	if p.requiredTypes[node.Kind] {
		return true
	}

	// 检查节点元数据
	if node.Metadata != nil {
		if requiresApproval, ok := node.Metadata["requires_approval"]; ok {
			if str, ok := requiresApproval.(string); ok {
				return str == "true"
			}
		}
	}

	return false
}

// Approvers 获取审批人列表。
func (p *DefaultHumanApprovalPolicy) Approvers(ctx context.Context, node *core.GraphNode) ([]string, error) {
	// 优先使用节点指定的审批人
	if node.Metadata != nil {
		if approvers, ok := node.Metadata["approvers"]; ok {
			if str, ok := approvers.(string); ok {
				return []string{str}, nil
			}
		}
	}

	// 使用默认审批人
	return p.defaultApprovers, nil
}

// MetadataApprovalPolicy 基于节点元数据的审批策略：
// metadata 中 requires_approval == "true" 时需要人工审批。
type MetadataApprovalPolicy struct {
	// 默认审批人
	approvers []string
}

// NewMetadataApprovalPolicy 创建基于元数据的审批策略。
func NewMetadataApprovalPolicy(approvers []string) *MetadataApprovalPolicy {
	return &MetadataApprovalPolicy{approvers: approvers}
}

// RequiresApproval 判断是否需要审批。
func (p *MetadataApprovalPolicy) RequiresApproval(ctx context.Context, node *core.GraphNode) bool {
	if node.Metadata != nil {
		if requiresApproval, ok := node.Metadata["requires_approval"]; ok {
			if str, ok := requiresApproval.(string); ok {
				return str == "true"
			}
		}
	}

	return false
}

// Approvers 获取审批人列表。
func (p *MetadataApprovalPolicy) Approvers(ctx context.Context, node *core.GraphNode) ([]string, error) {
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
