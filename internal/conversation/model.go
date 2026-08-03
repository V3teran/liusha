// Package conversation 实现会话式平台（阶段B）的 conversation + message model + store。
//
// 业务定位：用户在前端与 liusha 会话发起扫描，agent 过程事件流式展示并落库可回看
// （见 docs/superpowers/specs/2026-06-07-conversational-platform.md §B + 记忆 project_phaseb_sse_arch）。
//
//   - Conversation：一次会话。TaskID 关联本会话所属的 task（纯聊天时空）。
//   - Message：会话内的消息。Kind 区分普通会话消息与 agent 过程事件（UI 渲染不同）。
//
// 事件流式传输（runner→Redis→api）在 B2/B3 实现；本包只管会话/消息的持久化。
package conversation

import (
	"encoding/json"
	"time"
)

// Role 是消息的发言角色（对齐 LLM 消息角色）。
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	RoleTool      Role = "tool"
)

// Kind 区分普通会话消息与 agent 过程事件。
type Kind string

const (
	// KindMessage 是普通会话消息（user 提问 / assistant 回复 / system 提示）。
	KindMessage Kind = "message"
	// KindEvent 是 agent 跑扫描时的过程事件（tool 调用 / 阶段切换 / finding 产出…），
	// 结构化细节进 Metadata。
	KindEvent Kind = "event"
)

// Conversation 是 conversation 表行的 Go 表示。
type Conversation struct {
	ID        string
	Title     string // 可空——首条消息摘要，UI 列表用
	TaskID    string // 可空——关联本会话所属的 task
	CreatedAt time.Time
	UpdatedAt time.Time
	// RunStatus 是派生的「真实运行态」（关联 task 的 status：active/completed/aborted；
	// 纯聊天为空）。仅 ListConversations 填充。
	// （注：conversation 表自身的 status 列是僵尸字段，已不读入；运行态一律派生自关联 task。）
	RunStatus string `json:"RunStatus,omitempty"`
	// ScenarioID 是派生的场景 code（关联 task 的 scenario_id；纯聊天空）。仅 ListConversations 填充，
	// 供前端按场景分流列表。
	ScenarioID string `json:"ScenarioID,omitempty"`
	// FindingCount 是本会话关联 task 已挖到的漏洞数（派生自 finding 表）。仅 ListConversations 填充，
	// 供前端列表/流量分析 feed 卡展示「host · N findings」摘要。纯聊天/无 task = 0。
	FindingCount int `json:"FindingCount,omitempty"`
	// Source 是派生的下发来源（关联 assignment 的 source：manual 人工下发 / auto 聚合器攒批；
	// 纯聊天/无 task 归 manual）。仅 ListConversations 填充，供前端「主动下发 / 被动代理」双 tab 分流。
	// 与 scenario/playbook/engine 正交——仅审计/来源维度，不参与场景解析。
	Source string `json:"Source,omitempty"`
}

// Message 是 message 表行的 Go 表示。
type Message struct {
	Seq            int64 // 全局自增，会话内稳定顺序键（SSE 高频事件不依赖时间戳精度）
	ID             string
	ConversationID string
	Role           Role
	Kind           Kind
	Content        string
	Metadata       json.RawMessage // 可空——event 的结构化详情（tool name/args、finding id…）
	CreatedAt      time.Time
}
