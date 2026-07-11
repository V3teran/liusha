package einoagent

import (
	"fmt"

	"github.com/cloudwego/eino/components/tool"

	"github.com/V3teran/liusha/internal/einotools"
)

// role_tools.go：工具注册表——把角色 markdown 里声明的「工具名」运行时建成实际 eino 工具。
//
// 角色定义（role.go）的 Tools 字段是字符串清单（read_findings/write_finding/...）。本注册表
// 把每个名字映射到一个 builder：用运行期注入值（owner/host/hunter/sandbox）建实例。
// 这样「角色用哪些工具」由 markdown 配（动态），「工具怎么注入身份」由代码定（安全，防 LLM 串库）。

// ToolBuildCtx 是建工具的运行期上下文（per agent run 注入，LLM 不可控）。
type ToolBuildCtx struct {
	Deps   TrafficAnalysisToolDeps   // store/loader/sandbox
	Params TrafficAnalysisToolParams // owner/host/hunter/flow 注入值
}

// toolBuilder 按运行期上下文造一个工具实例。
type toolBuilder func(c ToolBuildCtx) (tool.BaseTool, error)

// toolRegistry 是「工具名 → builder」表。新增工具：在此注册一行 + 角色 md 写工具名即可。
var toolRegistry = map[string]toolBuilder{
	// notes 工具（read_notes/write_note）已退役——agent 思路改输出到对话（reasoning 事件），
	// 不再用独立 notes 黑板。跨 run 上下文走对话历史 + finding/lesson 黑板。
	"read_credentials": func(c ToolBuildCtx) (tool.BaseTool, error) {
		return einotools.BuildReadCredentials(c.Deps.Credentials, c.Params.Host)
	},
	"write_credential": func(c ToolBuildCtx) (tool.BaseTool, error) {
		return einotools.BuildWriteCredential(c.Deps.Credentials, c.Params.Host)
	},
	"read_findings": func(c ToolBuildCtx) (tool.BaseTool, error) {
		return einotools.BuildReadFindings(c.Deps.Findings, c.Params.TaskID, c.Params.Host)
	},
	"write_finding": func(c ToolBuildCtx) (tool.BaseTool, error) {
		return einotools.BuildWriteFinding(c.Deps.Findings, c.Params.TaskID, c.Params.HunterID, c.Params.Host, c.Params.FlowID)
	},
	"update_finding": func(c ToolBuildCtx) (tool.BaseTool, error) {
		return einotools.BuildUpdateFinding(c.Deps.Findings)
	},
	"read_lessons": func(c ToolBuildCtx) (tool.BaseTool, error) {
		return einotools.BuildReadLessons(c.Deps.Lessons, c.Params.Host)
	},
	"write_lesson": func(c ToolBuildCtx) (tool.BaseTool, error) {
		return einotools.BuildWriteLesson(c.Deps.Lessons, c.Params.Host)
	},
	"write_lead": func(c ToolBuildCtx) (tool.BaseTool, error) {
		return einotools.BuildWriteLead(c.Deps.Lead, c.Params.Host, c.Params.HunterID, c.Params.TaskID)
	},
	"done": func(c ToolBuildCtx) (tool.BaseTool, error) {
		return einotools.BuildDone()
	},

	// 流量字典（active：读自产 agent_traffic，按 task_id）
	"replay_flow": func(c ToolBuildCtx) (tool.BaseTool, error) {
		if c.Deps.AgentFlows == nil {
			return nil, errFlowsNil("replay_flow")
		}
		return einotools.BuildReplayFlow(einotools.NewAgentFlowScope(c.Deps.AgentFlows, c.Params.TaskID))
	},
	"list_flows": func(c ToolBuildCtx) (tool.BaseTool, error) {
		if c.Deps.AgentFlows == nil {
			return nil, errFlowsNil("list_flows")
		}
		return einotools.BuildListFlows(einotools.NewAgentFlowScope(c.Deps.AgentFlows, c.Params.TaskID), c.Params.Host)
	},
	"view_flow": func(c ToolBuildCtx) (tool.BaseTool, error) {
		if c.Deps.AgentFlows == nil {
			return nil, errFlowsNil("view_flow")
		}
		return einotools.BuildViewFlow(einotools.NewAgentFlowScope(c.Deps.AgentFlows, c.Params.TaskID))
	},

	// 沙箱（需 Sandbox）
	"run_command": func(c ToolBuildCtx) (tool.BaseTool, error) {
		if c.Deps.Sandbox == nil {
			return nil, fmt.Errorf("run_command: Sandbox 未注入")
		}
		return einotools.BuildRunCommand(c.Deps.Sandbox, c.Params.HunterID, c.Deps.MaxTimeoutSeconds, c.Deps.TailBytes)
	},
	// 浏览器（需 Sandbox）：browser-use CLI 的 typed 包装，传 identity 做登录态复用（见 browser_use.go）
	"browser_use": func(c ToolBuildCtx) (tool.BaseTool, error) {
		if c.Deps.Sandbox == nil {
			return nil, fmt.Errorf("browser_use: Sandbox 未注入")
		}
		return einotools.BuildBrowserUse(c.Deps.Sandbox, c.Params.HunterID, c.Deps.TailBytes)
	},

	// 技能索引（需 Loader 且 catalog 非空）
	"read_tooling_skill": func(c ToolBuildCtx) (tool.BaseTool, error) {
		if c.Deps.ToolingLoader == nil || len(c.Deps.ToolingLoader.List()) == 0 {
			return nil, errLoaderEmpty("read_tooling_skill")
		}
		return einotools.BuildReadToolingSkill(c.Deps.ToolingLoader)
	},
	"read_vuln_skill": func(c ToolBuildCtx) (tool.BaseTool, error) {
		if c.Deps.VulnLoader == nil || len(c.Deps.VulnLoader.List()) == 0 {
			return nil, errLoaderEmpty("read_vuln_skill")
		}
		return einotools.BuildReadVulnSkill(c.Deps.VulnLoader)
	},
}

func errFlowsNil(name string) error    { return fmt.Errorf("%s: Flows store 未注入", name) }
func errLoaderEmpty(name string) error { return fmt.Errorf("%s: Loader nil 或空 catalog", name) }

// BuildRoleTools 按角色的工具名清单 + 运行期上下文，建该角色的工具集。
//
// 依赖缺失（如 run_command 声明了但 Sandbox nil）→ 报错，启动期暴露配置/装配缺漏。
func BuildRoleTools(role RoleDef, c ToolBuildCtx) ([]tool.BaseTool, error) {
	tools := make([]tool.BaseTool, 0, len(role.Tools))
	for _, name := range role.Tools {
		b, ok := toolRegistry[name]
		if !ok {
			return nil, fmt.Errorf("角色 %q: 未知工具 %q（不在注册表）", role.ID, name)
		}
		bt, err := b(c)
		if err != nil {
			return nil, fmt.Errorf("角色 %q 建工具 %q: %w", role.ID, name, err)
		}
		tools = append(tools, bt)
	}
	return tools, nil
}

// KnownToolNames 返回注册表里所有合法工具名（供角色 md 校验/文档/测试）。
func KnownToolNames() []string {
	names := make([]string, 0, len(toolRegistry))
	for n := range toolRegistry {
		names = append(names, n)
	}
	return names
}
