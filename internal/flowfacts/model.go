// Package flowdecision 实现 flow_facts 表的 model 与 store。
//
// flow_facts 行落库 classify_traffic 的 LLM 输出——把"流量事实"
// （operation / resource_scope / param_locations / carries_auth /
// credential_locations / reasoning）显式持久化，让归档后的扫描会话
// 仍可审计、复现和按维度统计。
//
// v1.3 命名整改（与表名 flow_decision → flow_facts 同步）：
//   - 表 flow_decision → flow_facts（v1.3 起本表只存事实，不再做决策）
//   - 列 attack_surfaces → param_locations（只是参数位置，与"攻击面"无关）
//   - 删 required_skills 列与字段（路由决策已下放回主 ReAct）
//
// 包名暂保留 flowdecision，避免 import 风暴；类型 Decision → Facts
// 与表新名同步。
package flowfacts

import (
	"encoding/json"
	"time"
)

// ResourceScope 是 flow_facts.resource_scope 的文本枚举（与 SQL CHECK 一致）。
type ResourceScope string

const (
	ResourceScopePrivate ResourceScope = "private"
	ResourceScopePublic  ResourceScope = "public"
	ResourceScopeUnknown ResourceScope = "unknown"
)

// Facts 是 flow_facts 表行的 Go 表示。
//
// TaskID 可空（主 ReAct 第 1 步调 classify_traffic 时还没注册 sub-task）。
// jsonb 字段以 json.RawMessage 透传，由调用方 marshal 成稳定 JSON。
type Facts struct {
	ID                  int64
	EngagementID        string
	FlowID              int64
	TaskID              *string
	Operation           string
	ResourceScope       ResourceScope
	ParamLocations      json.RawMessage
	CarriesAuth         bool
	CredentialLocations json.RawMessage
	Reasoning           string
	CreatedAt           time.Time
}
