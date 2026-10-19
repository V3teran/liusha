package llm

import (
	"context"
)

// ─── StreamChat / CountTokens（Provider 桥接用可选能力） ───────────────────────

func (g *openAICompatGen) StreamChat(ctx context.Context, msgs []Message, tools []ToolSchema, maxTokens int) (<-chan StreamEvent, error) {
	// 1. 准备请求参数
	creq, err := prepareOpenAICompatRequest(msgs, tools, g.model, maxTokens, g.maxTokens, g.supportsVision)
	if err != nil {
		return nil, err
	}

	// 2. 创建流式请求
	stream, err := g.client.CreateChatCompletionStream(ctx, creq)
	if err != nil {
		return nil, wrapOpenAICompatErr(err)
	}

	// 3. 启动异步处理
	ch := make(chan StreamEvent, 32)
	go processOpenAICompatStream(ctx, stream, ch)

	return ch, nil
}

// CountTokens 本地估算输入 token 数（OpenAI 协议无专用计数端点）。
// 每 message 约 4 token overhead，文本按字符数估算；精度足够触发压缩决策。
func (g *openAICompatGen) CountTokens(_ context.Context, msgs []Message, tools []ToolSchema) (int, error) {
	total := 3
	for _, m := range msgs {
		total += 4
		total += estimateTokens(m.Content)
		for _, tc := range m.ToolCalls {
			total += 4
			total += estimateTokens(tc.Name)
			total += estimateTokens(string(tc.Arguments))
		}
		total += estimateTokens(m.ToolCallID)
	}
	for _, t := range tools {
		total += 10
		total += estimateTokens(t.Name)
		total += estimateTokens(t.Description)
		total += estimateTokens(string(t.Parameters))
	}
	return total, nil
}

// estimateTokens 粗估文本 token 数：英文约 1 token/4 字符，中文约 1 token/字。
func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	ascii, cjk := 0, 0
	for _, r := range s {
		if r > 0x2E7F {
			cjk++
		} else {
			ascii++
		}
	}
	return ascii/4 + cjk
}
