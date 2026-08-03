// Package scenario 实现场景（scenario 表）的持久化层——引用一个 playbook + 独立选
// engine（solo/swarm），带交战域 domain。是运行期派发的入口配置（task.scenario_id 存
// scenario.code）。import 时用别名 cfgscenario，区别于旧 role 系统 internal/scenario。
package scenario

import "time"

// 引擎常量：engine 与 playbook 正交，任意场景可选任意 engine（见 D2）。
const (
	EngineSolo  = "solo"
	EngineSwarm = "swarm"
)

// Scenario 是 scenario 表行的 Go 表示。
//   - Instruction：场景领域侧重，注入 AI（旧 scenario md 正文）
//   - Domain     ：交战域（web/ctf/cloud…），CLI 扫描工具目录过滤键（见 D11/M7）
//   - Engine      ∈ {EngineSolo, EngineSwarm}
type Scenario struct {
	ID          string
	Code        string
	Name        string
	Description string
	Instruction string
	Domain      string
	Engine      string
	PlaybookID  string
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NewParams 是 Store.Create / Store.Update 的入参。
type NewParams struct {
	Code        string
	Name        string
	Description string
	Instruction string
	Domain      string
	Engine      string
	PlaybookID  string
	Enabled     bool
}
