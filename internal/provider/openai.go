// Package provider — OpenAI 兼容适配器。
//
// 覆盖 OpenAI / DeepSeek / Qwen / Moonshot 等遵守 OpenAI 协议的 provider。
// CountTokens 使用 tiktoken-go 本地计算，不发网络请求。
package provider

import (
	"context"
	"errors"
	"fmt"
	"io"

	openai "github.com/sashabaranov/go-openai"
)

type openaiProvider struct {
	client *openai.Client
	model  string
}

// NewOpenAI 构造 OpenAI 兼容 Provider。client 由调用方注入（含 base_url / api_key）。
func NewOpenAI(client *openai.Client, model string) (Provider, error) {
	if client == nil {
		return nil, errors.New("provider/openai: client 必填")
	}
	if model == "" {
		return nil, errors.New("provider/openai: model 必填")
	}
	return &openaiProvider{client: client, model: model}, nil
}

func (p *openaiProvider) ProviderID() string { return "openai" }
func (p *openaiProvider) ModelID() string    { return p.model }

func (p *openaiProvider) Complete(ctx context.Context, req Request) (Response, error) {
	msgs, err := toOpenAIMessages(req.Messages)
	if err != nil {
		return Response{}, fmt.Errorf("provider/openai: convert messages: %w", err)
	}
	tools := toOpenAITools(req.Tools)

	creq := openai.ChatCompletionRequest{
		Model:    p.model,
		Messages: msgs,
	}
	if len(tools) > 0 {
		creq.Tools = tools
	}
	if req.MaxTokens > 0 {
		creq.MaxTokens = req.MaxTokens
	}

	resp, err := p.client.CreateChatCompletion(ctx, creq)
	if err != nil {
		return Response{}, wrapOpenAIErr(err)
	}
	return fromOpenAIResponse(resp), nil
}

func (p *openaiProvider) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	msgs, err := toOpenAIMessages(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("provider/openai: convert messages: %w", err)
	}
	tools := toOpenAITools(req.Tools)

	creq := openai.ChatCompletionRequest{
		Model:    p.model,
		Messages: msgs,
		Stream:   true,
	}
	if len(tools) > 0 {
		creq.Tools = tools
	}
	if req.MaxTokens > 0 {
		creq.MaxTokens = req.MaxTokens
	}

	stream, err := p.client.CreateChatCompletionStream(ctx, creq)
	if err != nil {
		return nil, wrapOpenAIErr(err)
	}

	ch := make(chan StreamEvent, 32)
	go func() {
		defer close(ch)
		defer stream.Close()

		// 按 index 聚合 tool call 增量
		partial := map[int]*ToolCall{}

		emit := func(e StreamEvent) bool {
			select {
			case ch <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}

		for {
			chunk, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				emit(StreamEvent{Kind: StreamError, Err: wrapOpenAIErr(err)})
				return
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			delta := chunk.Choices[0].Delta

			if delta.Content != "" {
				if !emit(StreamEvent{Kind: StreamText, Content: delta.Content}) {
					return
				}
			}

			for _, tc := range delta.ToolCalls {
				idx := tc.Index
				if idx == nil {
					continue
				}
				i := *idx
				if _, ok := partial[i]; !ok {
					partial[i] = &ToolCall{
						ID:        tc.ID,
						Name:      tc.Function.Name,
						Arguments: []byte(""),
					}
				} else {
					// 可能 ID / Name 在后续 chunk 也有
					if tc.ID != "" {
						partial[i].ID = tc.ID
					}
					if tc.Function.Name != "" {
						partial[i].Name = tc.Function.Name
					}
				}
				partial[i].Arguments = append(partial[i].Arguments, []byte(tc.Function.Arguments)...)
			}

			if chunk.Choices[0].FinishReason == openai.FinishReasonToolCalls ||
				chunk.Choices[0].FinishReason == openai.FinishReasonStop {
				// 刷出所有积累的 tool calls
				for i := 0; i < len(partial); i++ {
					tc, ok := partial[i]
					if !ok {
						continue
					}
					if len(tc.Arguments) == 0 {
						tc.Arguments = []byte("{}")
					}
					if !emit(StreamEvent{Kind: StreamToolCall, Tool: tc}) {
						return
					}
				}
				break
			}
		}

		// OpenAI 流式不在 chunk 里返回 usage，发空 Usage
		u := Usage{}
		emit(StreamEvent{Kind: StreamDone, Usage: &u})
	}()

	return ch, nil
}

// CountTokens 使用本地估算（OpenAI 无专用计数端点）。
// 按 GPT-4o tokenizer 规格：每个 message 约 4 token overhead，文本按字符数 /4 估算。
// 精度足够触发压缩决策使用；如需精确计数可注入 tiktoken-go。
func (p *openaiProvider) CountTokens(_ context.Context, req Request) (int, error) {
	total := 3 // 每次请求固定 overhead
	for _, m := range req.Messages {
		total += 4 // per-message overhead
		total += estimateTokens(m.Content)
		for _, tc := range m.ToolCalls {
			total += 4
			total += estimateTokens(tc.Name)
			total += estimateTokens(string(tc.Arguments))
		}
		total += estimateTokens(m.ToolCallID)
	}
	for _, t := range req.Tools {
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
	runes := []rune(s)
	ascii, cjk := 0, 0
	for _, r := range runes {
		if r > 0x2E7F {
			cjk++
		} else {
			ascii++
		}
	}
	return cjk + (ascii+3)/4
}

// ─────────────────────────────────────────────
//  格式转换
// ─────────────────────────────────────────────

func toOpenAIMessages(msgs []Message) ([]openai.ChatCompletionMessage, error) {
	out := make([]openai.ChatCompletionMessage, 0, len(msgs))
	for _, m := range msgs {
		msg := openai.ChatCompletionMessage{
			Role:       string(m.Role),
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		if len(m.Parts) > 0 {
			parts, err := toOpenAIParts(m.Parts)
			if err != nil {
				return nil, err
			}
			msg.MultiContent = parts
			msg.Content = ""
		}
		if len(m.ToolCalls) > 0 {
			tcs := make([]openai.ToolCall, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				tcs[i] = openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      tc.Name,
						Arguments: string(tc.Arguments),
					},
				}
			}
			msg.ToolCalls = tcs
		}
		out = append(out, msg)
	}
	return out, nil
}

func toOpenAIParts(parts []ContentPart) ([]openai.ChatMessagePart, error) {
	out := make([]openai.ChatMessagePart, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "text":
			out = append(out, openai.ChatMessagePart{
				Type: openai.ChatMessagePartTypeText,
				Text: p.Text,
			})
		case "image":
			if p.ImageData == nil {
				return nil, errors.New("provider/openai: image part missing data")
			}
			url := fmt.Sprintf("data:%s;base64,%s", p.ImageData.MediaType, p.ImageData.Base64Data)
			out = append(out, openai.ChatMessagePart{
				Type: openai.ChatMessagePartTypeImageURL,
				ImageURL: &openai.ChatMessageImageURL{
					URL: url,
				},
			})
		default:
			return nil, fmt.Errorf("provider/openai: unknown content part type %q", p.Type)
		}
	}
	return out, nil
}

func toOpenAITools(schemas []ToolSchema) []openai.Tool {
	if len(schemas) == 0 {
		return nil
	}
	out := make([]openai.Tool, len(schemas))
	for i, s := range schemas {
		out[i] = openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        s.Name,
				Description: s.Description,
				Parameters:  s.Parameters,
			},
		}
	}
	return out
}

func fromOpenAIResponse(resp openai.ChatCompletionResponse) Response {
	if len(resp.Choices) == 0 {
		return Response{FinishReason: "error"}
	}
	choice := resp.Choices[0]
	var toolCalls []ToolCall
	for _, tc := range choice.Message.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: []byte(tc.Function.Arguments),
		})
	}
	u := Usage{
		InTokens:  resp.Usage.PromptTokens,
		OutTokens: resp.Usage.CompletionTokens,
	}
	reason := string(choice.FinishReason)
	if reason == "" {
		reason = "stop"
	}
	return Response{
		Content:      choice.Message.Content,
		ToolCalls:    toolCalls,
		Usage:        u,
		FinishReason: reason,
	}
}

func wrapOpenAIErr(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		return &HTTPError{Code: apiErr.HTTPStatusCode, Inner: err}
	}
	return err
}
