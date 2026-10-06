// Package agent 实现Agent配置表的持久化层。
//
// 四个固定角色以 code 寻址（planner/executor/evaluator/monitor），无独立种类字段——
// 角色语义由 code 与 charter 正文（system_prompt）承载。
//
// Agent是配置，而非运行实例。运行实例由其他包管理。
package agent

import "time"

// Agent 是 agent 配置表的 Go 表示。
//
// 字段说明：
//   - SystemPrompt：Agent的System Prompt，定义其行为和能力（对应数据库的 system_prompt 列）
//   - FunctionTools：LLM可直接调用的function calling工具
//   - CliTools：外部命令行工具
//   - Skills：Agent可访问的skill裸名列表（如 ["bac", "browser-use", "dom-xss"]，
//     = skills/<分类>/<名字> 的目录名），渐进式加载白名单（Tier1 索引 / Tier2 read_skill）
type Agent struct {
	ID            string
	Code          string
	Name          string
	Description   string
	SystemPrompt  string
	FunctionTools []string
	CliTools      []string
	Skills        []string
	MaxIterations int
	Complexity    string
	Enabled       bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// UpdateParams 是 Store.Update 的入参。
type UpdateParams struct {
	SystemPrompt  *string
	FunctionTools *[]string
	CliTools      *[]string
	Skills        *[]string
	MaxIterations *int
	Complexity    *string
}
