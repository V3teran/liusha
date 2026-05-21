// Package hunter 是当前唯一的 ReAct skill builder：
// scanner 拉到 flow 后调 NewBuilder(deps)(ctx, params) 拿 react.Config 跑 react.Run。
//
// hunter 小队架构：tracker / commander / striker 都用本 builder 装配 — 接到一条流量
// （request + response + 凭证 + 已有 finding + hint）后，自由组合下列工具挖漏洞：
//
//	必装（11 个）:
//	  read_notes / write_note                   — task 内中间状态
//	  read_credentials                          — 拿该 host 凭证
//	  read_findings / write_finding / update_finding — finding 读写
//	  read_relations / write_relation           — finding 依赖图
//	  read_lessons / write_lesson               — 跨 owner 经验
//	  done                                      — 收尾
//
//	可选（3 个，Deps.*Loader / params.Sandbox nil 时跳过）:
//	  read_tooling_skill                        — 拉 skills/tooling/<name>/SKILL.md
//	  read_vuln_skill                           — 拉 skills/vuln/<name>/SKILL.md
//	  run_command                               — 沙箱跑外部 CLI（含 browser-use 浏览器交互）
package hunter

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/lesson"
	"github.com/V3teran/liusha/internal/notes"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/subtask"
	"github.com/V3teran/liusha/internal/toolinvocation"
	toolfx "github.com/V3teran/liusha/internal/toolruntime"
	"github.com/V3teran/liusha/internal/toolruntime/interceptor"
	"github.com/V3teran/liusha/internal/tools/common"
	"github.com/V3teran/liusha/internal/tools/external"
	"github.com/V3teran/liusha/internal/tools/manifest"
)

// hunter agent system prompt 按 (mode, 是否 commander) 拆四段编译期嵌入：
//   - shared：通用规则（角色 / 写 finding 铁律 / mode-invariant 反模式）
//   - tracker（passive 单 agent / 侦察兵）：流量驱动入口 + 401→read_credentials 反模式
//   - commander（指挥官）：brief 驱动入口 + commander 与 striker 职责分工 + spawn 优先
//   - striker（突击手）：接 brief 深挖单点 + 不再 spawn + evidence handoff
//
// 拆段避免角色错位（commander 看到"挖单点 brief 之外不要碰"会矛盾；
// striker 看到"spawn striker"会因没注册工具而困惑）。
// 改 prompt 走 PR + review，与代码同路径管理（Strix 风格）。
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
//   - mode=="active" && isParent  → commander（指挥官，spawn 优先）
//   - mode=="active" && !isParent → striker（突击手，专注 brief 深挖单点）
//   - 其它（passive / 未知）       → tracker（侦察兵，独立挖单流量）
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

// Deps hunter builder 的依赖注入。由 cmd/scanner/main.go 在启动时构造一份。
type Deps struct {
	Notes       notes.Store // 短期工作笔记（Redis；owner 内同 host 跨 task 共享）
	Findings    *finding.Store
	Lessons     *lesson.Store
	Credentials credential.Provider

	// ToolInvocations 为 Record interceptor 提供 PG 持久化能力——每次 Execute
	// 在 enter/exit 边界写一行 tool_invocation。nil 时跳过 telemetry。
	ToolInvocations *toolinvocation.Store

	// ToolingLoader root 指向 skills/tooling/，给 read_tooling_skill 工具用（按需读 SKILL.md 详细手册）。
	// nil 时不注册 read_tooling_skill 工具（向后兼容）。
	// 注意：工具索引段（Tier 1）不再扫 SKILL.md frontmatter 拼，而是读 ToolsManifest——
	// 这样"工具是否存在"与"工具是否有 SKILL 详细手册"解耦：删 SKILL 不等于工具消失。
	ToolingLoader *skill.Loader

	// ToolsManifest 是 deployments/tool-images/pentools/tools.yaml 解析后的清单——
	// 与 Dockerfile 装的 binary 严格对应，hunter 用它渲染 SystemPrompt 的 tooling_catalog 段。
	// nil 时不注入索引段（LLM 看不到沙箱有哪些工具，应在 scanner 启动期 fail-fast）。
	ToolsManifest *manifest.Manifest

	// VulnLoader root 指向 skills/vuln/，给 read_vuln_skill 用 +
	// buildUserPrompt 启动期扫 frontmatter 注入"漏洞挖掘指南索引"段。
	// 与 ToolingLoader 同模式：常驻极简索引（无 category 分组），详情按需
	// read_vuln_skill 拉。nil 时不注入索引段、不注册 read_vuln_skill 工具。
	VulnLoader *skill.Loader

	// run_command 工具运行时配置（实际 sandbox.Client 由 p.Sandbox 传入，per agent run）。
	// 这里只放静态配置，不持有 client：client 生命周期 = agent run 生命周期，
	// 由 cmd/scanner handlePassive/handleActive 通过 Launcher.Spawn/Destroy 管理。
	SandboxCfg config.SandboxConfig

	// StepToolTimeoutSeconds 单次 tool Execute 兜底超时（秒）；0 = 不加 deadline。
	StepToolTimeoutSeconds int

	// 预算——按 mode 分流：passive 流量驱动 60 步够；active 站点扫描深挖需 300 步（strix 同款）
	PassiveMaxSteps    int
	ActiveMaxSteps     int
	WatchdogSeconds    int
	InspectorEverySteps int

	// SpawnerFactory 为 commander 装配 subtask.Spawner + Registry（subtask swarm）。
	// 由 cmd/scanner 注入：闭包捕获 router/stores/calls/pricing 等所有装配striker 所需依赖。
	// nil 时commander 不注册 spawn_striker / list_strikers 工具（向后兼容 / 单测场景）。
	// max_children 闸值在 spawner 内部持有，闸触发的 wrapped error 已含数字提示。
	SpawnerFactory func(commanderCtx context.Context, p skill.BuilderParams) (subtask.Spawner, *subtask.Registry, error)

	// Prompt 拼装预算
	UserPromptBodyLimit int // 请求/响应 body 单段截断字节数；≤0 → 8192
	FindingsLimit       int // user prompt 该 host 已有 finding 段显示条数；≤0 → 100
	LessonsLimit        int // user prompt lesson + hint 段共用上限；≤0 → 100
}

// NewBuilder 构造 hunter SkillBuilder 闭包。scanner 启动时调用一次。
func NewBuilder(deps Deps) skill.Builder {
	return func(ctx context.Context, p skill.BuilderParams) (react.Config, error) {
		reg := toolfx.NewRegistry()

		// 累积所有 register error 一次性返回——之前 `_ = reg.Register(...)` 静默
		// 吞掉 duplicate name / schema 验证错，agent 端只表现为"工具调不出"难排查。
		var regErrs []error
		must := func(act toolfx.Action) {
			if err := reg.Register(act); err != nil {
				regErrs = append(regErrs, fmt.Errorf("register %s: %w", act.Name(), err))
			}
		}

		must(&common.ReadNotes{Store: deps.Notes, OwnerID: p.OwnerID, Host: p.Host, TaskID: p.TaskID})
		must(&common.WriteNote{Store: deps.Notes, OwnerID: p.OwnerID, Host: p.Host, TaskID: p.TaskID})
		// read_credentials 仅 passive 模式注册：passive 流量已绑 host，从 redis credential
		// store 拉对应身份重放/重试天经地义。active 模式账号密码走自然语言 brief，不复用此机制。
		if p.Mode != "active" {
			must(&common.ReadCredentials{Provider: deps.Credentials, Host: p.Host})
		}
		must(&common.ReadFindings{Store: deps.Findings, OwnerType: p.OwnerType, OwnerID: p.OwnerID, Host: p.Host})
		must(&common.WriteFinding{
			Store:     deps.Findings,
			OwnerType: p.OwnerType,
			OwnerID:   p.OwnerID,
			TaskID:    p.TaskID,
			Host:      p.Host,
			FlowID:    p.FlowID,
		})
		must(&common.UpdateFinding{Store: deps.Findings})
		must(&common.ReadRelations{Store: deps.Findings, OwnerID: p.OwnerID})
		must(&common.WriteRelation{Store: deps.Findings})
		must(&common.ReadLessons{Store: deps.Lessons, Host: p.Host})
		must(&common.WriteLesson{Store: deps.Lessons, Host: p.Host})

		// subtask swarm：**仅 commander** 注册 spawn_striker / list_strikers。
		// striker（CommanderTaskID 非空）不注册防递归（max_depth=1）。
		// commander Done 装 PreDoneCheck 拒绝"striker 未完先 done"。
		// passive 不开 spawn 的原因：passive 60 步预算 + striker 常 100+ 步 → commander 来不及等 striker 完
		//   就会 max_steps 退出（H3 修过孤儿 goroutine，但仍违反"commander 等 striker"语义）。
		//   passive 场景"1 流量挖多类型"应由流量分发器拆多个 active 任务，不该 swarm。
		var spawnerRegistry *subtask.Registry
		if p.Mode == "active" && p.CommanderTaskID == "" && deps.SpawnerFactory != nil {
			spawner, registry, err := deps.SpawnerFactory(ctx, p)
			if err != nil {
				return react.Config{}, fmt.Errorf("subtask spawner factory: %w", err)
			}
			spawnerRegistry = registry
			must(common.SpawnStriker{Spawner: spawner})
			must(common.ListStrikers{Registry: registry})
		}

		if spawnerRegistry != nil {
			must(common.Done{
				Sandbox: p.Sandbox,
				TaskID:  p.TaskID,
				PreDoneCheck: func(_ context.Context) error {
					// 不诱导 polling：错误消息**自含** running striker摘要（taskID 前缀 + 已跑秒数），
					// LLM 看 error 即得到 list_strikers 该给的信息；明确建议挖新链路 / read_findings /
					// 写 lesson，过段时间再试 done，避免空转 polling 烧 token。
					snaps := spawnerRegistry.Snapshot()
					var running []string
					now := time.Now()
					for _, s := range snaps {
						if s.Status != subtask.StatusRunning {
							continue
						}
						elapsed := int(now.Sub(s.SpawnedAt).Seconds())
						short := s.TaskID
						if len(short) > 8 {
							short = short[:8]
						}
						running = append(running, fmt.Sprintf("%s(%ds)", short, elapsed))
					}
					if len(running) == 0 {
						return nil
					}
					return fmt.Errorf("仍有 %d 个 running striker: %s。**不要调 list_strikers polling**——striker 写的 finding 已通过共享黑板冒给你（read_findings 看），现在去：(a) 用 striker 已挖出的发现作引导挖新链路；(b) 完善 finding/写 lesson；(c) 过段时间再试 done。strikers 全完了再调 done 即可",
						len(running), strings.Join(running, ", "))
				},
			})
		} else {
			must(common.Done{Sandbox: p.Sandbox, TaskID: p.TaskID})
		}

		// Progressive Disclosure Tier 2：LLM 看 user prompt 工具索引选中工具后
		// 调本工具拿完整 SKILL.md。Loader 由 cmd/scanner 单独装配（root=skills/tooling）。
		// 仅当 catalog 非空时才注册 read_tooling_skill——
		// 工具不暴露给 LLM，避免它凭"行业常识"猜不存在的 name 浪费 round-trip。
		if deps.ToolingLoader != nil && len(deps.ToolingLoader.List()) > 0 {
			must(&common.ReadToolingSkill{Loader: deps.ToolingLoader})
		}

		// Progressive Disclosure Tier 2（漏洞挖掘指南）：LLM 按 user_prompt 注入的"漏洞类型索引"
		// 判完流量方向后，调本工具拿对应漏洞类型完整 SKILL.md。
		// Loader root=skills/vuln，由 cmd/scanner 单独装配。
		// 仅当 catalog 非空时才注册 read_vuln_skill——同上 P4 教训。
		if deps.VulnLoader != nil && len(deps.VulnLoader.List()) > 0 {
			must(&common.ReadVulnSkill{Loader: deps.VulnLoader})
		}

		// run_command 工具运行时绑定到本次 agent run 的 sandbox 容器。
		// p.Sandbox 由 cmd/scanner handlePassive/handleActive 调 Launcher.Spawn(runID) 后注入；
		// nil 时跳过注册，避免 LLM 调到没 sandbox 的工具（如 dev/test 场景）。
		if p.Sandbox != nil {
			must(&external.RunCommand{
				Sandbox:           p.Sandbox,
				TaskID:            p.TaskID, // sandbox-server 按此切 cwd / OUTPUT_DIR（PR2 subtask 隔离）
				MaxTimeoutSeconds: deps.StepToolTimeoutSeconds,
				TailBytes:         deps.SandboxCfg.RunTailBytes,
			})
		}

		if len(regErrs) > 0 {
			return react.Config{}, fmt.Errorf("hunter register tools: %w", errors.Join(regErrs...))
		}

		reg.Use(
			interceptor.Observe(),
			interceptor.Record(deps.ToolInvocations, p.TaskID, p.OwnerType, p.OwnerID),
			interceptor.Timeout(deps.StepToolTimeoutSeconds),
		)

		userPrompt := buildUserPrompt(ctx, deps, p)

		// 按 mode 选 max_steps——active 站点扫描需 300 步深挖；passive 单流量 60 步足
		maxSteps := deps.PassiveMaxSteps
		if p.Mode == "active" {
			maxSteps = deps.ActiveMaxSteps
		}
		if maxSteps <= 0 {
			maxSteps = 30
		}
		watchdog := deps.WatchdogSeconds
		if watchdog <= 0 {
			watchdog = 60
		}

		return react.Config{
			LLM:                p.LLM,
			Actions:            reg,
			Budget:             react.Budget{MaxSteps: maxSteps, WatchdogSeconds: watchdog},
			SystemPrompt:       buildSystemPrompt(p.Mode, p.CommanderTaskID == ""),
			UserPrompt:         userPrompt,
			Inspector:           p.Inspector,
			InspectorEverySteps: deps.InspectorEverySteps,
		}, nil
	}
}

// buildUserPrompt 拼接 hunter agent 的第一条 user message。
//
// 两种入口形态共用段 3 起的"已有 finding / 笔记板 / lesson+hint / 工具索引 / 漏洞指南索引"：
//   - passive: 段 1/2 = 流量请求 + 流量响应（raw HTTP/1.1）
//   - active:  段 1   = 站点任务（brief 自然语言整段）
//
// 不在 user prompt 重复"行动指令"——agent 目标 / 工作流 / 反模式都在
// hunter system prompt（system_prompt_{shared,passive,active}.md）里，
// active 模式 LLM 自决怎么爬怎么测。
