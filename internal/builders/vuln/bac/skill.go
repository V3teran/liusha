package bac

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/vulnfinding"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/lesson"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/replay"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/toolfx"
	"github.com/V3teran/liusha/internal/toolfx/done_validator"
	"github.com/V3teran/liusha/internal/toolfx/middleware"
	"github.com/V3teran/liusha/internal/tools/common"
	"github.com/V3teran/liusha/internal/tools/probe"
)

// SubBuilderDeps BAC SkillBuilder 的依赖注入。
//
// ResultCompressDir 为 result_compress middleware 的落盘目录；
// 空时退化到 defaultResultCompressDir（避免 /tmp 在容器只读 fs 上 silent fallback）。
type SubBuilderDeps struct {
	Engagements       *engagement.Store
	Findings          *vulnfinding.Store
	Lessons           *lesson.Store // v1.2 P2 跨 engagement 长期知识库；nil 时 loadLessonsForPrompt 返空
	Credentials       credential.Provider
	Flows             *flow.Store
	Replay            *replay.Engine
	SkillLoader       *skill.Loader
	ResultCompressDir string
}

// 子 ReAct 兜底参数。MaxSteps 由 SKILL.md frontmatter budget.max_steps 驱动；
// 当 budget 缺失或 ≤0 时使用此默认值。
const (
	defaultSubMaxSteps       = 15
	subWatchdogSeconds       = 60
	defaultResultCompressDir = "./engagement-store"
)

// NewSubBuilder 构造 BAC SkillBuilder 闭包。
//
// scanner 启动时调用一次，注册到 spawn.SpawnSkill.Builders["vuln-web-bac"]
// （key 与 SKILL.md frontmatter `name` 一致，CC 风格 path-style 唯一标识）。
//
// 子 ReAct 工具集：
//
//	common:  read_state / write_fact / write_idea / write_finding / done
//	probe: fetch_credentials / replay_multi_identity / heuristic_check / compute_similarity
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
		if err := factory.Register(reg, p.EngagementID, p.CredentialLocations); err != nil {
			return react.Config{}, fmt.Errorf("register probe actions: %w", err)
		}

		// skill loader 加载 SKILL.md（命中缓存 0 IO）。
		// 极简后 Load 不再需要 cb 参数（builder 是唯一真理来源）。
		card, err := deps.SkillLoader.Load("vuln-web-bac")
		if err != nil {
			return react.Config{}, fmt.Errorf("load skill: %w", err)
		}
		validator := done_validator.NewBACValidator(deps.Engagements, deps.Findings, p.EngagementID, p.TaskID)

		// middleware: result_compress + done_validate（LoopDetect 已砍）
		compressDir := deps.ResultCompressDir
		if compressDir == "" {
			compressDir = defaultResultCompressDir
		}
		// v1.2 收尾：notes 改 engagement-scope 不再 prune；middleware 不挂 onSuccess hook。
		reg.Use(
			middleware.ResultCompress(p.EngagementID, compressDir),
			middleware.DoneValidate(validator, nil),
		)

		// v1.2 收尾：仅读 host_lesson 拿跨 engagement 长期经验（lesson_extract 单写）。
		// 旧 memory_hints 层已删——lesson_extract 的输出本身就是经验手册，不需要再拼"本 engagement"段。
		lessonsBlock := loadLessonsForPrompt(ctx, deps.Lessons, p.Host)

		// SystemPrompt = SKILL.md body 单一来源（v1.1 末删除 cognitive_map 字段，
		// 6 槽位内容已合并入 body，避免冗余浪费 token）。
		systemPrompt := card.Body

		// MaxSteps 用代码默认（业界做法：runtime budget 不放 SKILL.md frontmatter，
		// SKILL.md 只声明能力 + 描述；budget 在 config.yaml 或代码层）。
		maxSteps := defaultSubMaxSteps

		return react.Config{
			LLM:                p.LLM,
			Actions:            reg,
			Budget:             react.Budget{MaxSteps: maxSteps, WatchdogSeconds: subWatchdogSeconds},
			SystemPrompt:       systemPrompt,
			UserPrompt:         buildUserPrompt(p, lessonsBlock),
			Observer:           p.Observer, // 子 ReAct 复用主 ReAct 的 observer 实例
			ObserverEverySteps: 5,
		}, nil
	}
}

// buildUserPrompt 构造子 ReAct 第一条 user message。
//
// lessonsBlock：跨 engagement 长期 host_lesson（lesson_extract 蒸馏经验，含具体 payload/手法）。
// 为空时降级跳过该小节，只返 base 指令。
func buildUserPrompt(p skill.BuilderParams, lessonsBlock string) string {
	var b strings.Builder
	b.WriteString("测试 flow_id=" + strconv.FormatInt(p.FlowID, 10) +
		" host=" + p.Host + " " + p.Method + " " + p.URL +
		"。立刻按 BAC SKILL.md 流程调用工具，不要文本回答。")
	if lessonsBlock != "" {
		b.WriteString("\n\n## Host 历史经验（跨 engagement 长期知识库，可能含旧情报；带具体 payload/手法可直接复用）\n")
		b.WriteString(lessonsBlock)
	}
	return b.String()
}

// loadLessonsForPrompt 同步读 host_lesson top-N 拼成可读文本。
//
// store 为 nil / host 为空 / 查询失败 → 返空字符串（让子 ReAct 退化到无 lesson 形态）。
const lessonsPromptLimit = 20

func loadLessonsForPrompt(ctx context.Context, store *lesson.Store, host string) string {
	if store == nil || host == "" {
		return ""
	}
	lessons, err := store.ListByHost(ctx, "default", host, lessonsPromptLimit)
	if err != nil || len(lessons) == 0 {
		return ""
	}
	var b strings.Builder
	for i, l := range lessons {
		fmt.Fprintf(&b, "%d. (priority=%d, hits=%d) %s\n", i+1, l.Priority, l.HitCount, l.Content)
	}
	return b.String()
}

