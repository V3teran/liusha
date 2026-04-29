package llm

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/claude"
)

// ClaudeConfig 是 Claude provider 的最小配置集。
type ClaudeConfig struct {
	BaseURL   string
	APIKey    string
	Model     string
	MaxTokens int
}

type claudeGen struct {
	model   *claude.ChatModel
	modelID string
}

// NewClaude 同 NewDeepSeek：tools 一次性绑定，Generator 非线程安全。
//
// BaseURL 为空时不显式设置，让 SDK 走默认 endpoint（或 ANTHROPIC_BASE_URL 环境变量）。
func NewClaude(ctx context.Context, c ClaudeConfig, tools []ToolSchema) (Generator, error) {
	cfg := &claude.Config{
		APIKey:    c.APIKey,
		Model:     c.Model,
		MaxTokens: c.MaxTokens,
	}
	if c.BaseURL != "" {
		bu := c.BaseURL
		cfg.BaseURL = &bu
	}
	cm, err := claude.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new claude: %w", err)
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
	return &claudeGen{model: cm, modelID: c.Model}, nil
}

func (g *claudeGen) Provider() string { return "anthropic" }
func (g *claudeGen) Model() string    { return g.modelID }

// Generate 发起一次同步对话；tools 参数被忽略（已在 New 时绑定）。
func (g *claudeGen) Generate(ctx context.Context, msgs []Message, _ []ToolSchema) (Result, error) {
	out, err := g.model.Generate(ctx, toEinoMessages(msgs))
	if err != nil {
		return Result{}, fmt.Errorf("claude generate: %w", err)
	}
	return fromEinoMessage(out, "anthropic", g.modelID), nil
}
