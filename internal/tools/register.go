package tools

import (
	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/registry"
)

// RegisterAll 向 reg 注入全部域工具（按 deps 中可用的存储按需注册）。
// sandbox / skill 工具在对应依赖为 nil 时跳过，不影响其余工具。
func RegisterAll(reg *registry.Registry, deps Deps) {
	for _, t := range BuildTools(deps, []string{"*"}) {
		reg.Register(t)
	}
}

// BuildTools 按 deps 构造工具集，再按 names 白名单过滤（agent.function_tools 配置的
// 运行时执行点）。names 为 nil = 拒绝；空 slice = 空集；["*"] = 全量；[...] = 白名单（严格白名单，与 cli_tools 语义
// 一致，同 registry.Allows 的语义）；白名单里不存在的名字静默忽略（配置漂移
// 不致命）。control/观察类工具（publish_decision、observe_state 等）在各角色包内自建，
// 不在此注册。
func BuildTools(deps Deps, names []string) []registry.Tool {
	var all []registry.Tool
	add := func(t registry.Tool) { all = append(all, t) }

	// credentials（快速读写，并发安全）
	if deps.Creds != nil {
		add(newReadCredentialsTool(deps, constants.ToolTimeoutQuick))
		add(newWriteCredentialTool(deps, constants.ToolTimeoutQuick))
	}

	// corpus（搜索可能慢，写入快）
	if deps.Corpus != nil {
		add(newSearchCorpusTool(deps, constants.ToolTimeoutLong))
	}

	// insight 读写（快速，并发安全）
	if deps.Insights != nil {
		add(newReadInsightsTool(deps, constants.ToolTimeoutQuick))
		add(newWriteInsightTool(deps, constants.ToolTimeoutQuick))
	}

	// traffic（读取可能较慢，replay 更慢且不安全）
	if deps.ProxyStore != nil || deps.AgentStore != nil {
		add(newListTrafficTool(deps, constants.ToolTimeoutLong))
		add(newViewTrafficTool(deps, constants.ToolTimeoutMedium))
	}
	// http_request（带抓流的 typed HTTP——晋升链复现原语的采集前端，mitm 缺位时的对位物）
	if deps.AgentStore != nil {
		add(newHTTPRequestTool(deps, constants.ToolTimeoutQuick))
	}

	// sandbox（命令执行慢且不安全，浏览器更慢）
	if deps.Sandbox != nil {
		add(newRunCommandTool(deps, runCommandTimeout(deps))) // 注册表级超时 = runtime.step_tool_timeout_seconds（同源钳制）
		add(newDriveBrowserTool(deps, constants.ToolTimeoutBrowser))
	}

	// skill（读取技能文档，快速且安全）——Reader 是按 agent.skills 声明建的
	// 白名单视图（DB 事实源 + 多级缓存后端）；nil（未装配）不注册。
	// 声明为空的 agent 由装配层（cognition）直接不传 Reader，不会走到这里。
	if deps.Skills != nil {
		add(newReadSkillTool(deps.Skills, constants.ToolTimeoutQuick))
	}

	if names == nil {
		return []registry.Tool{} // nil = 默认拒绝（registry.Allows 同口径）
	}
	out := make([]registry.Tool, 0, len(names))
	for _, t := range all {
		if registry.Allows(names, t.Name()) {
			out = append(out, t)
		}
	}
	return out
}
