package middleware

import (
	"context"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
)

// HumanInputRequest 是人工输入请求。
type HumanInputRequest struct {
	// 请求 ID
	ID string `json:"id"`

	// 任务 ID
	TaskID string `json:"task_id"`

	// 关联的节点 ID
	NodeID string `json:"node_id"`

	// 提示文本（给人看的）
	Prompt string `json:"prompt"`

	// 输入类型：text, choice, approval, file
	InputType string `json:"input_type"`

	// 选项（当 InputType 为 choice 时）
	Choices []string `json:"choices,omitempty"`

	// 默认值
	DefaultValue string `json:"default_value,omitempty"`

	// 超时时间（秒，0 表示无限制）
	TimeoutSec int `json:"timeout_sec,omitempty"`

	// 创建时间
	CreatedAt time.Time `json:"created_at"`

	// 元数据
	Metadata map[string]any `json:"metadata,omitempty"`
}

// HumanInputResponse 是人工输入结果。
type HumanInputResponse struct {
	// 请求 ID
	RequestID string `json:"request_id"`

	// 任务 ID
	TaskID string `json:"task_id"`

	// 节点 ID
	NodeID string `json:"node_id"`

	// 输入值
	Value string `json:"value"`

	// 是否批准（仅 approval 类型）
	Approved bool `json:"approved"`

	// 提交时间
	SubmittedAt time.Time `json:"submitted_at"`

	// 提交者（用户 ID 或系统标识）
	Submitter string `json:"submitter,omitempty"`
}

// HumanInteractionManager 管理人机交互。
type HumanInteractionManager interface {
	// RequestInput 请求人工输入（阻塞直到收到输入或超时）
	RequestInput(ctx context.Context, req HumanInputRequest) (*HumanInputResponse, error)

	// SubmitInput 提交人工输入
	SubmitInput(ctx context.Context, resp HumanInputResponse) error

	// ListPending 列出待处理的请求
	ListPending(ctx context.Context, taskID string) ([]HumanInputRequest, error)

	// Cancel 取消请求
	Cancel(ctx context.Context, requestID string) error

	// Subscribe 订阅新请求（用于前端推送）
	Subscribe(ctx context.Context, taskID string) (<-chan HumanInputRequest, error)
}

// HumanInputValidator 验证人工输入。
type HumanInputValidator interface {
	// Validate 验证输入是否合法
	Validate(ctx context.Context, req HumanInputRequest, resp HumanInputResponse) error
}

// HumanInputStore 持久化人工输入。
type HumanInputStore interface {
	// SaveRequest 保存请求
	SaveRequest(ctx context.Context, req HumanInputRequest) error

	// SaveResponse 保存响应
	SaveResponse(ctx context.Context, resp HumanInputResponse) error

	// GetRequest 获取请求
	GetRequest(ctx context.Context, requestID string) (*HumanInputRequest, error)

	// GetResponse 获取响应
	GetResponse(ctx context.Context, requestID string) (*HumanInputResponse, error)

	// ListRequests 列出请求（分页）
	ListRequests(ctx context.Context, filter HumanInputFilter) ([]HumanInputRequest, error)
}

// HumanInputFilter 是请求过滤器。
type HumanInputFilter struct {
	// 按任务 ID 过滤
	TaskID string

	// 按状态过滤：pending, completed, timeout, canceled
	Status string

	// 时间范围
	StartTime time.Time
	EndTime   time.Time

	// 分页
	Limit  int
	Offset int
}

// HumanApprovalPolicy 是审批策略。
type HumanApprovalPolicy interface {
	// RequiresApproval 判断节点是否需要人工审批
	RequiresApproval(ctx context.Context, node core.Node) bool

	// Approvers 获取审批人列表
	Approvers(ctx context.Context, node core.Node) ([]string, error)
}

// AutoApprovalRule 是自动审批规则。
type AutoApprovalRule interface {
	// ShouldAutoApprove 判断是否可以自动审批
	ShouldAutoApprove(ctx context.Context, req HumanInputRequest) bool

	// AutoApprove 执行自动审批
	AutoApprove(ctx context.Context, req HumanInputRequest) (*HumanInputResponse, error)
}

// HumanInputNotifier 通知人工输入请求。
type HumanInputNotifier interface {
	// Notify 发送通知（邮件、短信、Slack 等）
	Notify(ctx context.Context, req HumanInputRequest, approvers []string) error
}

// NewHumanInputRequest 创建人工输入请求（便捷函数）。
func NewHumanInputRequest(taskID, nodeID, prompt, inputType string) HumanInputRequest {
	return HumanInputRequest{
		ID:         core.NewEvent(core.EventHumanInputRequired, taskID, "human", nil).ID,
		TaskID:     taskID,
		NodeID:     nodeID,
		Prompt:     prompt,
		InputType:  inputType,
		CreatedAt:  time.Now(),
		TimeoutSec: 3600, // 默认 1 小时
	}
}

// NewApprovalRequest 创建审批请求（便捷函数）。
func NewApprovalRequest(taskID, nodeID, prompt string) HumanInputRequest {
	req := NewHumanInputRequest(taskID, nodeID, prompt, "approval")
	req.Choices = []string{"批准", "拒绝"}
	return req
}
