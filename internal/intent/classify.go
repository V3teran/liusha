// Package intent 用便宜 LLM 判定用户对话消息的意图：动作（触发扫描）还是问答（读结果回答）。
package intent

import (
	"context"
	"strings"

	"github.com/V3teran/liusha/internal/llm"
)

// Intent 是消息意图。
type Intent string

const (
	IntentAction Intent = "action" // 触发/继续扫描
	IntentQA     Intent = "qa"     // 就已有结果提问
)

// gen 是 Classify 依赖的最小 LLM 接口（llm.Generator 满足）。
type gen interface {
	Generate(ctx context.Context, msgs []llm.Message, tools []llm.ToolSchema) (llm.Result, error)
}

const systemPrompt = `你是渗透测试对话的意图分类器。判断用户最新消息是想"让 agent 执行/继续扫描动作"（action），还是"就已挖到的结果提问/解释/总结"（qa）。
只输出一个词：action 或 qa。无法确定时输出 qa。`

// Classify 调 light LLM 分类；解析不出 action/qa 或调用失败 → 默认 qa（便宜、安全：
// 避免把提问误判成动作而白烧一次扫描）。
func Classify(ctx context.Context, g gen, userMessage string) Intent {
	res, err := g.Generate(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: systemPrompt},
		{Role: llm.RoleUser, Content: userMessage},
	}, nil)
	if err != nil {
		return IntentQA
	}
	switch strings.ToLower(strings.TrimSpace(res.Content)) {
	case "action":
		return IntentAction
	case "qa":
		return IntentQA
	default:
		return IntentQA
	}
}
