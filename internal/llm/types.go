// Package llm 是业务侧 LLM 策略层（role 路由 / fallback 编排 / 审计 instrumentation）。
//
// 类型事实源在 framework/llm（框架层自包含）；本包全部类型是框架类型的别名，
// 仅为业务侧调用方保留稳定的包内引用。
package llm

import (
	"github.com/V3teran/liusha/internal/framework/llm"
)

// ClientPool/Role/Message 等均为 framework/llm 事实源的别名。
//
//nolint:revive // 别名块共享一条说明，为每个别名重复 doc 无信息量。
type (
	ClientPool   = llm.ClientPool
	Role         = llm.Role
	Message      = llm.Message
	ContentPart  = llm.ContentPart
	ImageContent = llm.ImageContent
	ToolCall     = llm.ToolCall
	ToolSchema   = llm.ToolSchema
	Usage        = llm.Usage
	Result       = llm.Result
	Generator    = llm.Generator
	HTTPError    = llm.HTTPError
)

// RoleSystem 等枚举定义。
const (
	RoleSystem    = llm.RoleSystem
	RoleUser      = llm.RoleUser
	RoleAssistant = llm.RoleAssistant
	RoleTool      = llm.RoleTool
)

// ErrVisionUnsupported 当 caller 传入含 image_url 的 ContentParts，但 provider 不支持视觉时返。
var ErrVisionUnsupported = llm.ErrVisionUnsupported

// NewClientPool 构造共享 HTTP client 池（构造在框架层，业务层组合使用）。
func NewClientPool() *ClientPool { return llm.NewClientPool() }
