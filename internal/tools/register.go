package tools

import (
	"time"

	"github.com/V3teran/liusha/internal/registry"
)

// RegisterAll 向 reg 注入全部域工具（按 deps 中可用的存储按需注册）。
// sandbox / skill 工具在对应依赖为 nil 时跳过，不影响其余工具。
func RegisterAll(reg *registry.Registry, deps Deps) {
	// control（无状态，始终注册）
	reg.Register(newDoneTool())
	reg.Register(newMarkInsightTool())

	// credentials（快速读写，并发安全）
	if deps.Creds != nil {
		reg.Register(newReadCredentialsTool(deps, 5*time.Second, true))
		reg.Register(newWriteCredentialTool(deps, 5*time.Second, true))
	}

	// findings（快速读写，并发安全）
	if deps.Findings != nil {
		reg.Register(newReadFindingsTool(deps, 5*time.Second, true))
		// write_finding 移到 Evaluator 专用（验证后才能写入 finding 表）
		// reg.Register(&writeFindingTool{deps: deps})
		reg.Register(newUpdateFindingTool(deps, 10*time.Second, true))
	}

	// corpus（搜索可能慢，写入快）
	if deps.Corpus != nil {
		reg.Register(newSearchCorpusTool(deps, 30*time.Second, true))
		reg.Register(newWriteCorpusTool(deps, 5*time.Second, true))
	}

	// lead（快速写入，并发安全）
	if deps.Leads != nil {
		reg.Register(newWriteLeadTool(deps, 5*time.Second, true))
	}

	// traffic（读取可能较慢，replay 更慢且不安全）
	if deps.ProxyStore != nil || deps.AgentStore != nil {
		reg.Register(newListTrafficTool(deps, 30*time.Second, true))
		reg.Register(newViewTrafficTool(deps, 10*time.Second, true))
		reg.Register(newReplayTrafficTool(deps, 60*time.Second, false)) // replay 串行
	}

	// sandbox（命令执行慢且不安全，浏览器更慢）
	if deps.Sandbox != nil {
		reg.Register(newRunCommandTool(deps, 120*time.Second, false))
		reg.Register(newBrowserUseTool(deps, 180*time.Second, false))
	}

	// skill（读取技能文档，快速且安全）
	if deps.ToolingLoader != nil {
		reg.Register(newReadToolingSkillTool(deps, 5*time.Second, true))
	}
	if deps.VulnLoader != nil {
		reg.Register(newReadVulnSkillTool(deps, 5*time.Second, true))
	}

	// exploration graph（写入观察和证据，快速且安全）
	if deps.World != nil {
		reg.Register(newWriteObservationTool(deps, 10*time.Second, true))
		reg.Register(newWriteEvidenceTool(deps, 10*time.Second, true))
	}
}
