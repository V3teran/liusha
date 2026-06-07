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

// toolSet 选 hunter 三角色（tracker/striker/commander）工具集的差异维度。
type toolSet struct {
	writeFinding bool          // tracker/striker 写 finding；commander 不写（铁律硬阻断）
	listView     bool          // striker/commander 注册 list/view_flow；tracker 不注册
	spawn        tool.BaseTool // commander 注册 spawn_striker；其余 nil
}

// buildHunterTools 是三角色共享的工具装配核心，按 toolSet 选差异工具。
func buildHunterTools(deps TrackerToolDeps, p TrackerToolParams, set toolSet) ([]tool.BaseTool, error) {
	var tools []tool.BaseTool
	var errs []error
	add := func(bt tool.BaseTool, err error) {
		if err != nil {
			errs = append(errs, err)
			return
		}
		tools = append(tools, bt)
	}

	// 公共：notes / credentials / read_findings / lessons
	add(einotools.BuildReadNotes(deps.Notes, p.OwnerID, p.Host, p.HunterID))
	add(einotools.BuildWriteNote(deps.Notes, p.OwnerID, p.Host, p.HunterID))
	add(einotools.BuildReadCredentials(deps.Credentials, p.Host))
	add(einotools.BuildWriteCredential(deps.Credentials, p.Host))
	add(einotools.BuildReadFindings(deps.Findings, p.OwnerType, p.OwnerID, p.Host))
	if set.writeFinding {
		add(einotools.BuildWriteFinding(deps.Findings, p.OwnerType, p.OwnerID, p.HunterID, p.Host, p.FlowID))
		add(einotools.BuildUpdateFinding(deps.Findings))
	}
	add(einotools.BuildReadLessons(deps.Lessons, p.Host))
	add(einotools.BuildWriteLesson(deps.Lessons, p.Host))

	// 流量字典：replay 三角色都有；list/view 仅 active（striker/commander）
	if deps.Flows != nil {
		add(einotools.BuildReplayFlow(deps.Flows, p.OwnerType, p.OwnerID, p.HunterID))
		if set.listView {
			add(einotools.BuildListFlows(deps.Flows, p.OwnerType, p.OwnerID, p.Host))
			add(einotools.BuildViewFlow(deps.Flows, p.OwnerType, p.OwnerID))
		}
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

	// commander 独有：spawn_striker（由 caller 构造好传入）
	if set.spawn != nil {
		tools = append(tools, set.spawn)
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("build hunter tools: %w", errors.Join(errs...))
	}
	return tools, nil
}

// BuildTrackerTools 装配 tracker（passive 单 agent）：写 finding，无 list/view，无 spawn。
func BuildTrackerTools(deps TrackerToolDeps, p TrackerToolParams) ([]tool.BaseTool, error) {
	return buildHunterTools(deps, p, toolSet{writeFinding: true})
}

// BuildStrikerTools 装配 striker（active 突击手）：写 finding + list/view_flow，不 spawn（无递归）。
func BuildStrikerTools(deps TrackerToolDeps, p TrackerToolParams) ([]tool.BaseTool, error) {
	return buildHunterTools(deps, p, toolSet{writeFinding: true, listView: true})
}

// BuildCommanderTools 装配 commander（active 指挥官）：**不注册 write/update_finding**（铁律硬阻断
// "自挖必转 spawn"）+ list/view_flow + spawn_striker（由 caller 用 BuildSpawnStriker 构造传入）。
func BuildCommanderTools(deps TrackerToolDeps, p TrackerToolParams, spawnStriker tool.BaseTool) ([]tool.BaseTool, error) {
	return buildHunterTools(deps, p, toolSet{listView: true, spawn: spawnStriker})
}
