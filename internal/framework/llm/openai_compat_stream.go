package llm

import (
	"context"

	openai "github.com/sashabaranov/go-openai"
)

// ─── StreamChat（Provider 桥接用可选能力） ─────────────────────────────────────

func (g *openAICompatGen) StreamChat(ctx context.Context, msgs []Message, tools []ToolSchema, maxTokens int) (<-chan StreamEvent, error) {
	// 1. 准备请求参数
	creq, err := prepareOpenAICompatRequest(msgs, tools, g.model, g.supportsVision)
	if err != nil {
		return nil, err
	}
	limit := g.maxTokens
	if maxTokens > 0 {
		limit = maxTokens
	}

	// 2. 创建流式请求（字段被拒在建流时即以 4xx 返回：按新结论改写后重建）
	var stream *openai.ChatCompletionStream
	for attempt := 0; ; attempt++ {
		g.compat.apply(&creq, limit, nil)
		stream, err = g.client.CreateChatCompletionStream(ctx, creq)
		if err != nil && attempt < maxCompatRetries && g.compat.adapt(err, &creq) {
			continue
		}
		break
	}
	if err != nil {
		return nil, wrapOpenAICompatErr(err)
	}

	// 3. 启动异步处理
	ch := make(chan StreamEvent, 32)
	go processOpenAICompatStream(ctx, stream, ch)

	return ch, nil
}
