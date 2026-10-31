package llm

import (
	"context"
)

// ─── StreamChat（Provider 桥接用可选能力） ─────────────────────────────────────

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
