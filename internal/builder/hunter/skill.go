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
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/grounding"
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
// 改 prompt 走 PR + review，与代码同路径管理（prompt-as-code 实践）。
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

// SystemPromptFor 导出 buildSystemPrompt，供 eino 迁移路径（internal/einoagent）复用同一套
// prompt-as-code 资产，避免 react / eino 双路径 prompt 漂移。
func SystemPromptFor(mode string, isParent bool) string {
	return buildSystemPrompt(mode, isParent)
}

// BuildUserPrompt 导出 buildUserPrompt，供 eino 迁移路径复用流量/finding/notes/lesson/索引
// 段的统一拼装（同上：单一真相源，防双路径漂移）。
func BuildUserPrompt(ctx context.Context, deps Deps, p skill.BuilderParams) string {
	return buildUserPrompt(ctx, deps, p)
}

// Deps hunter builder 的依赖注入。由 cmd/scanner/main.go 在启动时构造一份。
type Deps struct {
	Notes       notes.Store // 短期工作笔记（Redis；owner 内同 host 跨 task 共享）
	Findings    *finding.Store
	Lessons     *lesson.Store
	Flows       *flow.Store // 流量字典；nil 时不注册 list_flows/view_flow/replay_flow
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

	// 预算——按 mode 分流：passive 流量驱动 60 步够；active 站点扫描深挖需 300 步
	PassiveMaxSteps     int
	ActiveMaxSteps      int
	WatchdogSeconds     int
	InspectorEverySteps int
	MaxImagesInHistory  int // multimodal 历史保留图片数（来自 cfg.React.MaxImagesInHistory，0 时 runtime fallback 3）

	// HistoryCompactor 是 ReAct msgs 文本压缩器（防 context 爆）。
	// cmd/scanner 装配时拿 light_provider Generator 构造 *react.LLMHistoryCompactor 注入；
	// nil → 跳过文本压缩（图压缩 compressImages 不受影响）。
	HistoryCompactor react.HistoryCompactor

	// HistoryCompact 压缩超参（trigger_ratio / trailing_budget_ratio / cooldown_token_delta）。
	HistoryCompact react.HistoryCompactConfig

	// HistoryCompactTimeout 单次蒸馏调用硬超时；零值 runtime 内 30s 默认。
	HistoryCompactTimeout time.Duration

	// ContextWindows 是 provider name → context_window 映射（来自 cfg.Providers[name].ContextWindow）。
	// hunter 装配时按 p.LLM.Provider() 查表得当前 hunter 的 ctx_window，传 react.Config.ContextWindow。
	// 缺 key（含本不应发生的 nil）→ runtime 压缩自动跳过；该 provider 视为"未声明 ctx"安全降级。
	ContextWindows map[string]int

	// CoordSystems 是 provider name → grounding_coord_system 映射（仅 vision provider 有值）。
	// browser_use(action=click/input) 装配时按 p.LLM.Provider() 查表得当前 hunter 的坐标系；
	// 非 vision provider 路由进 click/input action 会因空 CoordSystem 在 ToRealPixels 报 err（防误用）。
	CoordSystems map[string]string

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

// doneBackoffState 是 commander done 退避计数器状态（闭包局部 per-commander，react.Run 单 goroutine 无 mutex）。
//
// 设计动机（done 焦虑修复）：之前一次 active e2e 实测 commander 被 PreDoneCheck 拒后连续重试 18 次 done，
// 每次重试都消耗 ~50K input tokens（含完整 ReAct history），单次 e2e 烧 0.5 元在空 done 上。
//
// 防御机制：computeDoneBackoff 给出指数退避（无→30s→2min→5min），冷却期内 done 静默拒不增加计数。
type doneBackoffState struct {
	consecutiveDenies int       // 连续被拒次数（striker 全 done 时归 0）
	lastDenyTime      time.Time // 上次 deny 时间戳，用于冷却期判定
}

// computeDoneBackoff 按连续拒次数返回应冷却的时长。
//
// 阈值设计：
//   - 1-2 次：不冷却（LLM 试探后真该走别的路径）
//   - 3-4 次：30s 冷却（明显焦虑）
//   - 5-6 次：2min 冷却（深度焦虑）
//   - 7+ 次：5min 冷却（直接放弃 done 路径）
//
// 实测 striker 典型 1-7 分钟完成，2min/5min 退避正好对齐 striker 工作周期。
func computeDoneBackoff(consecutiveDenies int) time.Duration {
	switch {
	case consecutiveDenies <= 2:
		return 0
	case consecutiveDenies <= 4:
		return 30 * time.Second
	case consecutiveDenies <= 6:
		return 2 * time.Minute
	default:
		return 5 * time.Minute
	}
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

		must(&common.ReadNotes{Store: deps.Notes, OwnerID: p.OwnerID, Host: p.Host, HunterID: p.HunterID})
		must(&common.WriteNote{Store: deps.Notes, OwnerID: p.OwnerID, Host: p.Host, HunterID: p.HunterID})
		// 凭证读写一对：read_credentials 拉本 host 已录身份；write_credential 把活凭证
		// （登录拿到的 cookie/token/csrf 任意位置 N 条）回写同 redis hash，供同 owner 下
		// 其他 hunter 共享。active / passive 一律注册——active 撤回旧"brief 嵌 cookie 文本"
		// 协议，统一走 redis credentials key 作为凭证唯一传递通道。
		must(&common.ReadCredentials{Provider: deps.Credentials, Host: p.Host})
		must(&common.WriteCredential{Provider: deps.Credentials, Host: p.Host})
		must(&common.ReadFindings{Store: deps.Findings, OwnerType: p.OwnerType, OwnerID: p.OwnerID, Host: p.Host})
		// commander（active+无父 task）永远不写/不改 finding——铁律"自挖必转 spawn striker"
		// 硬阻断：不注册工具 → LLM 看不到 schema → 根本调不到（强于纯 prompt 约束）
		isCommander := p.Mode == "active" && p.CommanderID == ""
		if !isCommander {
			must(&common.WriteFinding{
				Store:     deps.Findings,
				OwnerType: p.OwnerType,
				OwnerID:   p.OwnerID,
				HunterID:  p.HunterID,
				Host:      p.Host,
				FlowID:    p.FlowID,
			})
			must(&common.UpdateFinding{Store: deps.Findings})
		}
		must(&common.ReadLessons{Store: deps.Lessons, Host: p.Host})
		must(&common.WriteLesson{Store: deps.Lessons, Host: p.Host})

		// subtask swarm：**仅 commander** 注册 spawn_striker / list_strikers。
		// striker（CommanderID 非空）不注册防递归（max_depth=1）。
		// commander Done 装 PreDoneCheck 拒绝"striker 未完先 done"。
		// passive 不开 spawn 的原因：passive 60 步预算 + striker 常 100+ 步 → commander 来不及等 striker 完
		//   就会 max_steps 退出（H3 修过孤儿 goroutine，但仍违反"commander 等 striker"语义）。
		//   passive 场景"1 流量挖多类型"应由流量分发器拆多个 active 任务，不该 swarm。
		// 流量字典工具（所有角色都注册 replay_flow——重发改字段，cookie/CSRF/session 自动继承）。
		// list_flows/view_flow 仅 active 角色注册——B1 后 active 容器内 browser-svc.py 内建 CDP
		//   Network observer 把 chromium 真实认证请求写进 active owner 的 http_flow（source=internal），
		//   commander/striker 据此查真实请求结构 + 凭证位置 → 转 replay_flow 做水平/垂直越权（BAC）测试。
		//   tracker（passive）单 flow focused，只 replay 不 list/view 避免诱导跑偏。
		// replay_flow v34+ 直连目标（删 proxy 路径），重发不再回字典；改 modifications 字段 + 其余自动继承。
		// 凭证共享另走 read_credentials/write_credential（见 system_prompt_shared.md「凭证共享协议」）。
		if deps.Flows != nil {
			must(&common.ReplayFlow{
				Store:     deps.Flows,
				OwnerType: p.OwnerType,
				OwnerID:   p.OwnerID,
				HunterID:  p.HunterID,
			})
			if p.Mode == "active" {
				must(&common.ListFlows{
					Store:     deps.Flows,
					OwnerType: p.OwnerType,
					OwnerID:   p.OwnerID,
					Host:      p.Host,
				})
				must(&common.ViewFlow{
					Store:     deps.Flows,
					OwnerType: p.OwnerType,
					OwnerID:   p.OwnerID,
				})
			}
		}

		var spawnerRegistry *subtask.Registry
		if p.Mode == "active" && p.CommanderID == "" && deps.SpawnerFactory != nil {
			spawner, registry, err := deps.SpawnerFactory(ctx, p)
			if err != nil {
				return react.Config{}, fmt.Errorf("subtask spawner factory: %w", err)
			}
			spawnerRegistry = registry
			must(common.SpawnStriker{Spawner: spawner})
			must(common.ListStrikers{Registry: registry})
		}

		var onNoToolCall func(context.Context) (bool, string, error)
		if spawnerRegistry != nil {
			// 共享快照：done 闸门（PreDoneCheck）与 no_tool_call 闸门（OnNoToolCall）
			// 都靠它判断"还有没有 running striker"——两道闸门同一语义，避免逻辑漂移。
			snapshotRunning := func() []string {
				snaps := spawnerRegistry.Snapshot()
				var running []string
				now := time.Now()
				for _, s := range snaps {
					if s.Status != subtask.StatusRunning {
						continue
					}
					elapsed := int(now.Sub(s.SpawnedAt).Seconds())
					short := s.HunterID
					if len(short) > 8 {
						short = short[:8]
					}
					running = append(running, fmt.Sprintf("%s(%ds)", short, elapsed))
				}
				return running
			}

			// done 焦虑修复：commander LLM 被拦后倾向反复改 reason 重试 done（实测一次 e2e 烧 18 次空 done = 0.5 元 token）。
			// 双层防御——
			//   A. 退避计数器（cooldown）：连续被拒 N 次后强制冷却 30s→2min→5min 指数退避，期间 done 直接静默拒
			//   B. 错误消息删"过段时间再试 done"诱导句，改成命令式"不要再 done，去做 X/Y/Z"
			// 状态闭包局部（每 commander 独立），react.Run 单 goroutine 无需 mutex。
			backoffSt := &doneBackoffState{}
			must(common.Done{
				Sandbox:  p.Sandbox,
				HunterID: p.HunterID,
				PreDoneCheck: func(_ context.Context) error {
					running := snapshotRunning()
					if len(running) == 0 {
						backoffSt.consecutiveDenies = 0 // reset cooldown
						return nil
					}
					// A：冷却期内静默拒（不增加计数，避免冷却中重试被算新拒绝形成永久冷却）
					if backoff := computeDoneBackoff(backoffSt.consecutiveDenies); backoff > 0 {
						if remaining := backoff - time.Since(backoffSt.lastDenyTime); remaining > 0 {
							return fmt.Errorf("done 冷却中（连续 %d 次被拒，下次允许还剩 %s）。"+
								"**不要再调 done，也不要 polling list_strikers**——两者都白烧推理；strikers 全 done 后 PreDoneCheck 自动放行你的 done。"+
								"现在去做有产出的事：read_findings 看 striker 黑板新 finding / 基于 finding 调 spawn_striker 派新 chaining striker / write_lesson 沉淀可复用攻击模式。"+
								"**你是指挥官，不亲自挖洞**（撞证据走 evidence handoff 协议 spawn striker）。",
								backoffSt.consecutiveDenies, remaining.Truncate(time.Second))
						}
					}
					backoffSt.consecutiveDenies++
					backoffSt.lastDenyTime = time.Now()
					// B：删"过段时间再试 done"诱导，改命令式 + 明确"协调者不亲自挖"防 commander 自挖
					return fmt.Errorf("仍有 %d 个 running striker: %s。**不要再调 done，也不要 polling list_strikers**——重复 done 会进入冷却（30s→2min→5min 指数退避），polling 同样白烧推理；strikers 全 done 后 PreDoneCheck 自动放行你的 done。"+
						"现在去做有产出的事：(a) read_findings 看 striker 黑板新 finding；"+
						"(b) 基于 finding 调 spawn_striker 派**新** striker 挖 chaining 链路（不同攻面 / SQLi→Auth Bypass 等组合）；"+
						"(c) write_lesson 沉淀可复用攻击模式（不是任务总结）。"+
						"**你是指挥官，永远不亲自挖洞 / 不亲自 write_finding**——撞证据走 evidence handoff 协议 spawn striker。",
						len(running), strings.Join(running, ", "))
				},
			})

			// no_tool_call 闸门：commander 返回零 tool call（想沉默收口）但仍有 running striker 时拦下。
			// 与 PreDoneCheck 同源（snapshotRunning），让"沉默收口"和"显式 done"受同一约束——
			// 这样 commander 不再靠 polling list_strikers 防 no_tool_call 早停（#5 已删该诱导）。
			// 全 done 才放行收口；keep-alive 受 react Budget.MaxSteps 兜底，不会无限循环。
			onNoToolCall = func(_ context.Context) (bool, string, error) {
				running := snapshotRunning()
				if len(running) == 0 {
					return false, "", nil // 无 running striker → 放行收口
				}
				return true, fmt.Sprintf("仍有 %d 个 striker 在跑: %s。**你现在不能收口**（no_tool_call 已被闸门拦下，等同 done 被拦）。"+
					"strikers 全 done 后你再收口；现在去做有产出的事：(a) read_findings 看 striker 黑板新 finding；"+
					"(b) 基于 finding 调 spawn_striker 派**新** striker 挖 chaining 链路（不同攻面 / SQLi→Auth Bypass 等组合）；"+
					"(c) write_lesson 沉淀可复用攻击模式。"+
					"**你是指挥官，永远不亲自挖洞 / 不亲自 write_finding**。",
					len(running), strings.Join(running, ", ")), nil
			}
		} else {
			must(common.Done{Sandbox: p.Sandbox, HunterID: p.HunterID})
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

		// run_command + page_* 系列工具运行时绑定到本次 agent run 的 sandbox 容器。
		// p.Sandbox 由 cmd/scanner handlePassive/handleActive 调 Launcher.Spawn(runID) 后注入；
		// nil 时跳过注册，避免 LLM 调到没 sandbox 的工具（如 dev/test 场景）。
		//
		// browser_use 单工具（vision-first）：
		//   - action: open / click / input / wait / eval / extract / source 一处 switch 分流
		//   - 状态变化 action（open/click/input/wait）wrapper 自动附截图
		//   - 共用同一 *RunCommand 实例确保 Sandbox/HunterID 一致；低频 browser 子命令仍走 run_command 兜底
		// 坐标系按当前 hunter 路由到的 provider 配置（grounding.CoordSystem）；
		// 非 vision provider 走到 click/input action 会因 CoordSystem="" 在 ToRealPixels 报 err（防误用）。
		if p.Sandbox != nil {
			rc := &external.RunCommand{
				Sandbox:           p.Sandbox,
				HunterID:          p.HunterID, // sandbox-server 按此切 cwd / OUTPUT_DIR（PR2 subtask 隔离）
				MaxTimeoutSeconds: deps.StepToolTimeoutSeconds,
				TailBytes:         deps.SandboxCfg.RunTailBytes,
			}
			must(rc)
			// browser_use 只给 active——passive 单流量验证走 curl/replay_flow（HTTP 层足够）；
			// 渲染类验证（DOM-XSS）本就归 active，不在 passive 注册浏览器工具。
			if p.Mode == "active" {
				coordSys := grounding.CoordSystem("")
				if p.LLM != nil && deps.CoordSystems != nil {
					coordSys = grounding.CoordSystem(deps.CoordSystems[p.LLM.Provider()])
				}
				// 视口尺寸：从 deps 透传到 click/input action 用于 grounding 换算；
				// 与 sandbox.DockerLauncher 注入 docker run -e 的值同源——SandboxConfig 单值多处共享。
				vpW := deps.SandboxCfg.ViewportWidth
				vpH := deps.SandboxCfg.ViewportHeight
				must(&external.BrowserUse{Run: rc, CoordSystem: coordSys, ViewportW: vpW, ViewportH: vpH})
			}
		}

		if len(regErrs) > 0 {
			return react.Config{}, fmt.Errorf("hunter register tools: %w", errors.Join(regErrs...))
		}

		reg.Use(
			interceptor.Observe(),
			interceptor.Record(deps.ToolInvocations, p.HunterID, p.OwnerType, p.OwnerID),
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

		// 按当前 hunter 路由到的 provider 查 ctx_window；缺 key 走 0 → runtime 压缩自动跳过。
		ctxWindow := 0
		if deps.ContextWindows != nil && p.LLM != nil {
			ctxWindow = deps.ContextWindows[p.LLM.Provider()]
		}

		return react.Config{
			LLM:                   p.LLM,
			Actions:               reg,
			Budget:                react.Budget{MaxSteps: maxSteps, WatchdogSeconds: watchdog},
			SystemPrompt:          buildSystemPrompt(p.Mode, p.CommanderID == ""),
			UserPrompt:            userPrompt,
			OnNoToolCall:          onNoToolCall, // commander 拦过早收口；非 commander 为 nil（维持旧行为）
			Inspector:             p.Inspector,
			InspectorEverySteps:   deps.InspectorEverySteps,
			MaxImagesInHistory:    deps.MaxImagesInHistory,
			HistoryCompactor:      deps.HistoryCompactor,
			ContextWindow:         ctxWindow,
			HistoryCompact:        deps.HistoryCompact,
			HistoryCompactTimeout: deps.HistoryCompactTimeout,
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
