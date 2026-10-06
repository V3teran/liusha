// Package skillstore 实现 Skill 配置表的持久化层。
//
// Skill 是 Agent 可访问的知识库文档，包含工具使用手册、漏洞检测指南等。
// 用户可在前端对 Skill 进行增删改查，Agent 通过 skills 字段关联可访问的 Skill 范围。
package skillstore

import "time"

// Skill 是 skill 配置表的 Go 表示。
type Skill struct {
	ID          string    `json:"id"`
	Code        string    `json:"code"`      // 裸名寻址键（= 种子目录名，如 browser-use / dom-xss）
	Category    string    `json:"category"`  // tooling / vuln
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Body        string    `json:"body"` // Markdown 正文
	IsBuiltin   bool      `json:"is_builtin"`
	Enabled     bool      `json:"enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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
