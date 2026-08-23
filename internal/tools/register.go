package tools

import "github.com/V3teran/liusha/internal/registry"

// RegisterAll 向 reg 注入全部域工具（按 deps 中可用的存储按需注册）。
// sandbox / skill 工具在对应依赖为 nil 时跳过，不影响其余工具。
func RegisterAll(reg *registry.Registry, deps Deps) {
	// control（无状态，始终注册）
	reg.Register(&doneTool{})
	reg.Register(&markInsightTool{})

	// credentials
	if deps.Creds != nil {
		reg.Register(&readCredentialsTool{deps: deps})
		reg.Register(&writeCredentialTool{deps: deps})
	}

	// findings
	if deps.Findings != nil {
		reg.Register(&readFindingsTool{deps: deps})
		reg.Register(&writeFindingTool{deps: deps})
		reg.Register(&updateFindingTool{deps: deps})
	}

	// corpus
	if deps.Corpus != nil {
		reg.Register(&searchCorpusTool{deps: deps})
		reg.Register(&writeCorpusTool{deps: deps})
	}

	// lead
	if deps.Leads != nil {
		reg.Register(&writeLeadTool{deps: deps})
	}

	// traffic（proxyStore 或 agentStore 任一可用即注册）
	if deps.ProxyStore != nil || deps.AgentStore != nil {
		reg.Register(&listTrafficTool{deps: deps})
		reg.Register(&viewTrafficTool{deps: deps})
		reg.Register(&replayTrafficTool{deps: deps})
	}

	// sandbox
	if deps.Sandbox != nil {
		reg.Register(&runCommandTool{deps: deps})
		reg.Register(&browserUseTool{deps: deps})
	}

	// skill
	if deps.ToolingLoader != nil {
		reg.Register(&readToolingSkillTool{deps: deps})
	}
	if deps.VulnLoader != nil {
		reg.Register(&readVulnSkillTool{deps: deps})
	}
}
