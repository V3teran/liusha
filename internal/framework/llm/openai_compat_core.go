package llm

import (
	"context"
	"errors"
	"fmt"

	openai "github.com/sashabaranov/go-openai"
)

// OpenAICompatConfig 描述一个 OpenAI 兼容 provider 的连接参数。
//
// APIKey 字段保留是为了配置层面的向后兼容；NewOpenAICompat 不再读它——
// 底层 *openai.Client 已带 apikey，由调用方从 ClientPool 注入。
type OpenAICompatConfig struct {
	BaseURL        string // 如 https://api.deepseek.com/v1 ；空 = OpenAI 官方
	Model          string // 如 deepseek-chat、qwen3-max
	APIKey         string // 仅用于配置传递；client 已经从 ClientPool 注入
	MaxTokens      int    // 0 = 走 provider 默认
	SupportsVision bool   // true=image_url 走 MultiContent 真识图；false=stripImagesToText 降级文本占位
}

// openAICompatGen 实现 Generator，跨 OpenAI 协议族复用同一份逻辑。
//
// 无状态：tools 不再构造时绑定，而是每次 Generate(ctx, msgs, tools) 动态传入，
// 修复 v1 Factory 缓存 Generator 导致跨 task tools 错乱的并发 bug。
type openAICompatGen struct {
	client         *openai.Client
	model          string
	maxTokens      int
	provider       string
	supportsVision bool // 透传自 OpenAICompatConfig.SupportsVision，控制 ContentParts 走 MultiContent vs 降级
}

// NewOpenAICompat 构造无状态 Generator。
//
// client 由调用方从 ClientPool 取（共享 HTTP 连接池）；tools 不在此绑定。
// providerKey 用作 Provider() / instrument 标记（如 "deepseek"）。
func NewOpenAICompat(_ context.Context, providerKey string, c OpenAICompatConfig, client *openai.Client) (Generator, error) {
	if client == nil {
		return nil, errors.New("OpenAICompat: client 必填（从 ClientPool 取）")
	}
	if c.Model == "" {
		return nil, errors.New("OpenAICompat: Model 必填")
	}
	return &openAICompatGen{
		client:         client,
		model:          c.Model,
		maxTokens:      c.MaxTokens,
		provider:       providerKey,
		supportsVision: c.SupportsVision,
	}, nil
}

// Provider / Model 满足 Generator。
func (g *openAICompatGen) Provider() string { return g.provider }
func (g *openAICompatGen) Model() string    { return g.model }

// Generate 发起一次 chat completion；tools 每次动态传入。
func (g *openAICompatGen) Generate(ctx context.Context, msgs []Message, tools []ToolSchema) (Result, error) {
	return g.GenerateWithOpts(ctx, msgs, tools, GenOpts{})
}

// GenerateWithOpts 实现 OptionsGenerator：按调用透传采样温度等可选参数。
func (g *openAICompatGen) GenerateWithOpts(ctx context.Context, msgs []Message, tools []ToolSchema, opts GenOpts) (Result, error) {
	// 前置校验 messages 格式，防止 API 400 错误
	if err := validateMessagesForAPI(msgs); err != nil {
		// 尝试降级修复
		msgs = truncateMessagesToFit(msgs, 20) // 保留最近 20 轮对话
		if err := validateMessagesForAPI(msgs); err != nil {
			return Result{}, fmt.Errorf("messages 校验失败且无法修复: %w", err)
		}
	}

	openaiMsgs, err := toOpenAIMessages(msgs, g.supportsVision)
	if err != nil {
		return Result{}, fmt.Errorf("convert messages: %w", err)
	}
	openaiTools, err := toOpenAITools(tools)
	if err != nil {
		return Result{}, fmt.Errorf("convert tools: %w", err)
	}

	req := openai.ChatCompletionRequest{
		Model:    g.model,
		Messages: openaiMsgs,
	}
	if g.maxTokens > 0 {
		req.MaxTokens = g.maxTokens
	}
	if len(openaiTools) > 0 {
		req.Tools = openaiTools
	}
	if opts.Temperature != nil {
		req.Temperature = float32(*opts.Temperature)
	}

	resp, err := g.client.CreateChatCompletion(ctx, req)
	if err != nil {
		// 增强错误信息：消息数量 + roles 形状摘要，便于远程排障（4xx 多为序列形状问题）
		return Result{}, fmt.Errorf("openai-compat generate (provider=%s, model=%s, msg_count=%d, tool_count=%d, shape=%s): %w",
			g.provider, g.model, len(openaiMsgs), len(openaiTools), messageShape(openaiMsgs), wrapOpenAICompatErr(err))
	}
	if len(resp.Choices) == 0 {
		return Result{}, fmt.Errorf("openai-compat generate: 0 choices returned")
	}
	return fromOpenAIResponse(resp, g.provider, g.model), nil
}

// messageShape 把消息序列压成紧凑形状摘要（如 "sys,user,a(2),t,t"），
// 随 4xx 错误输出——远程排障时不用回放就能看出序列形状。
