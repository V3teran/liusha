package bac

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/replay"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/tool"
	"github.com/V3teran/liusha/internal/tool/done_validator"
	"github.com/V3teran/liusha/internal/tool/middleware"
	"github.com/V3teran/liusha/internal/tools/common"
	"github.com/V3teran/liusha/internal/tools/sniffer"
)

// SubBuilderDeps BAC SkillBuilder 的依赖注入。
type SubBuilderDeps struct {
	Engagements *engagement.Store
	Findings    *finding.Store
	Credentials credential.Provider
	Flows       *flow.Store
	Replay      *replay.Engine
	SkillLoader *skill.Loader
}

// 子 ReAct 兜底参数。MaxSteps 由 SKILL.md frontmatter budget.max_steps 驱动；
// 当 budget 缺失或 ≤0 时使用此默认值。
const (
	defaultSubMaxSteps = 15
	subWatchdogSeconds = 60
	resultCompressDir  = "/tmp/liusha-react-compress"
)

// NewSubBuilder 构造 BAC SkillBuilder 闭包。
//
// scanner 启动时调用一次，注册到 spawn.SpawnSkill.Builders["vuln/web/bac"]
// （key 与 SKILL.md frontmatter `name` 一致，CC 风格 path-style 唯一标识）。
//
// 子 ReAct 工具集：
//
//	common:  read_state / write_fact / write_idea / write_finding / done
//	sniffer: fetch_credentials / replay_multi_identity / heuristic_check / compute_similarity
func NewSubBuilder(deps SubBuilderDeps) func(ctx context.Context, p skill.BuilderParams) (react.Config, error) {
	factory := sniffer.NewFactory(deps.Credentials, deps.Flows, deps.Replay)

	return func(ctx context.Context, p skill.BuilderParams) (react.Config, error) {
		reg := tool.NewRegistry()

		// common tools
		_ = reg.Register(common.Done{})
		_ = reg.Register(&common.ReadState{Store: deps.Engagements, EngagementID: p.EngagementID})
		_ = reg.Register(&common.WriteFact{Store: deps.Engagements, EngagementID: p.EngagementID})
		_ = reg.Register(&common.WriteIdea{Store: deps.Engagements, EngagementID: p.EngagementID})
		_ = reg.Register(&common.WriteFinding{Store: deps.Findings, EngagementID: p.EngagementID})

		// 漏洞探针通用工具（fetch_credentials / replay / heuristic / similarity）
		// 由 sniffer 包提供，所有 vuln skill 共享。
		if err := factory.Register(reg, p.EngagementID); err != nil {
			return react.Config{}, fmt.Errorf("register sniffer actions: %w", err)
		}

		// skill loader 加载 SKILL.md（命中缓存 0 IO）+ cognitive_map.md 内容（CC 风格）。
		// 这里传 reg.Has 让 required_actions cross-check 启用——
		// 此时 Registry 已注册完所有 sniffer + common 工具，校验有意义。
		card, err := deps.SkillLoader.Load("vuln/web/bac", done_validator.IsRegistered, reg.Has)
		if err != nil {
			return react.Config{}, fmt.Errorf("load skill: %w", err)
		}
		validator := done_validator.NewBACValidator(deps.Engagements, deps.Findings, p.EngagementID)

		// middleware: result_compress + done_validate（LoopDetect 已砍）
		reg.Use(
			middleware.ResultCompress(p.EngagementID, resultCompressDir),
			middleware.DoneValidate(validator),
		)

		// 同步读取当前 engagement memory_hints，把 distill 写入的跨 task 经验拼到 user prompt。
		hintsBlock := loadHintsForPrompt(ctx, deps.Engagements, p.EngagementID)

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
			UserPrompt:         buildUserPrompt(p, hintsBlock),
			Observer:           p.Observer, // 子 ReAct 复用主 ReAct 的 observer 实例
			ObserverEverySteps: 5,
		}, nil
	}
}

// buildUserPrompt 构造子 ReAct 第一条 user message。
//
// hintsBlock 为空时降级为原 prompt；非空时附在尾部，告诉 LLM "上轮经验" 让它优先采纳。
func buildUserPrompt(p skill.BuilderParams, hintsBlock string) string {
	base := "测试 flow_id=" + strconv.FormatInt(p.FlowID, 10) +
		" host=" + p.Host + " " + p.Method + " " + p.URL +
		"。立刻按 BAC SKILL.md 流程调用工具，不要文本回答。"
	if hintsBlock == "" {
		return base
	}
	return base + "\n\n## 来自上轮发现的经验提示（请优先参考，避坑/扩展方向）\n" + hintsBlock
}

// loadHintsForPrompt 同步读 engagement.memory_hints.hints[] 拼成可读文本。
//
// 失败任何一步都返回空字符串（让子 prober 退化到无 hint 形态），避免单点故障阻塞 spawn。
func loadHintsForPrompt(ctx context.Context, engs *engagement.Store, eid string) string {
	if engs == nil || eid == "" {
		return ""
	}
	state, err := engs.ReadState(ctx, eid)
	if err != nil {
		return ""
	}
	var s struct {
		Hints json.RawMessage `json:"hints"`
	}
	if err := json.Unmarshal(state, &s); err != nil || len(s.Hints) == 0 {
		return ""
	}
	var hints struct {
		Hints []struct {
			Content  string `json:"content"`
			Priority int    `json:"priority"`
		} `json:"hints"`
	}
	if err := json.Unmarshal(s.Hints, &hints); err != nil || len(hints.Hints) == 0 {
		return ""
	}
	var b strings.Builder
	for i, h := range hints.Hints {
		fmt.Fprintf(&b, "%d. (priority=%d) %s\n", i+1, h.Priority, h.Content)
	}
	return b.String()
}
