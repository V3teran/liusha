package einoagent

import (
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/tool"

	"github.com/V3teran/liusha/internal/einotools"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/skill"
)

// 必装 store 组合接口（accept interfaces）：*finding.Store / *lesson.Store /
// credential.Provider 各自自动满足；单测可注入 fake。
type (
	// FindingStore 满足 read/write/update_finding 三工具。
	FindingStore interface {
		einotools.FindingReader
		einotools.FindingWriter
		einotools.FindingUpdater
	}
	// LessonStore 满足 read/write_lesson。
	LessonStore interface {
		einotools.LessonLister
		einotools.LessonAdder
	}
	// CredentialStore 满足 read/write_credential。
	CredentialStore interface {
		einotools.CredentialReader
		einotools.CredentialWriter
	}
	// FlowStore 满足 replay_flow（Reader）+ list_flows（Lister）+ view_flow（Reader）。
	FlowStore interface {
		einotools.FlowReader
		einotools.FlowLister
	}
)

// TrafficAnalysisToolDeps 是装配 trafficAnalysis eino 工具集所需的依赖。
// 由 cmd/scanner composition root 注入（与旧 hunter.Deps 同源 store）。
type TrafficAnalysisToolDeps struct {
	Findings    FindingStore
	Lessons     LessonStore
	Credentials CredentialStore

	// Flows 是接口（scanner 传 *flow.Store 满足）；nil 时不注册 replay/list/view_flow。
	// 注意 nil 门控：scanner 始终传非 nil 真实 store，故无 typed-nil 装箱陷阱。
	Flows FlowStore

	// 可选：用具体指针类型，nil 时不注册对应工具（nil 门控语义与 hunter/skill.go 一致，
	// 避免 typed-nil 装箱进接口后 != nil 的陷阱）。
	ToolingLoader *skill.Loader
	VulnLoader    *skill.Loader
	Sandbox       sandbox.Client

	MaxTimeoutSeconds int // run_command timeout 钳上限
	TailBytes         int // run_command stdout/stderr 截尾
}

// TrafficAnalysisToolParams 是 per-run 注入值（LLM 不可控，防串库）。
type TrafficAnalysisToolParams struct {
	OwnerType string
	OwnerID   string
	HunterID  string
	Host      string
	FlowID    int64
}

// BuildTrafficAnalysisTools 装配 trafficAnalysis（passive 单 agent）的工具集。
//
// deep active 路径（orchestrator/exploitation 等杀伤链角色）改走 role_tools.go 的 BuildRoleTools——
// 工具由角色 md 的 tools 清单声明、运行时注入身份建实例，不再用本函数。故这里只服务 passive
// trafficAnalysis：固定工具集，无 list/view_flow（passive 单流量驱动不需枚举站点流量）、无 spawn。
func BuildTrafficAnalysisTools(deps TrafficAnalysisToolDeps, p TrafficAnalysisToolParams) ([]tool.BaseTool, error) {
	var tools []tool.BaseTool
	var errs []error
	add := func(bt tool.BaseTool, err error) {
		if err != nil {
			errs = append(errs, err)
			return
		}
		tools = append(tools, bt)
	}

	// credentials / findings(读写) / lessons（notes 已退役）
	add(einotools.BuildReadCredentials(deps.Credentials, p.Host))
	add(einotools.BuildWriteCredential(deps.Credentials, p.Host))
	add(einotools.BuildReadFindings(deps.Findings, p.OwnerType, p.OwnerID, p.Host))
	add(einotools.BuildWriteFinding(deps.Findings, p.OwnerType, p.OwnerID, p.HunterID, p.Host, p.FlowID))
	add(einotools.BuildUpdateFinding(deps.Findings))
	add(einotools.BuildReadLessons(deps.Lessons, p.Host))
	add(einotools.BuildWriteLesson(deps.Lessons, p.Host))
	// done：prompt 是 react/eino 共享资产、深度依赖 done 收尾——不注册会「tool done not found」（e2e 实测）。
	add(einotools.BuildDone())

	// 流量字典：trafficAnalysis 只重放当前流量，不枚举站点（list/view 是 active 的事）。
	if deps.Flows != nil {
		add(einotools.BuildReplayFlow(deps.Flows, p.OwnerType, p.OwnerID, p.HunterID))
	}

	// 可选索引/沙箱（nil / 空 catalog 跳过，与 skill.go 门控一致）
	if deps.ToolingLoader != nil && len(deps.ToolingLoader.List()) > 0 {
		add(einotools.BuildReadToolingSkill(deps.ToolingLoader))
	}
	if deps.VulnLoader != nil && len(deps.VulnLoader.List()) > 0 {
		add(einotools.BuildReadVulnSkill(deps.VulnLoader))
	}
	if deps.Sandbox != nil {
		add(einotools.BuildRunCommand(deps.Sandbox, p.HunterID, deps.MaxTimeoutSeconds, deps.TailBytes))
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("build trafficAnalysis tools: %w", errors.Join(errs...))
	}
	return tools, nil
}
