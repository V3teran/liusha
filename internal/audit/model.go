// Package audit 实现 audit_log 表的 model + store。
//
// 业务定位：系统级敏感操作的审计日志——谁在何时建/中止 owner、删凭证。
// 通用 actor / action / target 三元结构覆盖：
//   - 安全合规需求（审计追踪）
//   - 误操作复盘根因（凭证被删 / owner 被意外 abort）
//
// 写入策略：业务侧明确感知"这是审计事件"才显式调 Append——
// 不靠 trigger 全自动，避免隐式行为难维护。
package audit

import (
	"encoding/json"
	"time"
)

// Event 是 audit_log 表行的 Go 表示。
//
// Actor 示例: 'system' / 'sweep' / 'api_user:abcd1234' / 'scanner'
// Action 示例: 'owner.abort' / 'owner.create' / 'credential.set' / 'credential.delete'
// TargetKind 示例: 'passive_session' / 'active_scan' / 'credential'
// TargetID 是 target 主键（uuid 字符串 / host key 等）
type Event struct {
	ID         int64
	Actor      string
	Action     string
	TargetKind string
	TargetID   string
	Metadata   json.RawMessage // 任意上下文 jsonb；为空时存 '{}'
	CreatedAt  time.Time
}

// 常量化常用 actor / action 字符串，避免散落字面量拼写错。
const (
	ActorSystem  = "system"
	ActorSweep   = "sweep" // passive_session.Sweep TTL 过期清理
	ActorAPIUser = "api_user"
	ActorScanner = "scanner"

	ActionOwnerAbort       = "owner.abort"
	ActionOwnerCreate      = "owner.create"
	ActionCredentialSet    = "credential.set"
	ActionCredentialDelete = "credential.delete"
)
