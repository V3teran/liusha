package einoagent

import (
	"errors"
	"fmt"

	"github.com/cloudwego/eino/components/tool"

	"github.com/V3teran/liusha/internal/einotools"
	"github.com/V3teran/liusha/internal/flow"
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
)

// TrackerToolDeps 是装配 tracker eino 工具集所需的依赖。
// 由 cmd/scanner composition root 注入（与旧 hunter.Deps 同源 store）。
type TrackerToolDeps struct {
	Findings    FindingStore
	Notes       notes.Store
	Lessons     LessonStore
	Credentials CredentialStore

	// 可选：用具体指针类型，nil 时不注册对应工具（nil 门控语义与 hunter/skill.go 一致，
	// 避免 typed-nil 装箱进接口后 != nil 的陷阱）。
	Flows         *flow.Store
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
