// Package engagement 定义单次渗透测试的顶层生命周期实体及其业务不变量。
// 三级生命周期: Engagement → AnalysisWindow → AgentTask，本包负责最顶层。
package engagement

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Mode 枚举 engagement 的采集模式: proxy（HTTP 代理）/ browser（浏览器接管）/ ctf（CTF 场景）。
type Mode string

const (
	ModeProxy   Mode = "proxy"
	ModeBrowser Mode = "browser"
	ModeCTF     Mode = "ctf"
)

// validModes 与 db/migrations/0001 里的 CHECK 约束保持一致。
var validModes = map[Mode]struct{}{
	ModeProxy:   {},
	ModeBrowser: {},
	ModeCTF:     {},
}

// Status 枚举 engagement 的生命周期状态，与 db 层 CHECK 约束一致。
type Status string

const (
	StatusActive   Status = "active"
	StatusPaused   Status = "paused"
	StatusArchived Status = "archived"
	StatusAborted  Status = "aborted"
)

// Engagement 是一次渗透测试的聚合根，Scope 保留 JSON 灵活性以承载靶场配置。
// Version 用于存储层乐观锁，由 store 层负责在更新时递增。
type Engagement struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Mode      Mode
	Scope     map[string]any
	Status    Status
	Objective string
	Version   int64
	StartedAt time.Time
	UpdatedAt time.Time
	EndedAt   *time.Time
	Meta      map[string]any
}

// CreateInput 是创建 engagement 的输入契约，不暴露内部字段如 ID/Version/Status。
type CreateInput struct {
	TenantID  uuid.UUID
	Mode      Mode
	Scope     map[string]any
	Objective string
	Meta      map[string]any
}

// New 构造合法的 Engagement：校验租户、模式、scope 非空；生成 UUID 与初始时间戳。
// 不做 I/O，纯领域逻辑，便于在单测中断言不变量。
func New(in CreateInput) (Engagement, error) {
	if in.TenantID == uuid.Nil {
		return Engagement{}, errors.New("tenant_id is required")
	}
	if _, ok := validModes[in.Mode]; !ok {
		return Engagement{}, fmt.Errorf("mode must be proxy/browser/ctf, got %q", in.Mode)
	}
	if len(in.Scope) == 0 {
		return Engagement{}, errors.New("scope is required and must be non-empty")
	}
	now := time.Now().UTC()
	return Engagement{
		ID:        uuid.New(),
		TenantID:  in.TenantID,
		Mode:      in.Mode,
		Scope:     in.Scope,
		Status:    StatusActive,
		Objective: in.Objective,
		Version:   1,
		StartedAt: now,
		UpdatedAt: now,
		Meta:      in.Meta,
	}, nil
}
