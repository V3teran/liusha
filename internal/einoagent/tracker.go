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

// defaultModelRetries 是 ChatModel 调用失败的重试次数（替代 react 的 retry 中间件）。
// 国产 provider（小米 mimo 等）偶发瞬时 4xx（如「Param Incorrect」）/ 429 / EOF —— e2e 实测
// 一次瞬时 400 会杀掉整个 tracker run。eino 内建 ModelRetryConfig：默认指数退避（100ms→10s）+
// jitter，重试整轮 ChatModel 调用。MaxRetries=3 → 最多 4 次调用，瞬时错重试即恢复，永久错退避后传播。
const defaultModelRetries = 3

// TrackerResult 是一次 tracker 运行的产物摘要。
type TrackerResult struct {
	FinalText string   // 最终 assistant 文字输出
	ToolCalls []string // 按顺序调用过的工具名（用于断言/可观测）
}

const (
	defaultStrikerMaxIters   = 120 // striker 深挖单点比 tracker 多步（active）
	defaultCommanderMaxIters = 300 // commander 协调多轮 spawn，预算更高
)

// RunTracker 用 eino ChatModelAgent 跑一条 passive 流量（替代 react.Run 的 tracker 路径）。
//
// m 必须是**独立** ChatModel 实例（per-hunter，见 einollm 包注释铁律）。
// instruction = 拼好的 system prompt（shared + tracker 段）；flowText = 一条 raw HTTP 流量。
// middlewares 注入 AgentMiddleware（如历史压缩 NewCompactionMiddleware）；可为 nil。
// opts 透传给 Runner.Run（如 adk.WithCallbacks 注入计费埋点 handler）。
func RunTracker(ctx context.Context, m model.ToolCallingChatModel, tools []tool.BaseTool, instruction, flowText string, middlewares []adk.AgentMiddleware, opts ...adk.AgentRunOption) (TrackerResult, error) {
	return runSingleAgent(ctx, agentSpec{
		name: "tracker", desc: "passive 侦察兵：分析一条流量挖漏洞", maxIters: defaultTrackerMaxIters,
	}, m, tools, instruction, flowText, middlewares, opts...)
}

// RunStriker 用 eino ChatModelAgent 跑一个 active striker（接 brief 深挖单点，替代 react.Run 的 striker 路径）。
// striker 是单 agent（不再 spawn），自然收尾。userText = commander 写的 brief（+ 可选流量段）。
func RunStriker(ctx context.Context, m model.ToolCallingChatModel, tools []tool.BaseTool, instruction, userText string, middlewares []adk.AgentMiddleware, opts ...adk.AgentRunOption) (TrackerResult, error) {
	return runSingleAgent(ctx, agentSpec{
		name: "striker", desc: "active 突击手：接 brief 深挖单点", maxIters: defaultStrikerMaxIters,
	}, m, tools, instruction, userText, middlewares, opts...)
}

// RunCommander 用 eino ChatModelAgent 跑 active commander（接 brief 拆活派 striker，替代 react.Run 的 commander 路径）。
//
// commander 也是单 ChatModelAgent —— 其工具集含 spawn_striker（同步工具）。并发派活 = 一轮发多个
// spawn_striker 调用（eino ToolsNode 并行执行）；done-gating 天然（同步工具未返回不能收口）。
// 故不需要 react 路径的 PreDoneCheck / OnNoToolCall / Registry。userText = 站点任务 brief。
func RunCommander(ctx context.Context, m model.ToolCallingChatModel, tools []tool.BaseTool, instruction, userText string, middlewares []adk.AgentMiddleware, opts ...adk.AgentRunOption) (TrackerResult, error) {
	return runSingleAgent(ctx, agentSpec{
		name: "commander", desc: "active 指挥官：拆活派 striker，不亲自挖洞", maxIters: defaultCommanderMaxIters,
	}, m, tools, instruction, userText, middlewares, opts...)
}

// agentSpec 是单 agent 的固定身份/预算（tracker 与 striker 仅此不同）。
type agentSpec struct {
	name     string
	desc     string
	maxIters int
}

// runSingleAgent 装配 + 跑一个单 ChatModelAgent，消费事件流收集 ToolCalls + 最终文字。
// tracker / striker 共用此机制（eino 单 agent 不调工具即自然收尾，无需 done）。
func runSingleAgent(ctx context.Context, spec agentSpec, m model.ToolCallingChatModel, tools []tool.BaseTool, instruction, userText string, middlewares []adk.AgentMiddleware, opts ...adk.AgentRunOption) (TrackerResult, error) {
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:             spec.name,
		Description:      spec.desc,
		Instruction:      instruction,
		Model:            m,
		ToolsConfig:      adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools}},
		MaxIterations:    spec.maxIters,
		Middlewares:      middlewares,
		ModelRetryConfig: &adk.ModelRetryConfig{MaxRetries: defaultModelRetries}, // 瞬时 provider 错重试（默认指数退避+jitter）
	})
	if err != nil {
		return TrackerResult{}, fmt.Errorf("build %s agent: %w", spec.name, err)
	}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})
	iter := runner.Run(ctx, []adk.Message{schema.UserMessage(userText)}, opts...)
	return drainAgentEvents(iter, spec.name)
}

// drainAgentEvents 消费 eino AgentEvent 流，收集 assistant 的 ToolCalls + 最终文字。
// tracker/striker（runSingleAgent）与 deep commander（RunDeepSwarm）共用。
func drainAgentEvents(iter *adk.AsyncIterator[*adk.AgentEvent], label string) (TrackerResult, error) {
	var res TrackerResult
	var lastText strings.Builder
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			return res, fmt.Errorf("%s run: %w", label, ev.Err)
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
