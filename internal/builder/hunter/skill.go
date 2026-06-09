// Package hunter 提供 hunter agent 的 prompt 资产（system prompt 分段 + user prompt 拼装）+ Deps。
//
// eino 路径（internal/einoagent + cmd/scanner/handler_*_eino）复用这里的：
//   - SystemPromptFor / SharedSystemPrompt：system prompt 分段（shared + 角色段）
//   - BuildUserPrompt：流量 / finding / notes / lesson / 工具索引 段的统一拼装
//   - Deps：prompt 拼装所需的 store / loader / manifest 依赖
//
// react 退路已删除，本包只剩 prompt-as-code 资产；agent 编排装配在 internal/einoagent。
package hunter

import (
	"context"
	_ "embed"

	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/lesson"
	"github.com/V3teran/liusha/internal/notes"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/tools/manifest"
)

// hunter agent system prompt 按 (mode, 是否主代理) 拆四段编译期嵌入：
//   - shared：通用规则（角色 / 写 finding 铁律 / mode-invariant 反模式）
//   - tracker（passive 单 agent）：流量驱动入口
//   - commander（active 主代理）：brief 驱动入口
//   - striker（active 子代理）：接 brief 深挖单点
//
// 改 prompt 走 PR + review，与代码同路径管理（prompt-as-code 实践）。
// 注：文件名沿用旧角色名（commander/striker/tracker），命名统一在后续阶段处理。
//
//go:embed system_prompt_shared.md
var hunterSystemPromptShared string

//go:embed system_prompt_tracker.md
var hunterSystemPromptTracker string

//go:embed system_prompt_commander.md
var hunterSystemPromptCommander string

//go:embed system_prompt_striker.md
var hunterSystemPromptStriker string

// buildSystemPrompt 按 (mode, isParent) 选段拼接 shared + 角色段。
//   - mode=="active" && isParent  → commander（主代理）
//   - mode=="active" && !isParent → striker（子代理，专注 brief 深挖单点）
//   - 其它（passive / 未知）       → tracker（独立挖单流量）
func buildSystemPrompt(mode string, isParent bool) string {
	var addendum string
	switch {
	case mode == "active" && isParent:
		addendum = hunterSystemPromptCommander
	case mode == "active":
		addendum = hunterSystemPromptStriker
	default:
		addendum = hunterSystemPromptTracker
	}
	return hunterSystemPromptShared + "\n" + addendum
}

// SystemPromptFor 导出 buildSystemPrompt，供 eino 路径复用同一套 prompt-as-code 资产。
func SystemPromptFor(mode string, isParent bool) string {
	return buildSystemPrompt(mode, isParent)
}

// SharedSystemPrompt 单独导出 shared 段（不含任何角色 addendum）。
// deep 路径的主代理用此 + 角色 md 的 deep-native 编排 charter 组装（不复用 commander 段，
// 那是为旧 spawn 机制写的）。
func SharedSystemPrompt() string {
	return hunterSystemPromptShared
}

// BuildUserPrompt 导出 buildUserPrompt，供 eino 路径复用流量/finding/notes/lesson/索引段的统一拼装。
func BuildUserPrompt(ctx context.Context, deps Deps, p skill.BuilderParams) string {
	return buildUserPrompt(ctx, deps, p)
}

// Deps 是 prompt 拼装 + eino 工具装配的依赖注入（由 cmd/scanner/main.go 构造一份）。
type Deps struct {
	Notes           notes.Store
	Findings        *finding.Store
	Lessons         *lesson.Store
	Credentials     credential.Provider
	ToolInvocations *toolinvocation.Store // tool_invocation 遥测落库
	ToolingLoader   *skill.Loader         // read_tooling_skill；nil 不注册
	ToolsManifest   *manifest.Manifest    // user prompt 的工具索引段（tooling_catalog）
	VulnLoader      *skill.Loader         // read_vuln_skill + 漏洞挖掘指南索引段；nil 不注入

	UserPromptBodyLimit int // 请求/响应 body 单段截断字节数；≤0 → 8192
	FindingsLimit       int // user prompt 该 host 已有 finding 段显示条数；≤0 → 100
	LessonsLimit        int // user prompt lesson + hint 段共用上限；≤0 → 100
}
