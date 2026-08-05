// Package hunter 实现配置猎手（hunter 配置表）的持久化层——离散领域猎手的
// 方法论 charter、工具集与派活摘要，DB 是事实源，前端可编辑。
//
// 与 internal/hunterrun（每次 ReAct 运行的记录）物理隔离：本包管「猎手是什么」，
// hunterrun 管「某次运行发生了什么」。import 时用别名 cfghunter 避免与 einoagent
// 的 hunter 包（M2）冲突。
//
// kind：
//   - orchestrator：engine=swarm 时自动注入的编排猎手，全局唯一，不进领域池
//   - domain      ：领域猎手，swarm 时入自动池、solo 时被场景单点引用
package hunter

import "time"

// Kind 是 hunter.kind 的取值（与 DB CHECK 双保险）。
type Kind string

const (
	KindOrchestrator Kind = "orchestrator"
	KindDomain       Kind = "domain"
)

// Hunter 是 hunter 配置表行的 Go 表示。
//   - Description：派活摘要，swarm 时注入 deep task 工具供编排者据此选派（非给人看的简介）
//   - Body       ：方法论正文（charter），该猎手跑起来时的 system 指令
//   - Tools      ：内置函数工具集（run_command/write_finding… 的 code 列表），走 jsonb ↔ []string
//   - CliTools    ：外置 CLI 工具白名单（tools.yaml 名字），独立于 Tools；空 = 域内全部可见
type Hunter struct {
	ID            string
	Code          string
	Kind          Kind
	Name          string
	Description   string
	Body          string
	Tools         []string
	CliTools      []string
	MaxIterations int
	Enabled       bool
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
	Tools         []string
	CliTools      []string
	MaxIterations int
	Enabled       bool
}
