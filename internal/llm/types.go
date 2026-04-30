// Package llm 定义 LLM Generator 抽象 + 各 provider 适配。
//
// 设计要点：
//   - Generator 接口屏蔽 provider 差异（DeepSeek/Claude/OpenAI...）。
//   - Generator 不安全跨 goroutine 并发使用（eino BindTools 改变内部状态）；
//     调用方负责每个任务建独立实例。
//   - tools 在 New 时一次性绑定，Generate 不再传 tools（保留参数仅为接口对称）。
package llm

import (
	"context"
	"encoding/json"
)

// Role 是消息角色。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message 是一次对话中的一条消息。
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall 是模型请求调用工具的结构。Arguments 为 JSON 串（OpenAI 风格）。
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolSchema 描述一个工具的元信息（名字 + 描述 + JSON Schema 参数）。
type ToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Usage 是一次调用的 token 用量。
type Usage struct {
	InTokens     int
	OutTokens    int
	CachedTokens int
}

// Add 返回两个 Usage 累加后的新值（值方法，不修改 receiver）。
func (u Usage) Add(o Usage) Usage {
	return Usage{
		InTokens:     u.InTokens + o.InTokens,
		OutTokens:    u.OutTokens + o.OutTokens,
		CachedTokens: u.CachedTokens + o.CachedTokens,
	}
}

// Result 是 Generate 的返回结构。
type Result struct {
	Content      string
	ToolCalls    []ToolCall
	Usage        Usage
	FinishReason string
	Provider     string
	Model        string
}

// Generator 是 LLM 生成器统一接口。
type Generator interface {
	// Generate 发起一次对话生成。tools 参数当前未使用（在 New 时一次绑定），
	// 保留是为了后续支持动态绑定或 mock。
	Generate(ctx context.Context, messages []Message, tools []ToolSchema) (Result, error)
	Provider() string
	Model() string
}
