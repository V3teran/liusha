package bac

import (
	"context"
	"fmt"
	"strconv"

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
	"github.com/V3teran/liusha/internal/tools/mainreact"
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

// 子 ReAct 默认参数。
const (
	subMaxSteps        = 15
	subWatchdogSeconds = 60
	resultCompressDir  = "/tmp/liusha-react-compress"
)

// NewSubBuilder 构造 BAC SkillBuilder 闭包。
//
// scanner 启动时调用一次，注册到 mainreact.SpawnSkill.Builders["bac"]。
//
// 子 ReAct 工具集：
//
//	common: read_state / write_fact / write_idea / write_finding / done
//	bac:    fetch_creds / replay / diff / heuristic_match (factory.Register 提供)
func NewSubBuilder(deps SubBuilderDeps) func(ctx context.Context, p mainreact.SkillParams) (react.Config, error) {
	factory := NewFactory(deps.Credentials, deps.Flows, deps.Replay)

	return func(ctx context.Context, p mainreact.SkillParams) (react.Config, error) {
		reg := tool.NewRegistry()

		// common tools
		_ = reg.Register(common.Done{})
		_ = reg.Register(&common.ReadState{Store: deps.Engagements, EngagementID: p.EngagementID})
		_ = reg.Register(&common.WriteFact{Store: deps.Engagements, EngagementID: p.EngagementID})
		_ = reg.Register(&common.WriteIdea{Store: deps.Engagements, EngagementID: p.EngagementID})
		_ = reg.Register(&common.WriteFinding{Store: deps.Findings, EngagementID: p.EngagementID})

		// bac-specific tools
		if err := factory.Register(reg, p.EngagementID); err != nil {
			return react.Config{}, fmt.Errorf("register bac actions: %w", err)
		}

		// skill loader 加载 SKILL.md 正文当 system prompt
		card, err := deps.SkillLoader.Load("vuln/web/bac", done_validator.IsRegistered)
		if err != nil {
			return react.Config{}, fmt.Errorf("load skill: %w", err)
		}
		validator := done_validator.NewBACValidator(deps.Engagements, deps.Findings, p.EngagementID)

		// middleware: result_compress + done_validate（LoopDetect 已砍）
		reg.Use(
			middleware.ResultCompress(p.EngagementID, resultCompressDir),
			middleware.DoneValidate(validator),
		)

		return react.Config{
			LLM:          p.LLM,
			Actions:      reg,
			Budget:       react.Budget{MaxSteps: subMaxSteps, WatchdogSeconds: subWatchdogSeconds},
			SystemPrompt: card.Body,
			UserPrompt:   buildUserPrompt(p),
		}, nil
	}
}

// buildUserPrompt 构造子 ReAct 第一条 user message。
func buildUserPrompt(p mainreact.SkillParams) string {
	return "测试 flow_id=" + strconv.FormatInt(p.FlowID, 10) +
		" host=" + p.Host + " " + p.Method + " " + p.URL +
		"。立刻按 BAC SKILL.md 流程调用工具，不要文本回答。"
}
