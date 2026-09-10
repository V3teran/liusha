// Package skill 实现 Skill 配置表的持久化层。
//
// Skill 是 Agent 可访问的知识库文档，包含工具使用手册、漏洞检测指南等。
// 用户可在前端对 Skill 进行增删改查，Agent 通过 skills 字段关联可访问的 Skill 范围。
package skill

import "time"

// Skill 是 skill 配置表的 Go 表示。
type Skill struct {
	ID          string
	Code        string // 如 "tooling/browser-use", "vuln/dom-xss"
	Category    string // tooling / vuln
	Name        string
	Description string
	Body        string // Markdown 正文
	IsBuiltin   bool
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// UpdateParams 是 Store.Update 的入参。
type UpdateParams struct {
	Name        *string
	Description *string
	Body        *string
	Enabled     *bool
}

// ListParams 是 Store.List 的查询参数。
type ListParams struct {
	Category    string // 可选：按 category 过滤
	OnlyEnabled bool   // 只返回 enabled=true 的
	Search      string // 可选：在 name/description 中搜索
	Limit       int
	Offset      int
}
