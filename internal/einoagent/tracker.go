// Package einoagent 用 eino ADK 装配 liusha 的 hunter agent（取代 internal/react 手写循环）。
//
// 设计（eino 全面迁移 P3/P4，见 docs/superpowers/specs/2026-06-07-eino-full-migration.md）：
//   - tracker（passive 单 agent）→ ChatModelAgent + Runner（本文件）
//   - commander+striker（active）→ deep prebuilt（P4）
//   - model 由 einollm 工厂产出，**每个 agent 独立实例**（per-hunter 铁律，spike 实测）
//   - tools 由 einotools 产出（原生 eino tool）
//   - 终止：tracker 是单 agent，不需显式 done 工具——LLM 不再调工具（输出文字）即自然收尾
package einoagent

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

const defaultTrackerMaxIters = 30

// TrackerResult 是一次 tracker 运行的产物摘要。
type TrackerResult struct {
	FinalText string   // 最终 assistant 文字输出
	ToolCalls []string // 按顺序调用过的工具名（用于断言/可观测）
}

// RunTracker 用 eino ChatModelAgent 跑一条 passive 流量（替代 react.Run 的 tracker 路径）。
//
// m 必须是**独立** ChatModel 实例（per-hunter，见 einollm 包注释铁律）。
// instruction = 拼好的 system prompt（shared + tracker 段）；flowText = 一条 raw HTTP 流量。
// opts 透传给 Runner.Run（如 adk.WithCallbacks 注入计费埋点 handler）。
func RunTracker(ctx context.Context, m model.ToolCallingChatModel, tools []tool.BaseTool, instruction, flowText string, opts ...adk.AgentRunOption) (TrackerResult, error) {
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          "tracker",
		Description:   "passive 侦察兵：分析一条流量挖漏洞",
		Instruction:   instruction,
		Model:         m,
		ToolsConfig:   adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools}},
		MaxIterations: defaultTrackerMaxIters,
	})
	if err != nil {
		return TrackerResult{}, fmt.Errorf("build tracker agent: %w", err)
	}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})
	iter := runner.Run(ctx, []adk.Message{schema.UserMessage(flowText)}, opts...)

	var res TrackerResult
	var lastText strings.Builder
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			return res, fmt.Errorf("tracker run: %w", ev.Err)
		}
		if ev.Output == nil || ev.Output.MessageOutput == nil {
			continue
		}
		mv := ev.Output.MessageOutput
		if mv.Message == nil || mv.Role != schema.Assistant {
			continue
		}
		if mv.Message.Content != "" {
			lastText.Reset()
			lastText.WriteString(mv.Message.Content)
		}
		for _, tc := range mv.Message.ToolCalls {
			res.ToolCalls = append(res.ToolCalls, tc.Function.Name)
		}
	}
	res.FinalText = lastText.String()
	return res, nil
}
