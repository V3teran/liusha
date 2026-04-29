package llm

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/qwen"
)

// QwenConfig 是 Qwen provider 的最小配置集。
type QwenConfig struct {
	BaseURL   string
	Model     string
	APIKey    string
	MaxTokens int
}

type qwenGen struct {
	model   *qwen.ChatModel
	modelID string
}

// NewQwen 构造一个 Qwen Generator。tools 一次性绑定。
//
// 注意：返回的 Generator 不安全跨 goroutine 并发使用。
func NewQwen(ctx context.Context, c QwenConfig, tools []ToolSchema) (Generator, error) {
	cfg := &qwen.ChatModelConfig{
		BaseURL: c.BaseURL,
		Model:   c.Model,
		APIKey:  c.APIKey,
	}
	if c.MaxTokens > 0 {
		mt := c.MaxTokens
		cfg.MaxTokens = &mt
	}
	cm, err := qwen.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new qwen: %w", err)
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
	return &qwenGen{model: cm, modelID: c.Model}, nil
}

func (g *qwenGen) Provider() string { return "qwen" }
func (g *qwenGen) Model() string    { return g.modelID }

// Generate 发起一次同步对话；tools 参数被忽略（已在 New 时绑定）。
func (g *qwenGen) Generate(ctx context.Context, msgs []Message, _ []ToolSchema) (Result, error) {
	out, err := g.model.Generate(ctx, toEinoMessages(msgs))
	if err != nil {
		return Result{}, fmt.Errorf("qwen generate: %w", err)
	}
	return fromEinoMessage(out, "qwen", g.modelID), nil
}
