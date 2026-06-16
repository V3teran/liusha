// Package conversation 实现对话式平台（阶段B）的 conversation + message model + store。
//
// 业务定位：用户在前端与 liusha 对话发起扫描，agent 过程事件流式展示并落库可回看
// （见 docs/superpowers/specs/2026-06-07-conversational-platform.md §B + 记忆 project_phaseb_sse_arch）。
//
//   - Conversation：一次对话会话。ScanID 关联本对话发起的 active_scan（纯聊天/passive 时空）。
//     RoleID 是场景 role（阶段C 用，先留字段不接线）。
//   - Message：对话内的消息。Kind 区分普通对话消息与 agent 过程事件（UI 渲染不同）。
//
// 事件流式传输（scanner→Redis→api）在 B2/B3 实现；本包只管对话/消息的持久化。
package conversation

import (
	"encoding/json"
	"time"
)

// Status 是对话会话的生命周期状态。
type Status string

const (
	StatusActive   Status = "active"
	StatusArchived Status = "archived"
)

// Role 是消息的发言角色（对齐 LLM 消息角色）。
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
)

// Kind 区分普通对话消息与 agent 过程事件。
type Kind string

const (
	// KindMessage 是普通对话消息（user 提问 / assistant 回复 / system 提示）。
	KindMessage Kind = "message"
	// KindEvent 是 agent 跑扫描时的过程事件（tool 调用 / 阶段切换 / finding 产出…），
	// 结构化细节进 Metadata。
	KindEvent Kind = "event"
)

// Conversation 是 conversation 表行的 Go 表示。
type Conversation struct {
	ID        string
	Title     string // 可空——首条消息摘要，UI 列表用
	ScanID    string // 可空——关联本对话发起的 active_scan
	RoleID    string // 可空——场景 role（阶段C）
	Status    Status
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Message 是 message 表行的 Go 表示。
type Message struct {
	Seq            int64 // 全局自增，对话内稳定顺序键（SSE 高频事件不依赖时间戳精度）
	ID             string
	ConversationID string
	Role           Role
	Kind           Kind
	Content        string
	Metadata       json.RawMessage // 可空——event 的结构化详情（tool name/args、finding id…）
	CreatedAt      time.Time
}
