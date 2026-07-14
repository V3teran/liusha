// Package einoagent 用 eino ADK 装配 liusha 的 hunter agent。
//
// 设计：
//   - trafficAnalysis（passive 单 agent）→ ChatModelAgent + Runner（本文件）
//   - orchestrator+exploitation（active）→ deep prebuilt（P4）
//   - model 由 einollm 工厂产出（共享实例亦并发安全，见 einollm 包注释；旧 per-hunter 铁律已纠正）
//   - tools 由 einotools 产出（原生 eino tool）
//   - 终止：trafficAnalysis 是单 agent，不需显式 done 工具——LLM 不再调工具（输出文字）即自然收尾
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

// defaultTrafficAnalysisMaxIters：passive 单 agent 迭代上限。passive 分析单条流量，
// 但 upload→RCE 这类长链条（找上传点→造 webshell→上传→访问→确认执行→write_finding）需余量，
// 50 步够；真失控由 owner abort watcher + asynq 超时兜底。
const defaultTrafficAnalysisMaxIters = 50

// defaultModelRetries 是 ChatModel 调用失败的重试次数（替代 react 的 retry 中间件）。
// 国产 provider（小米 mimo 等）偶发瞬时 4xx（如「Param Incorrect」）/ 429 / EOF —— e2e 实测
// 一次瞬时 400 会杀掉整个 trafficAnalysis run。eino 内建 ModelRetryConfig：默认指数退避（100ms→10s）+
// jitter，重试整轮 ChatModel 调用。MaxRetries=3 → 最多 4 次调用，瞬时错重试即恢复，永久错退避后传播。
const defaultModelRetries = 3

// TrafficAnalysisResult 是一次 trafficAnalysis 运行的产物摘要。
type TrafficAnalysisResult struct {
	FinalText string   // 最终 assistant 文字输出
	ToolCalls []string // 按顺序调用过的工具名（用于断言/可观测）
}

// defaultExploitationMaxIters 是 deep sub-agent（exploitation 等杀伤链阶段）未声明 max_iterations 时的兜底
// （深挖单点比 passive trafficAnalysis 多步）。由 deep_swarm.go 装配 sub-agent 时引用。
const defaultExploitationMaxIters = 60

// RunTrafficAnalysis 用 eino ChatModelAgent 跑一条 passive 流量（替代 react.Run 的 trafficAnalysis 路径）。
//
// m 是 einollm 工厂产出的 ChatModel（passive 每 hunter 一个单 agent；共享亦安全，见 einollm 包注释）。
// instruction = 拼好的 system prompt（shared + trafficAnalysis 段）；flowText = 一条 raw HTTP 流量。
// middlewares 注入 struct 版 AgentMiddleware（遥测/事件/截图回灌）；handlers 注入接口版
// ChatModelAgentMiddleware（① summarization 上下文压缩，见 NewSummarizationHandler）；均可为 nil。
// opts 透传给 Runner.Run（如 adk.WithCallbacks 注入计费埋点 handler）。
// maxIters 来自 passive 角色 frontmatter（hunters/passive/traffic-analysis.md 的 max_iterations）；
// <=0 时回退 defaultTrafficAnalysisMaxIters。
func RunTrafficAnalysis(ctx context.Context, m model.ToolCallingChatModel, tools []tool.BaseTool, instruction, flowText string, maxIters int, middlewares []adk.AgentMiddleware, handlers []adk.ChatModelAgentMiddleware, opts ...adk.AgentRunOption) (TrafficAnalysisResult, error) {
	if maxIters <= 0 {
		maxIters = defaultTrafficAnalysisMaxIters
	}
	return runSingleAgent(ctx, agentSpec{
		name: "traffic-analysis", desc: "passive 侦察：分析一条流量挖漏洞", maxIters: maxIters,
	}, m, tools, instruction, flowText, middlewares, handlers, opts...)
}

// agentSpec 是单 agent 的固定身份/预算。
type agentSpec struct {
	name     string
	desc     string
	maxIters int
}

// runSingleAgent 装配 + 跑一个单 ChatModelAgent，消费事件流收集 ToolCalls + 最终文字。
// trafficAnalysis / exploitation 共用此机制（eino 单 agent 不调工具即自然收尾，无需 done）。
func runSingleAgent(ctx context.Context, spec agentSpec, m model.ToolCallingChatModel, tools []tool.BaseTool, instruction, userText string, middlewares []adk.AgentMiddleware, handlers []adk.ChatModelAgentMiddleware, opts ...adk.AgentRunOption) (TrafficAnalysisResult, error) {
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:             spec.name,
		Description:      spec.desc,
		Instruction:      instruction,
		Model:            m,
		ToolsConfig:      adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools}},
		MaxIterations:    spec.maxIters,
		Middlewares:      middlewares,
		Handlers:         handlers,                                               // ① summarization 上下文压缩（接口版扩展点）
		ModelRetryConfig: &adk.ModelRetryConfig{MaxRetries: defaultModelRetries}, // 瞬时 provider 错重试（默认指数退避+jitter）
	})
	if err != nil {
		return TrafficAnalysisResult{}, fmt.Errorf("build %s agent: %w", spec.name, err)
	}

	// EnableStreaming：让 ChatModel 走 Stream，agent 思路逐 token 产出 → reasoning callback 发
	// reasoning_delta 帧（前端逐字打字机）。drainAgentEvents 用 GetMessage() 兼容流式聚合最终结果。
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: true})
	iter := runner.Run(ctx, []adk.Message{schema.UserMessage(userText)}, opts...)
	return drainAgentEvents(iter, spec.name)
}

// drainAgentEvents 消费 eino AgentEvent 流，收集 assistant 的 ToolCalls + 最终文字。
// trafficAnalysis/exploitation（runSingleAgent）与 deep orchestrator（RunDeepSwarm）共用。
func drainAgentEvents(iter *adk.AsyncIterator[*adk.AgentEvent], label string) (TrafficAnalysisResult, error) {
	var res TrafficAnalysisResult
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
		if mv.Role != schema.Assistant {
			continue
		}
		// GetMessage 兼容流式（EnableStreaming）：IsStreaming 时 ConcatMessageStream 聚合完整消息，
		// 非流式直接返回 mv.Message。否则开流式后 mv.Message 为 nil → 丢最终文字 + tool_calls。
		msg, gerr := mv.GetMessage()
		if gerr != nil || msg == nil {
			continue
		}
		if msg.Content != "" {
			lastText.Reset()
			lastText.WriteString(msg.Content)
		}
		for _, tc := range msg.ToolCalls {
			res.ToolCalls = append(res.ToolCalls, tc.Function.Name)
		}
	}
	res.FinalText = lastText.String()
	return res, nil
}
