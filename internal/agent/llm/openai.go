package llm

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/openai"
)

// OpenAIConfig 是 OpenAI / Moonshot（OpenAI-compatible）共用的最小配置集。
type OpenAIConfig struct {
	BaseURL   string
	Model     string
	APIKey    string
	MaxTokens int
}

type openaiGen struct {
	model    *openai.ChatModel
	provider string // "openai" 或 "moonshot"（OpenAI-compatible 共用同一 adapter）
	modelID  string
}

// NewOpenAI 构造一个 OpenAI Generator。tools 一次性绑定。
//
// 注意：返回的 Generator 不安全跨 goroutine 并发使用。
func NewOpenAI(ctx context.Context, c OpenAIConfig, tools []ToolSchema) (Generator, error) {
	return newOpenAILike(ctx, "openai", c, tools)
}

// NewMoonshotViaOpenAI 走 openai adapter + 自定义 base_url（Moonshot 是 OpenAI-compatible）。
func NewMoonshotViaOpenAI(ctx context.Context, c OpenAIConfig, tools []ToolSchema) (Generator, error) {
	return newOpenAILike(ctx, "moonshot", c, tools)
}

func newOpenAILike(ctx context.Context, providerName string, c OpenAIConfig, tools []ToolSchema) (Generator, error) {
	cfg := &openai.ChatModelConfig{
		BaseURL: c.BaseURL,
		Model:   c.Model,
		APIKey:  c.APIKey,
	}
	if c.MaxTokens > 0 {
		mt := c.MaxTokens
		cfg.MaxTokens = &mt
	}
	cm, err := openai.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new %s: %w", providerName, err)
	}
	if len(tools) > 0 {
		einoTools, err := toEinoTools(tools)
		if err != nil {
			return nil, err
		}
		if err := cm.BindTools(einoTools); err != nil {
			return nil, fmt.Errorf("bind tools: %w", err)
		}
	}
	return &openaiGen{model: cm, provider: providerName, modelID: c.Model}, nil
}

func (g *openaiGen) Provider() string { return g.provider }
func (g *openaiGen) Model() string    { return g.modelID }

// Generate 发起一次同步对话；tools 参数被忽略（已在 New 时绑定）。
func (g *openaiGen) Generate(ctx context.Context, msgs []Message, _ []ToolSchema) (Result, error) {
	out, err := g.model.Generate(ctx, toEinoMessages(msgs))
	if err != nil {
		return Result{}, fmt.Errorf("%s generate: %w", g.provider, err)
	}
	return fromEinoMessage(out, g.provider, g.modelID), nil
}
