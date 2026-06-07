package einoagent

import (
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/tool"

	"github.com/V3teran/liusha/internal/einotools"
	"github.com/V3teran/liusha/internal/notes"
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

// TrackerToolDeps 是装配 tracker eino 工具集所需的依赖。
// 由 cmd/scanner composition root 注入（与旧 hunter.Deps 同源 store）。
type TrackerToolDeps struct {
	Findings    FindingStore
	Notes       notes.Store
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

// TrackerToolParams 是 per-run 注入值（LLM 不可控，防串库）。
type TrackerToolParams struct {
	OwnerType string
	OwnerID   string
	HunterID  string
	Host      string
	FlowID    int64
}

// BuildTrackerTools 装配 tracker（passive 单 agent）的 eino 工具集。
//
// 对标 hunter/skill.go 的 tracker 分支：必装 9 个（notes/credentials/findings×3/lessons）
// + 可选 4 个（replay_flow / read_tooling_skill / read_vuln_skill / run_command）。
// 不含 done（eino 单 agent 不调工具即自然收尾）、list_flows/view_flow（active-only）、
// spawn_striker/list_strikers（commander-only）。
func BuildTrackerTools(deps TrackerToolDeps, p TrackerToolParams) ([]tool.BaseTool, error) {
	var tools []tool.BaseTool
	var errs []error
	add := func(bt tool.BaseTool, err error) {
		if err != nil {
			errs = append(errs, err)
			return
		}
		tools = append(tools, bt)
	}

	// 必装
	add(einotools.BuildReadNotes(deps.Notes, p.OwnerID, p.Host, p.HunterID))
	add(einotools.BuildWriteNote(deps.Notes, p.OwnerID, p.Host, p.HunterID))
	add(einotools.BuildReadCredentials(deps.Credentials, p.Host))
	add(einotools.BuildWriteCredential(deps.Credentials, p.Host))
	add(einotools.BuildReadFindings(deps.Findings, p.OwnerType, p.OwnerID, p.Host))
	add(einotools.BuildWriteFinding(deps.Findings, p.OwnerType, p.OwnerID, p.HunterID, p.Host, p.FlowID))
	add(einotools.BuildUpdateFinding(deps.Findings))
	add(einotools.BuildReadLessons(deps.Lessons, p.Host))
	add(einotools.BuildWriteLesson(deps.Lessons, p.Host))

	// 可选（nil / 空 catalog 时跳过，与 skill.go 门控一致）
	if deps.Flows != nil {
		add(einotools.BuildReplayFlow(deps.Flows, p.OwnerType, p.OwnerID, p.HunterID))
	}
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
		return nil, fmt.Errorf("build tracker tools: %w", errors.Join(errs...))
	}
	return tools, nil
}

// BuildStrikerTools 装配 striker（active 突击手）的 eino 工具集。
//
// = tracker 工具集 + list_flows + view_flow（active 才注册，commander 已抓 internal 真实请求入字典，
// striker 据此查结构 + 凭证位置 → replay_flow 做 BAC）。striker 写 finding、不 spawn（无递归）。
func BuildStrikerTools(deps TrackerToolDeps, p TrackerToolParams) ([]tool.BaseTool, error) {
	tools, err := BuildTrackerTools(deps, p)
	if err != nil {
		return nil, err
	}
	if deps.Flows != nil {
		lf, err := einotools.BuildListFlows(deps.Flows, p.OwnerType, p.OwnerID, p.Host)
		if err != nil {
			return nil, fmt.Errorf("build striker tools: %w", err)
		}
		vf, err := einotools.BuildViewFlow(deps.Flows, p.OwnerType, p.OwnerID)
		if err != nil {
			return nil, fmt.Errorf("build striker tools: %w", err)
		}
		tools = append(tools, lf, vf)
	}
	return tools, nil
}
