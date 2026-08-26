// Package provider — Anthropic 适配器。
//
// 用 anthropic-sdk-go 原生协议，不走 OpenAI 兼容层。
// 支持 Complete / Stream / CountTokens 三个方法。
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
)

const anthropicDefaultMaxTokens = 8192

type anthropicProvider struct {
	client    *anthropic.Client
	model     anthropic.Model
	maxTokens int64
}

// NewAnthropic 构造 Anthropic Provider。client 由调用方从连接池注入。
func NewAnthropic(client *anthropic.Client, model string, maxTokens int) (Provider, error) {
	if client == nil {
		return nil, errors.New("provider/anthropic: client 必填")
	}
	if model == "" {
		return nil, errors.New("provider/anthropic: model 必填")
	}
	mt := maxTokens
	if mt <= 0 {
		mt = anthropicDefaultMaxTokens
	}
	return &anthropicProvider{client: client, model: anthropic.Model(model), maxTokens: int64(mt)}, nil
}

func (p *anthropicProvider) ProviderID() string { return "anthropic" }
func (p *anthropicProvider) ModelID() string    { return string(p.model) }

// Complete 发起单次非流式调用。
func (p *anthropicProvider) Complete(ctx context.Context, req Request) (Response, error) {
	sysBlocks, msgs, err := toAnthropicMessages(req.Messages)
	if err != nil {
		return Response{}, fmt.Errorf("provider/anthropic complete: %w", err)
	}
	tools, err := toAnthropicTools(req.Tools)
	if err != nil {
		return Response{}, fmt.Errorf("provider/anthropic complete tools: %w", err)
	}

	maxTok := p.maxTokens
	if req.MaxTokens > 0 {
		maxTok = int64(req.MaxTokens)
	}

	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: maxTok,
		Messages:  msgs,
	}
	if len(sysBlocks) > 0 {
		params.System = sysBlocks
	}
	if len(tools) > 0 {
		params.Tools = tools
	}

	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return Response{}, wrapAnthropicErr(err)
	}
	return fromAnthropicResponse(resp), nil
}

// Stream 发起流式调用，逐事件推送到返回的通道。
// 使用 Accumulate 模式：每收到事件更新 acc Message，流结束后提取最终状态。
func (p *anthropicProvider) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	sysBlocks, msgs, err := toAnthropicMessages(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("provider/anthropic stream: %w", err)
	}
	tools, err := toAnthropicTools(req.Tools)
	if err != nil {
		return nil, fmt.Errorf("provider/anthropic stream tools: %w", err)
	}

	maxTok := p.maxTokens
	if req.MaxTokens > 0 {
		maxTok = int64(req.MaxTokens)
	}

	params := anthropic.MessageNewParams{
		Model:     p.model,
		MaxTokens: maxTok,
		Messages:  msgs,
	}
	if len(sysBlocks) > 0 {
		params.System = sysBlocks
	}
	if len(tools) > 0 {
		params.Tools = tools
	}

	stream := p.client.Messages.NewStreaming(ctx, params)

	ch := make(chan StreamEvent, 32)
	go func() {
		defer close(ch)
		defer stream.Close()

		var acc anthropic.Message
		// 积累 tool call 的 arguments（按 index）
		toolArgs := map[int64][]byte{}

		emit := func(e StreamEvent) bool {
			select {
			case ch <- e:
				return true
			case <-ctx.Done():
				return false
			}
		}

		for stream.Next() {
			event := stream.Current()

			// 用 SDK Accumulate 更新最终 Message（用于拿 usage）
			_ = acc.Accumulate(event)

			switch ev := event.AsAny().(type) {
			case anthropic.ContentBlockDeltaEvent:
				switch d := ev.Delta.AsAny().(type) {
				case anthropic.TextDelta:
					if d.Text != "" {
						if !emit(StreamEvent{Kind: StreamText, Content: d.Text}) {
							return
						}
					}
				case anthropic.ThinkingDelta:
					if d.Thinking != "" {
						if !emit(StreamEvent{Kind: StreamThinking, Content: d.Thinking}) {
							return
						}
					}
				case anthropic.InputJSONDelta:
					toolArgs[ev.Index] = append(toolArgs[ev.Index], []byte(d.PartialJSON)...)
				}
			case anthropic.ContentBlockStartEvent:
				switch b := ev.ContentBlock.AsAny().(type) {
				case anthropic.ToolUseBlock:
					toolArgs[ev.Index] = []byte{}
					_ = b // 名称/ID 在 stop 时从 acc.Content 取
				}
			case anthropic.ContentBlockStopEvent:
				// 若这个 block 是 tool_use，从 acc.Content 取 ID/Name
				if int(ev.Index) < len(acc.Content) {
					if tu, ok := acc.Content[ev.Index].AsAny().(anthropic.ToolUseBlock); ok {
						args := toolArgs[ev.Index]
						if len(args) == 0 {
							args = []byte("{}")
						}
						tc := &ToolCall{ID: tu.ID, Name: tu.Name, Arguments: json.RawMessage(args)}
						delete(toolArgs, ev.Index)
						if !emit(StreamEvent{Kind: StreamToolCall, Tool: tc}) {
							return
						}
					}
				}
			}
		}

		if err := stream.Err(); err != nil {
			emit(StreamEvent{Kind: StreamError, Err: wrapAnthropicErr(err)})
			return
		}

		u := Usage{
			InTokens:     int(acc.Usage.InputTokens),
			OutTokens:    int(acc.Usage.OutputTokens),
			CachedTokens: int(acc.Usage.CacheReadInputTokens),
		}
		emit(StreamEvent{Kind: StreamDone, Usage: &u})
	}()

	return ch, nil
}

// CountTokens 使用 Anthropic 专用端点精确计算 token 数。
func (p *anthropicProvider) CountTokens(ctx context.Context, req Request) (int, error) {
	sysBlocks, msgs, err := toAnthropicMessages(req.Messages)
	if err != nil {
		return 0, fmt.Errorf("provider/anthropic count tokens: %w", err)
	}
	tools, err := toAnthropicTools(req.Tools)
	if err != nil {
		return 0, fmt.Errorf("provider/anthropic count tokens tools: %w", err)
	}

	params := anthropic.MessageCountTokensParams{
		Model:    p.model,
		Messages: msgs,
	}
	if len(sysBlocks) > 0 {
		params.System = anthropic.MessageCountTokensParamsSystemUnion{OfTextBlockArray: sysBlocks}
	}
	if len(tools) > 0 {
		ctTools := make([]anthropic.MessageCountTokensToolUnionParam, len(tools))
		for i, t := range tools {
			ctTools[i] = anthropic.MessageCountTokensToolUnionParam{OfTool: t.OfTool}
		}
		params.Tools = ctTools
	}

	resp, err := p.client.Messages.CountTokens(ctx, params)
	if err != nil {
		return 0, wrapAnthropicErr(err)
	}
	return int(resp.InputTokens), nil
}

// ─────────────────────────────────────────────
//  消息格式转换
// ─────────────────────────────────────────────

func toAnthropicMessages(msgs []Message) (
	sysBlocks []anthropic.TextBlockParam,
	out []anthropic.MessageParam,
	err error,
) {
	for _, m := range msgs {
		switch m.Role {
		case RoleSystem:
			sysBlocks = append(sysBlocks, anthropic.TextBlockParam{Text: m.Content})
		case RoleUser:
			if len(m.Parts) > 0 {
				blocks, perr := toAnthropicUserParts(m.Parts)
				if perr != nil {
					return nil, nil, perr
				}
				out = append(out, anthropic.NewUserMessage(blocks...))
			} else {
				out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
			}
		case RoleAssistant:
			blocks := make([]anthropic.ContentBlockParamUnion, 0, 1+len(m.ToolCalls))
			if m.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			for _, tc := range m.ToolCalls {
				args := json.RawMessage(tc.Arguments)
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, args, tc.Name))
			}
			if len(blocks) > 0 {
				out = append(out, anthropic.NewAssistantMessage(blocks...))
			}
		case RoleTool:
			if len(m.Parts) > 0 {
				toolBlocks, perr := toAnthropicToolResultParts(m.Parts)
				if perr != nil {
					return nil, nil, perr
				}
				out = append(out, anthropic.NewUserMessage(anthropic.ContentBlockParamUnion{
					OfToolResult: &anthropic.ToolResultBlockParam{
						ToolUseID: m.ToolCallID,
						Content:   toolBlocks,
					},
				}))
			} else {
				out = append(out, anthropic.NewUserMessage(
					anthropic.NewToolResultBlock(m.ToolCallID, m.Content, false),
				))
			}
		}
	}
	return sysBlocks, out, nil
}

func toAnthropicUserParts(parts []ContentPart) ([]anthropic.ContentBlockParamUnion, error) {
	out := make([]anthropic.ContentBlockParamUnion, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "text":
			out = append(out, anthropic.NewTextBlock(p.Text))
		case "image":
			if p.ImageData == nil {
				return nil, errors.New("provider/anthropic: image part missing data")
			}
			out = append(out, anthropic.NewImageBlockBase64(p.ImageData.MediaType, p.ImageData.Base64Data))
		default:
			return nil, fmt.Errorf("provider/anthropic: unknown content part type %q", p.Type)
		}
	}
	return out, nil
}

func toAnthropicToolResultParts(parts []ContentPart) ([]anthropic.ToolResultBlockParamContentUnion, error) {
	out := make([]anthropic.ToolResultBlockParamContentUnion, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "text":
			out = append(out, anthropic.ToolResultBlockParamContentUnion{
				OfText: &anthropic.TextBlockParam{Text: p.Text},
			})
		case "image":
			if p.ImageData == nil {
				return nil, errors.New("provider/anthropic: image part missing data")
			}
			out = append(out, anthropic.ToolResultBlockParamContentUnion{
				OfImage: &anthropic.ImageBlockParam{
					Source: anthropic.ImageBlockParamSourceUnion{
						OfBase64: &anthropic.Base64ImageSourceParam{
							Data:      p.ImageData.Base64Data,
							MediaType: anthropic.Base64ImageSourceMediaType(p.ImageData.MediaType),
						},
					},
				},
			})
		default:
			return nil, fmt.Errorf("provider/anthropic: unknown content part type %q", p.Type)
		}
	}
	return out, nil
}

func toAnthropicTools(schemas []ToolSchema) ([]anthropic.ToolUnionParam, error) {
	if len(schemas) == 0 {
		return nil, nil
	}
	out := make([]anthropic.ToolUnionParam, len(schemas))
	for i, s := range schemas {
		var props any
		var required []string
		if len(s.Parameters) > 0 {
			var raw struct {
				Properties any      `json:"properties"`
				Required   []string `json:"required"`
			}
			if err := json.Unmarshal(s.Parameters, &raw); err != nil {
				return nil, fmt.Errorf("provider/anthropic: tool %q parameters: %w", s.Name, err)
			}
			props = raw.Properties
			required = raw.Required
		}
		tp := &anthropic.ToolParam{
			Name: s.Name,
			InputSchema: anthropic.ToolInputSchemaParam{
				Type:       "object",
				Properties: props,
				Required:   required,
			},
		}
		if s.Description != "" {
			tp.Description = anthropic.String(s.Description)
		}
		out[i] = anthropic.ToolUnionParam{OfTool: tp}
	}
	return out, nil
}

func fromAnthropicResponse(resp *anthropic.Message) Response {
	var content string
	var toolCalls []ToolCall

	for _, block := range resp.Content {
		switch b := block.AsAny().(type) {
		case anthropic.TextBlock:
			content += b.Text
		case anthropic.ToolUseBlock:
			toolCalls = append(toolCalls, ToolCall{
				ID:        b.ID,
				Name:      b.Name,
				Arguments: b.Input,
			})
		}
	}

	u := Usage{
		InTokens:     int(resp.Usage.InputTokens),
		OutTokens:    int(resp.Usage.OutputTokens),
		CachedTokens: int(resp.Usage.CacheReadInputTokens),
	}

	reason := string(resp.StopReason)
	if reason == "" {
		reason = "stop"
	}

	return Response{
		Content:      content,
		ToolCalls:    toolCalls,
		Usage:        u,
		FinishReason: reason,
	}
}

func wrapAnthropicErr(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		return &HTTPError{Code: apiErr.StatusCode, Inner: err}
	}
	return err
}
