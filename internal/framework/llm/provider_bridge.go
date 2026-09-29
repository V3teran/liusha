// Package llm 的 Generator→Provider 桥。
//
// 归一后本包只有一套协议适配器（openai_compat / anthropic，实现 Generator），
// Provider 契约由本桥承载：Complete 直转 Generate；Stream/CountTokens 走
// 可选能力接口——适配器实现了就支持，未实现返回明确错误而非静默降级。
// Package llm 的 Generator→Provider 桥：Complete 直转 Generate，Stream/CountTokens 走可选能力接口。
package llm

import (
	"context"
	"fmt"
)

// StreamChatGenerator 是流式生成的可选能力。
type StreamChatGenerator interface {
	StreamChat(ctx context.Context, messages []Message, tools []ToolSchema, maxTokens int) (<-chan StreamEvent, error)
}

// TokenCountingGenerator 是 token 计数的可选能力（openai 为本地估算，anthropic 走专用端点）。
type TokenCountingGenerator interface {
	CountTokens(ctx context.Context, messages []Message, tools []ToolSchema) (int, error)
}

// NewProvider 把 Generator 适配成 Provider。
func NewProvider(g Generator) Provider {
	return &generatorProvider{g: g}
}

type generatorProvider struct {
	g Generator
}

func (p *generatorProvider) Complete(ctx context.Context, req Request) (Response, error) {
	res, err := p.g.Generate(ctx, req.Messages, req.Tools)
	if err != nil {
		return Response{}, err
	}
	return Response{
		Content:      res.Content,
		ToolCalls:    res.ToolCalls,
		Usage:        res.Usage,
		FinishReason: res.FinishReason,
	}, nil
}

func (p *generatorProvider) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	sg, ok := p.g.(StreamChatGenerator)
	if !ok {
		return nil, fmt.Errorf("provider bridge: %s/%s 不支持流式", p.g.Provider(), p.g.Model())
	}
	return sg.StreamChat(ctx, req.Messages, req.Tools, req.MaxTokens)
}

func (p *generatorProvider) CountTokens(ctx context.Context, req Request) (int, error) {
	cg, ok := p.g.(TokenCountingGenerator)
	if !ok {
		return 0, fmt.Errorf("provider bridge: %s/%s 不支持 token 计数", p.g.Provider(), p.g.Model())
	}
	return cg.CountTokens(ctx, req.Messages, req.Tools)
}

func (p *generatorProvider) ModelID() string    { return p.g.Model() }
func (p *generatorProvider) ProviderID() string { return p.g.Provider() }
