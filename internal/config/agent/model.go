// Package executor 实现配置执行体（agent 配置表）的持久化层——离散领域操作员的
// 方法论 charter、工具集与派活摘要，DB 是事实源，前端可编辑。
//
// 与 internal/agentrun（每次 ReAct 运行的记录）物理隔离：本包管「操作员是什么」，
// agentrun 管「某次运行发生了什么」。import 时用别名 cfgagent 避免与 agentrun
// 的 agent 包（M2）冲突。
//
// kind：
//   - planner：engine=swarm 时自动注入的编排操作员，全局唯一，不进领域池
//   - domain      ：领域操作员，swarm 时入自动池、solo 时被场景单点引用
package executor

import "time"

// Kind 是 agent.kind 的取值（与 DB CHECK 双保险）。
type Kind string

const (
	KindPlanner Kind = "planner"
	KindExecutor       Kind = "domain"
)

// Agent 是 agent 配置表行的 Go 表示。
//   - Description：派活摘要，swarm 时注入 deep task 工具供编排者据此选派（非给人看的简介）
//   - Body       ：方法论正文（charter），该操作员跑起来时的 system 指令
//   - FunctionTools：内置函数工具集（run_command/write_finding… 的 code 列表），走 jsonb ↔ []string
//   - CliTools     ：外置 CLI 工具集（tools.yaml 名字），独立于 FunctionTools；严格白名单，空 = 不装配任何外部工具
type Agent struct {
	ID            string
	Code          string
	Kind          Kind
	Name          string
	Description   string
	Body          string
	FunctionTools []string
	CliTools      []string
	MaxIterations int
	Enabled       bool
	Complexity    string // 复杂度档位 simple|medium|complex：agent → LLM 复杂度绑定，用户可配置
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// NewParams 是 Store.Create / Store.Update 的入参。
type NewParams struct {
	Code          string
	Kind          Kind
	Name          string
	Description   string
	Body          string
	FunctionTools []string
	CliTools      []string
	MaxIterations int
	Enabled       bool
	Complexity    string // 复杂度档位 simple|medium|complex（空 = 落 DB DEFAULT 'medium'）
}
