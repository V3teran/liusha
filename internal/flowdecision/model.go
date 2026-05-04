// Package flowdecision 实现 flow_decision 表的 model 与 store。
//
// flow_decision 行落库 classify_traffic 的 LLM 输出，把"流量语义判断"
// （operation / resource_scope / attack_surfaces / carries_auth /
// credential_locations / required_skills / reasoning）显式持久化，
// 让归档后的扫描会话仍可审计、复现和按维度统计。
package flowdecision

import (
	"encoding/json"
	"time"
)

// ResourceScope 是 flow_decision.resource_scope 的文本枚举（与 SQL CHECK 一致）。
type ResourceScope string

const (
	ResourceScopePrivate ResourceScope = "private"
	ResourceScopePublic  ResourceScope = "public"
	ResourceScopeUnknown ResourceScope = "unknown"
)

// Decision 是 flow_decision 表行的 Go 表示。
//
// TaskID 可空（主 ReAct 第 1 步调 classify_traffic 时还没注册 sub-task）。
// jsonb 字段以 json.RawMessage 透传，由调用方 marshal 成稳定 JSON。
type Decision struct {
	ID                  int64
	EngagementID        string
	FlowID              int64
	TaskID              *string
	Operation           string
	ResourceScope       ResourceScope
	AttackSurfaces      json.RawMessage
	CarriesAuth         bool
	CredentialLocations json.RawMessage
	RequiredSkills      json.RawMessage
	Reasoning           string
	CreatedAt           time.Time
}
