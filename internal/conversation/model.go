// Package conversation 实现会话式平台的 conversation + message model + store。
//
// 业务定位：用户在前端与 liusha 会话发起扫描，agent 过程事件流式展示并落库可回看
// 。
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

// 会话消息角色。
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

// Conversation 是 conversation 表行的 Go 表示。json tag 是 API 契约：
// 列表端点直出本结构，键统一小写蛇形（与 usage 等其它 DTO 同口径）。
type Conversation struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`   // 可空——首条消息摘要，UI 列表用
	TaskID    string    `json:"task_id"` // 可空——关联本会话所属的 task
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// RunStatus 是派生的「真实运行态」（关联 task 的 status：active/completed/aborted；
	// 纯聊天为空）。仅 ListConversations 填充。
	// （注：conversation 表自身的 status 列是僵尸字段，已不读入；运行态一律派生自关联 task。）
	RunStatus string `json:"run_status,omitempty"`
	// FindingCount 是本会话关联 task 已挖到的漏洞数（派生自 finding 表）。仅 ListConversations 填充，
	// 供前端列表/流量分析 feed 卡展示「host · N findings」摘要。纯聊天/无 task = 0。
	FindingCount int `json:"finding_count,omitempty"`
	// Source 是派生的下发来源（关联 assignment 的 source：manual 人工下发 / auto 聚合器攒批；
	// 纯聊天/无 task 归 manual）。仅 ListConversations 填充，供前端「主动下发 / 被动代理」双 tab 分流。
	Source string `json:"source,omitempty"`
}

// Message 是 message 表行的 Go 表示。json tag 是 API 契约：messages 端点、SSE 补历史
// 与 redis 实时帧都序列化本结构，键统一小写蛇形（前端 store 直接消费）。
type Message struct {
	Seq            int64           `json:"seq"` // 全局自增，会话内稳定顺序键（SSE 高频事件不依赖时间戳精度）
	ID             string          `json:"id"`
	ConversationID string          `json:"conversation_id"`
	Role           Role            `json:"role"`
	Kind           Kind            `json:"kind"`
	Content        string          `json:"content"`
	Metadata       json.RawMessage `json:"metadata"` // 可空——event 的结构化详情（tool name/args、finding id…）
	CreatedAt      time.Time       `json:"created_at"`
}

// TaskStatus 是任务生命周期节点在时间线上的状态值（metadata.status）。
// 生命周期事实源是 task.status（active/completed/aborted）；时间线节点是
// 各迁移写者随迁移落的可见事件（dispatched 是下发时刻的记录，无对应终态列）。
const (
	TaskStatusDispatched = "dispatched" // 已下发扫描（api 入队成功后）
	TaskStatusCompleted  = "completed"  // 认知循环自然收敛（runner）
	TaskStatusAborted    = "aborted"    // 用户停止 / 判死（api / reaper）
	TaskStatusFailed     = "failed"     // 执行失败（runner failTask）
)

// TaskStatusMeta 构造时间线任务状态节点的 metadata（单一契约源，五处迁移写者共用）：
//
//	{kind:"task_status", status:..., 其余字段按迁移可选（stop_reason/steps/findings/
//	 duration_ms/reason——completed 携报告、aborted/failed 携原因）}
//
// 负值哨兵（调用方读依赖失败时的 -1 缺省）与零值一并省略——wire 上不出现
// "无意义字段"。前端按 kind=task_status 渲染系统状态卡。
func TaskStatusMeta(status string, fields map[string]any) json.RawMessage {
	m := map[string]any{"kind": "task_status", "status": status}
	for k, v := range fields {
		switch t := v.(type) {
		case string:
			if t != "" {
				m[k] = t
			}
		case int:
			if t > 0 {
				m[k] = t
			}
		case int64:
			if t > 0 {
				m[k] = t
			}
		default:
			if v != nil {
				m[k] = v
			}
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(`{"kind":"task_status","status":"` + status + `"}`)
	}
	return b
}
