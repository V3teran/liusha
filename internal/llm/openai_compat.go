// Package llm 的 OpenAI 兼容 Generator：直接用 sashabaranov/go-openai 调
// `/v1/chat/completions`，覆盖所有遵守 OpenAI 协议的 provider：
//
//	OpenAI / DeepSeek / Qwen（compatible-mode）/ Moonshot / Together / Groq / 智谱 / 豆包 / Yi /
//	Anyscale / Fireworks / OpenRouter ...
//
// 用户切 provider 只需配 base_url + model + api_key_env，无需代码改动。
//
// 工具调用走 OpenAI 原生 `tool_calls` 字段（不靠任何中间转换层），
// 因此 DeepSeek / Qwen 等返回 tool_calls 的稳定性等同 OpenAI 官方协议。
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	openai "github.com/sashabaranov/go-openai"
)

// OpenAICompatConfig 描述一个 OpenAI 兼容 provider 的连接参数。
//
// APIKey 字段保留是为了配置层面的向后兼容；NewOpenAICompat 不再读它——
// 底层 *openai.Client 已带 apikey，由调用方从 ClientPool 注入。
type OpenAICompatConfig struct {
	BaseURL   string // 如 https://api.deepseek.com/v1 ；空 = OpenAI 官方
	Model     string // 如 deepseek-chat、qwen3-max
	APIKey    string // 仅用于配置传递；client 已经从 ClientPool 注入
	MaxTokens int    // 0 = 走 provider 默认
}

// openAICompatGen 实现 Generator，跨 OpenAI 协议族复用同一份逻辑。
//
// 无状态：tools 不再构造时绑定，而是每次 Generate(ctx, msgs, tools) 动态传入，
// 修复 v1 Factory 缓存 Generator 导致跨 task tools 错乱的并发 bug。
type openAICompatGen struct {
	client    *openai.Client
	model     string
	maxTokens int
	provider  string
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
		client:    client,
		model:     c.Model,
		maxTokens: c.MaxTokens,
		provider:  providerKey,
	}, nil
}

// Provider / Model 满足 Generator。
func (g *openAICompatGen) Provider() string { return g.provider }
func (g *openAICompatGen) Model() string    { return g.model }

// Generate 发起一次 chat completion；tools 每次动态传入。
func (g *openAICompatGen) Generate(ctx context.Context, msgs []Message, tools []ToolSchema) (Result, error) {
	openaiMsgs, err := toOpenAIMessages(msgs)
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

	resp, err := g.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return Result{}, fmt.Errorf("openai-compat generate: %w", err)
	}
	if len(resp.Choices) == 0 {
		return Result{}, fmt.Errorf("openai-compat generate: 0 choices returned")
	}
	return fromOpenAIResponse(resp, g.provider, g.model), nil
}

// toOpenAIMessages 把内部 Message 转成 sashabaranov ChatCompletionMessage。
//
// Role 直接映射（system/user/assistant/tool）；assistant 的 tool_calls 与
// tool 的 tool_call_id 都按 OpenAI 协议保留。
//
// DeepSeek 兼容性：assistant + tool_calls 但 content 空时，DeepSeek 严格校验
// 会报 "missing field content" 400。OpenAI 的 ChatCompletionMessage.Content 是
// `omitempty`，空 string 会被序列化器省略 → 这里在该场景下塞一个空格保留字段。
func toOpenAIMessages(in []Message) ([]openai.ChatCompletionMessage, error) {
	out := make([]openai.ChatCompletionMessage, 0, len(in))
	for _, m := range in {
		content := m.Content
		// 兼容 DeepSeek 等严格 OpenAI 协议实现：每条 message 必须含 content 字段（OpenAI 协议默认 omitempty）。
		// 涵盖：assistant+tool_calls 时 content 空 / tool_result Output 为空 / 其它边角空 content。
		if content == "" {
			content = " "
		}
		om := openai.ChatCompletionMessage{
			Role:       string(m.Role),
			Content:    content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		if len(m.ToolCalls) > 0 {
			om.ToolCalls = make([]openai.ToolCall, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				om.ToolCalls[i] = openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      tc.Name,
						Arguments: string(tc.Arguments),
					},
				}
			}
		}
		out = append(out, om)
	}
	return out, nil
}

// toOpenAITools 把内部 ToolSchema 转成 OpenAI Tool。
//
// Parameters（JSON Schema 原样）直接塞给 FunctionDefinition.Parameters
// （any 字段，sashabaranov 会原样 JSON 编码）。
func toOpenAITools(tools []ToolSchema) ([]openai.Tool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	out := make([]openai.Tool, len(tools))
	for i, t := range tools {
		var params any
		if len(t.Parameters) > 0 {
			if err := json.Unmarshal(t.Parameters, &params); err != nil {
				return nil, fmt.Errorf("tool %s parameters: %w", t.Name, err)
			}
		}
		out[i] = openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
			},
		}
	}
	return out, nil
}

// fromOpenAIResponse 把 ChatCompletionResponse 转成内部 Result。
func fromOpenAIResponse(resp openai.ChatCompletionResponse, provider, model string) Result {
	choice := resp.Choices[0]
	res := Result{
		Content:      choice.Message.Content,
		FinishReason: string(choice.FinishReason),
		Provider:     provider,
		Model:        model,
		Usage: Usage{
			InTokens:  resp.Usage.PromptTokens,
			OutTokens: resp.Usage.CompletionTokens,
		},
	}
	if resp.Usage.PromptTokensDetails != nil {
		res.Usage.CachedTokens = resp.Usage.PromptTokensDetails.CachedTokens
	}
	for _, tc := range choice.Message.ToolCalls {
		res.ToolCalls = append(res.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}
	return res
}
