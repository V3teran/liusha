// Package sqli 是 SQLi 子 ReAct 的 SkillBuilder（agentic 路线）。
//
// 与 vuln/bac 平级：复用 probe.Factory 的 3 个探针（fetch_credentials / run_replay /
// check_heuristics；不含 compute_similarity——SQLi 不靠响应差分判定）+ 公共的 RunCommand
// 容器化沙箱执行器。注入点提取已下线——子 ReAct LLM 直接看 user prompt 里的 flow 详情
// （headers + body）自识别候选注入点，无需代码层助手工具。
//
// SKILL 加载（双层）：
//   - 主流程 SKILL：vuln/web/sqli（决策树：何时调 sqlmap / curl / python3 / 何时写 finding）
//   - 工具手册 SKILL（按需注入）：tooling/sqlmap、tooling/curl、tooling/python3、tooling/sh
//     LLM 看工具手册学 CLI 用法，自己拼完整命令喂 RunCommand 跑容器，自读 stdout 决策。
package sqli

import (
	"context"
	"fmt"
	"strings"
	"time"

	vuln "github.com/V3teran/liusha/internal/builder/vuln"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/lesson"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/replay"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/toolruntime"
	"github.com/V3teran/liusha/internal/toolruntime/done_validator"
	"github.com/V3teran/liusha/internal/toolruntime/middleware"
	"github.com/V3teran/liusha/internal/tools/common"
	"github.com/V3teran/liusha/internal/tools/external"
	"github.com/V3teran/liusha/internal/tools/probe"
	"github.com/V3teran/liusha/internal/tools/runners"
)

// SubBuilderDeps SQLi SkillBuilder 的依赖注入；与 BAC 完全一致的字段集合，
// 让 cmd/scanner/main.go 装配两个 builder 时可复用同一份 deps 容器。
type SubBuilderDeps struct {
	Engagements *engagement.Store
	Findings    *finding.Store
	Lessons     *lesson.Store
	Credentials credential.Provider
	Flows       *flow.Store
	Replay      *replay.Engine
	SkillLoader *skill.Loader

	// 重型工具依赖（docker 不可用时整组传 nil，run_command 不注册，
	// 其余 SKILL 流程不受影响——但 LLM 失去容器化执行能力，多数 SQLi 验证将无法完成）。
	DockerRunner  *runners.DockerRunner // nil → 不注册 run_command
	PentoolsImage string                // 默认 liusha/pentools:1.0.0（external.RunCommand 内置兜底）
	ScanNetwork   string                // 默认空（docker bridge）

	// SandboxCfg 是 RunCommand 的运行时参数（min/max/default timeout / mem / cpu / tail）；
	// 由 cmd/scanner 从 cfg.Sandbox 注入，零值字段由 RunCommand 内部 fallback 兜底。
	SandboxCfg config.SandboxConfig

	// ProbeCfg 是探针工具的阈值；由 cmd/scanner 从 cfg.Probe 注入。
	ProbeCfg config.ProbeConfig

	// AuthKeywords 透传给 HeuristicCheck 的 all_auth_error 规则；
	// 由 cmd/scanner 从 cfg.Heuristic.AuthKeywords 注入；nil 时 heuristic 包内部回退默认表。
	AuthKeywords []string

	// Tenant 用于 LoadLessonsForPrompt 跨 engagement 拉 lesson；
	// 由 cmd/scanner 从 cfg.Engagement.DefaultTenant 注入；空时退化 "default"。
	Tenant string

	// ToolExecuteTimeoutSeconds 子 ReAct 单次 tool Execute 兜底超时（秒）；
	// 由 cmd/scanner 从 cfg.Toolruntime.ToolExecuteTimeoutSeconds 注入；0 = 不加 deadline。
	ToolExecuteTimeoutSeconds int
}

const skillName = "vuln/web/sqli"

// NewSubBuilder 构造 SQLi SkillBuilder 闭包。
//
// scanner 启动时调用一次，注册到 spawn.SpawnSkill.Builders["vuln/web/sqli"]。
//
// 子 ReAct 工具集：
//
//	common:   read_state / take_note / write_finding / done
//	probe:    fetch_credentials / run_replay / check_heuristics
//	external: run_command（仅 DockerRunner 可用时；跨漏洞通用沙箱执行器，
//	          LLM 自拼 sqlmap/curl/python3/sh 命令喂进去跑容器）
//
// 注入点提取已不在工具集——LLM 看 user prompt 里 flow 详情自识别。
func NewSubBuilder(deps SubBuilderDeps) func(ctx context.Context, p skill.BuilderParams) (react.Config, error) {
	probeFactory := probe.NewFactory(deps.Credentials, deps.Flows, deps.Replay)

	return func(ctx context.Context, p skill.BuilderParams) (react.Config, error) {
		reg := toolfx.NewRegistry()

		// common tools
		_ = reg.Register(common.Done{})
		_ = reg.Register(&common.ReadState{Store: deps.Engagements, EngagementID: p.EngagementID, TaskID: p.TaskID})
		_ = reg.Register(&common.TakeNote{Store: deps.Engagements, EngagementID: p.EngagementID, TaskID: p.TaskID})
		_ = reg.Register(&common.WriteFinding{
			Store:        deps.Findings,
			EngagementID: p.EngagementID,
			Host:         p.Host,
			TaskID:       p.TaskID,
			FlowID:       p.FlowID,
		})

		// 漏洞通用探针：仅 fetch_credentials / run_replay / check_heuristics。
		// 跳过 compute_similarity——SQLi 由 LLM 直读 body_hint 自决策，无需相似度差分。
		if err := probeFactory.Register(reg, p.EngagementID, p.CredentialLocations,
			probe.WithoutSimilarity(),
			probe.WithConfig(deps.ProbeCfg),
			probe.WithAuthKeywords(deps.AuthKeywords)); err != nil {
			return react.Config{}, fmt.Errorf("register probe actions: %w", err)
		}

		// 通用容器沙箱执行器（仅 docker 可用时注册）：跨漏洞共享。
		// LLM 看 system prompt 内置的工具手册（tooling/sqlmap 等）学 CLI 用法，
		// 自己拼 shell 命令喂进 run_command 跑容器，自读 stdout_tail 决策。
		if deps.DockerRunner != nil {
			s := deps.SandboxCfg
			_ = reg.Register(&external.RunCommand{
				Runner:         deps.DockerRunner,
				Image:          deps.PentoolsImage,
				Network:        deps.ScanNetwork,
				MinTimeout:     time.Duration(s.RunMinTimeoutSeconds) * time.Second,
				MaxTimeout:     time.Duration(s.RunMaxTimeoutSeconds) * time.Second,
				DefaultTimeout: time.Duration(s.RunDefaultTimeoutSeconds) * time.Second,
				DefaultMemMB:   s.RunDefaultMemMB,
				DefaultCPUs:    s.RunDefaultCPUs,
				TailBytes:      s.RunTailBytes,
			})
		}

		// SKILL 加载：主流程 + 工具手册（双层）。
		// 主 SKILL 失败 → 阻断（缺它没法跑）；tooling 失败 → 记 warn 不阻断
		// （多数情况下 vuln/web/sqli 仍能基于 LLM 训练知识工作）。
		mainCard, err := deps.SkillLoader.Load(skillName)
		if err != nil {
			return react.Config{}, fmt.Errorf("load skill %q: %w", skillName, err)
		}
		systemPromptParts := []string{mainCard.Body}
		for _, toolingName := range []string{"tooling/sqlmap", "tooling/curl", "tooling/python3", "tooling/sh"} {
			tc, terr := deps.SkillLoader.Load(toolingName)
			if terr != nil {
				// 工具手册缺失不阻断 ReAct（仅降级 LLM 用法准确性），由运维补 SKILL.md 即可。
				continue
			}
			systemPromptParts = append(systemPromptParts,
				fmt.Sprintf("\n\n---\n\n## 工具手册：%s\n\n%s", tc.Name, strings.TrimSpace(tc.Body)))
		}

		// done validator 复用 BACValidator —— reason 集合（finding_written / all_differ /
		// heuristic_skip / no_pattern_match）对 SQLi 同样适用，避免重复实现。
		validator := done_validator.NewBACValidator(deps.Engagements, deps.Findings, p.EngagementID, p.TaskID)

		reg.Use(
			// observe 最外层埋点；timeout 兜底；compress 截断；done 校验。
			middleware.Observe(),
			middleware.Timeout(deps.ToolExecuteTimeoutSeconds),
			// threshold/snippet/summary 传 0 即用 middleware fallback（64KB/16KB/1KB）。
			middleware.ResultCompress(0, 0, 0),
			middleware.DoneValidate(validator, nil),
		)

		lessonsBlock := vuln.LoadLessonsForPrompt(ctx, deps.Lessons, deps.Tenant, p.Host)
		systemPrompt := strings.Join(systemPromptParts, "")

		return react.Config{
			LLM:                p.LLM,
			Actions:            reg,
			Budget:             react.Budget{MaxSteps: vuln.DefaultSubMaxSteps, WatchdogSeconds: vuln.SubWatchdogSeconds},
			SystemPrompt:       systemPrompt,
			UserPrompt:         vuln.BuildUserPrompt(p, "SQLi", lessonsBlock),
			Observer:           p.Observer,
			ObserverEverySteps: 5,
		}, nil
	}
}

