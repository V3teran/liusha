// Package scenario 实现场景（scenario 表）的持久化层——选 engine（solo/swarm），带交
// 战域 domain。solo 场景额外指向唯一执行 hunter（SoloHunterID）。是运行期派发的入口
// 配置（task.scenario_id 存 scenario.code）。import 时用别名 cfgscenario，区别于旧
// role 系统 internal/scenario。
package scenario

import "time"

// 引擎常量：
//   - solo ：单 hunter 独立执行（SoloHunterID 指定），无编排
//   - swarm：orchestrator + 全部 enabled 领域 hunter 池，LLM 运行时动态 handoff
const (
	EngineSolo  = "solo"
	EngineSwarm = "swarm"
)

// Scenario 是 scenario 表行的 Go 表示。
//   - Instruction ：场景领域侧重，注入 AI（旧 scenario md 正文）
//   - Domain      ：交战域（web/ctf/cloud…），CLI 扫描工具目录过滤键（见 D11/M7）
//   - Engine       ∈ {EngineSolo, EngineSwarm}
//   - SoloHunterID：solo 引擎唯一执行 hunter 的 uuid；swarm 场景为 nil（DB CHECK 双保险）
type Scenario struct {
	ID           string
	Code         string
	Name         string
	Description  string
	Instruction  string
	Domain       string
	Engine       string
	SoloHunterID *string
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// NewParams 是 Store.Create / Store.Update 的入参。
type NewParams struct {
	Code         string
	Name         string
	Description  string
	Instruction  string
	Domain       string
	Engine       string
	SoloHunterID *string
	Enabled      bool
}
