// Package spawn 提供主 ReAct 的 spawn_skill 工具：按 skill 名嵌套调用子 ReAct（同进程同步）。
//
// 与 tools/traffic（业务工具）分离：spawn 只是"派发器"，不属于业务工具；
// 与 internal/skill 包解耦：Builder/BuilderParams 类型定义在 skill 包，spawn 只消费类型。
package spawn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/tool"
)

// SpawnSkill 工具：同进程嵌套调用子 ReAct（同步阻塞）。
//
// 多个 spawn_skill 在主 LLM 同一轮返回时，runtime tool_calls 并行框架（T12）
// 会启 N 个 goroutine 并行跑（每个 goroutine 独立调 SpawnSkill.Execute）。
type SpawnSkill struct {
	Builders     map[string]skill.Builder
	EngagementID string
	SubLLM       llm.Generator
}

// Name 返回工具名 "spawn_skill"。
func (a *SpawnSkill) Name() string { return "spawn_skill" }

// Description 给 LLM 的工具描述。
func (a *SpawnSkill) Description() string {
	return "嵌套调用某个 skill 的子 ReAct 完成漏洞测试。" +
		"支持的 skill 由调用方注册（如 'bac'）。子 ReAct 完成后返 summary。"
}

// ParametersJSON 工具入参 JSON Schema。
func (a *SpawnSkill) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
        "type":"object",
        "properties":{
            "skill":{"type":"string","description":"如 bac/sqli/xss"},
            "flow_id":{"type":"integer"},
            "host":{"type":"string"},
            "url":{"type":"string"},
            "method":{"type":"string"}
        },
        "required":["skill","flow_id","host"]
    }`)
}

// Execute 装配子 ReAct + 同进程同步嵌套跑 + 返 summary。
func (a *SpawnSkill) Execute(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	var in struct {
		Skill  string `json:"skill"`
		FlowID int64  `json:"flow_id"`
		Host   string `json:"host"`
		URL    string `json:"url"`
		Method string `json:"method"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return tool.Result{}, fmt.Errorf("decode args: %w", err)
	}
	if in.Skill == "" || in.FlowID == 0 || in.Host == "" {
		return tool.Result{}, errors.New("skill/flow_id/host 必填")
	}
	builder, ok := a.Builders[in.Skill]
	if !ok {
		return tool.Result{}, fmt.Errorf("unknown skill: %s", in.Skill)
	}

	cfg, err := builder(ctx, skill.BuilderParams{
		EngagementID: a.EngagementID,
		FlowID:       in.FlowID,
		Host:         in.Host,
		URL:          in.URL,
		Method:       in.Method,
		LLM:          a.SubLLM,
	})
	if err != nil {
		return tool.Result{}, fmt.Errorf("build skill %s: %w", in.Skill, err)
	}

	sub, err := react.Run(ctx, cfg)
	if err != nil {
		return tool.Result{}, fmt.Errorf("sub-react %s: %w", in.Skill, err)
	}

	summary := fmt.Sprintf("skill=%s steps=%d terminate=%s usage=in:%d/out:%d",
		in.Skill, sub.TotalSteps, sub.TerminateBy,
		sub.TotalUsage.InTokens, sub.TotalUsage.OutTokens)
	return tool.Result{Summary: summary}, nil
}
