package bac

import (
	"context"
	"fmt"

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
	"github.com/V3teran/liusha/internal/tools/probe"
)

// SubBuilderDeps BAC SkillBuilder 的依赖注入。
type SubBuilderDeps struct {
	Engagements *engagement.Store
	Findings    *finding.Store
	Lessons     *lesson.Store // v1.2 P2 跨 engagement 长期知识库；nil 时 loadLessonsForPrompt 返空
	Credentials credential.Provider
	Flows       *flow.Store
	Replay      *replay.Engine
	SkillLoader *skill.Loader

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

// NewSubBuilder 构造 BAC SkillBuilder 闭包。
//
// scanner 启动时调用一次，注册到 spawn.SpawnSkill.Builders["vuln/web/bac"]
// （key 与 SKILL.md frontmatter `name` 一致，CC 风格 path-style 唯一标识）。
//
// 子 ReAct 工具集：
//
//	common:  read_state / write_fact / write_idea / write_finding / done
//	probe: fetch_credentials / run_replay / check_heuristics / compute_similarity
func NewSubBuilder(deps SubBuilderDeps) func(ctx context.Context, p skill.BuilderParams) (react.Config, error) {
	factory := probe.NewFactory(deps.Credentials, deps.Flows, deps.Replay)

	return func(ctx context.Context, p skill.BuilderParams) (react.Config, error) {
		reg := toolfx.NewRegistry()

		// common tools (v1.2 收尾：facts+ideas 合并 TakeNote)
		_ = reg.Register(common.Done{})
		_ = reg.Register(&common.ReadState{Store: deps.Engagements, EngagementID: p.EngagementID, TaskID: p.TaskID})
		_ = reg.Register(&common.TakeNote{Store: deps.Engagements, EngagementID: p.EngagementID, TaskID: p.TaskID})
		// BuilderParams.TaskID 现在是 delegate 在 agent_task 表里真注册的 sub-task UUID
		// （v1.2 P0 修复），WriteFinding 透传给 finding.task_id 让 SQL 直接挂上具体 sub-task。
		// FlowID 来自 BuilderParams（delegate 透传 in.FlowID），写入 finding.source_flow_id
		// 让漏洞↔流量回查走 FK 而非 jsonb 文本匹配。
		_ = reg.Register(&common.WriteFinding{
			Store:        deps.Findings,
			EngagementID: p.EngagementID,
			Host:         p.Host,
			TaskID:       p.TaskID,
			FlowID:       p.FlowID,
		})

		// 漏洞探针通用工具（fetch_credentials / replay / heuristic / similarity）
		// 由 probe 包提供，所有 vuln skill 共享。
		// CredentialLocations 来自 BuilderParams（上游 classify_traffic 透传），
		// FetchCredentials 用它构造带占位 token 的 anonymous 假认证。
		if err := factory.Register(reg, p.EngagementID, p.CredentialLocations,
			probe.WithConfig(deps.ProbeCfg),
			probe.WithAuthKeywords(deps.AuthKeywords)); err != nil {
			return react.Config{}, fmt.Errorf("register probe actions: %w", err)
		}

		// skill loader 加载 SKILL.md（命中缓存 0 IO）。
		// 极简后 Load 不再需要 cb 参数（builder 是唯一真理来源）。
		card, err := deps.SkillLoader.Load("vuln/web/bac")
		if err != nil {
			return react.Config{}, fmt.Errorf("load skill: %w", err)
		}
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

		return react.Config{
			LLM:                p.LLM,
			Actions:            reg,
			Budget:             react.Budget{MaxSteps: vuln.DefaultSubMaxSteps, WatchdogSeconds: vuln.SubWatchdogSeconds},
			SystemPrompt:       card.Body,
			UserPrompt:         vuln.BuildUserPrompt(p, "BAC", lessonsBlock),
			Observer:           p.Observer,
			ObserverEverySteps: 5,
		}, nil
	}
}

