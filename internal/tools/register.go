package tools

import (
	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/registry"
)

// RegisterAll 向 reg 注入全部域工具（按 deps 中可用的存储按需注册）。
// sandbox / skill 工具在对应依赖为 nil 时跳过，不影响其余工具。
func RegisterAll(reg *registry.Registry, deps Deps) {
	for _, t := range BuildTools(deps, nil) {
		reg.Register(t)
	}
}

// BuildTools 按 deps 构造工具集，再按 names 白名单过滤（agent.function_tools 配置的
// 运行时执行点）。names 为 nil = 全量；空 slice = 空集（严格白名单，与 cli_tools 语义
// 一致）；白名单里不存在的名字静默忽略（配置漂移不致命）。
func BuildTools(deps Deps, names []string) []registry.Tool {
	var all []registry.Tool
	add := func(t registry.Tool) { all = append(all, t) }

	// control（无状态，始终注册）
	add(newDoneTool())
	add(newMarkInsightTool())

	// credentials（快速读写，并发安全）
	if deps.Creds != nil {
		add(newReadCredentialsTool(deps, constants.ToolTimeoutQuick, true))
		add(newWriteCredentialTool(deps, constants.ToolTimeoutQuick, true))
	}

	// findings（快速读写，并发安全）
	if deps.Findings != nil {
		add(newReadFindingsTool(deps, constants.ToolTimeoutQuick, true))
		// write_finding 移到 Evaluator 专用（验证后才能写入 finding 表）
		// reg.Register(&writeFindingTool{deps: deps})
		add(newUpdateFindingTool(deps, constants.ToolTimeoutMedium, true))
	}

	// corpus（搜索可能慢，写入快）
	if deps.Corpus != nil {
		add(newSearchCorpusTool(deps, constants.ToolTimeoutLong, true))
		add(newWriteCorpusTool(deps, constants.ToolTimeoutQuick, true))
	}

	// insight 写入（快速，并发安全）
	if deps.Insights != nil {
		add(newWriteInsightTool(deps, constants.ToolTimeoutQuick, true))
	}

	// traffic（读取可能较慢，replay 更慢且不安全）
	if deps.ProxyStore != nil || deps.AgentStore != nil {
		add(newListTrafficTool(deps, constants.ToolTimeoutLong, true))
		add(newViewTrafficTool(deps, constants.ToolTimeoutMedium, true))
		add(newReplayTrafficTool(deps, constants.ToolTimeoutReplay, false)) // replay 串行
	}
	// http_request（带抓流的 typed HTTP——晋升链复现原语的采集前端，mitm 缺位时的对位物）
	if deps.AgentStore != nil {
		add(newHTTPRequestTool(deps, constants.ToolTimeoutQuick, true))
	}

	// sandbox（命令执行慢且不安全，浏览器更慢）
	if deps.Sandbox != nil {
		add(newRunCommandTool(deps, constants.ToolTimeoutCommand, false))
		add(newBrowserUseTool(deps, constants.ToolTimeoutBrowser, false))
	}

	// skill（读取技能文档，快速且安全）
	if deps.ToolingLoader != nil {
		add(newReadToolingSkillTool(deps, constants.ToolTimeoutQuick, true))
	}
	if deps.VulnLoader != nil {
		add(newReadVulnSkillTool(deps, constants.ToolTimeoutQuick, true))
	}

	// exploration graph（写入观察和证据，快速且安全）
	if deps.Graph != nil {
		add(newWriteObservationTool(deps, constants.ToolTimeoutMedium, true))
		add(newWriteEvidenceTool(deps, constants.ToolTimeoutMedium, true))
	}

	if len(names) == 0 {
		return all
	}
	allow := make(map[string]struct{}, len(names))
	for _, n := range names {
		allow[n] = struct{}{}
	}
	out := make([]registry.Tool, 0, len(names))
	for _, t := range all {
		if _, ok := allow[t.Name()]; ok {
			out = append(out, t)
		}
	}
	return out
}
