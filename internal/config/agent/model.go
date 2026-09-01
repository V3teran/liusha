// Package agent 实现Agent配置表的持久化层。
//
// Agent配置表只包含两个固定角色：
//   - Planner：规划者，负责全局规划和任务分解（6分钟评估周期）
//   - Executor：执行者，负责具体执行任务（5步评估周期）
//
// Agent是配置，而非运行实例。运行实例由其他包管理。
package agent

import "time"

// Kind 是 agent.kind 的取值。
type Kind string

const (
	KindPlanner  Kind = "planner"  // 规划者
	KindExecutor Kind = "executor" // 执行者
)

// Agent 是 agent 配置表的 Go 表示。
//
// 字段说明：
//   - SystemPrompt：Agent的System Prompt，定义其行为和能力（对应数据库的 body 列）
//   - FunctionTools：LLM可直接调用的function calling工具
//   - CliTools：外部命令行工具
type Agent struct {
	ID            string
	Code          string
	Kind          Kind
	Name          string
	Description   string
	SystemPrompt  string   // 对应数据库的 body 列
	FunctionTools []string
	CliTools      []string
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
	MaxIterations *int
	Complexity    *string
}
