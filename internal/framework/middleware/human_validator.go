package middleware

import (
	"context"
	"fmt"
)

// DefaultHumanInputValidator 是默认的输入验证器。
type DefaultHumanInputValidator struct{}

// NewDefaultValidator 创建默认验证器。
func NewDefaultValidator() *DefaultHumanInputValidator {
	return &DefaultHumanInputValidator{}
}

// Validate 验证人工输入。
func (v *DefaultHumanInputValidator) Validate(ctx context.Context, req HumanInputRequest, resp HumanInputResponse) error {
	// 验证 RequestID 匹配
	if resp.RequestID != req.ID {
		return fmt.Errorf("request_id mismatch: expected %s, got %s", req.ID, resp.RequestID)
	}

	// 验证 TaskID 匹配
	if resp.TaskID != req.TaskID {
		return fmt.Errorf("task_id mismatch: expected %s, got %s", req.TaskID, resp.TaskID)
	}

	// 验证 NodeID 匹配
	if resp.NodeID != req.NodeID {
		return fmt.Errorf("node_id mismatch: expected %s, got %s", req.NodeID, resp.NodeID)
	}

	// 根据输入类型验证
	switch req.InputType {
	case "text":
		return v.validateText(req, resp)
	case "choice":
		return v.validateChoice(req, resp)
	case "approval":
		return v.validateApproval(req, resp)
	case "file":
		return v.validateFile(req, resp)
	default:
		return fmt.Errorf("unknown input type: %s", req.InputType)
	}
}

// validateText 验证文本输入。
func (v *DefaultHumanInputValidator) validateText(req HumanInputRequest, resp HumanInputResponse) error {
	if resp.Value == "" {
		return fmt.Errorf("text value is required")
	}
	return nil
}

// validateChoice 验证选择输入。
func (v *DefaultHumanInputValidator) validateChoice(req HumanInputRequest, resp HumanInputResponse) error {
	if resp.Value == "" {
		return fmt.Errorf("choice value is required")
	}

	// 验证选项是否在列表中
	if len(req.Choices) > 0 {
		valid := false
		for _, choice := range req.Choices {
			if resp.Value == choice {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("invalid choice: %s, must be one of %v", resp.Value, req.Choices)
		}
	}

	return nil
}

// validateApproval 验证审批输入。
func (v *DefaultHumanInputValidator) validateApproval(req HumanInputRequest, resp HumanInputResponse) error {
	// 审批类型必须有 Approved 字段
	if resp.Value == "" {
		return fmt.Errorf("approval value is required")
	}

	// 验证值为 "批准" 或 "拒绝"
	if resp.Value != "批准" && resp.Value != "拒绝" && resp.Value != "approve" && resp.Value != "reject" {
		return fmt.Errorf("invalid approval value: %s, must be '批准'/'拒绝' or 'approve'/'reject'", resp.Value)
	}

	return nil
}

// validateFile 验证文件输入。
func (v *DefaultHumanInputValidator) validateFile(req HumanInputRequest, resp HumanInputResponse) error {
	if resp.Value == "" {
		return fmt.Errorf("file path is required")
	}

	// 可以添加更多验证：文件大小、类型等
	return nil
}
