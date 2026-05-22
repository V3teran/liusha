// Package llm 的 Anthropic Generator：直接用 anthropics/anthropic-sdk-go 调
// `/v1/messages` 原生协议（不绕道 OpenAI compat），tool_use / tool_result 用 SDK 原生类型。
//
// 与 openai_compat.go 的关键差异：
//   - system 不能进 messages，单独走 MessageNewParams.System
//   - tool_result 必须放在 user role 的 content block 里
//   - 一次响应可能含多个 content block（text + tool_use 混合），需要遍历组合
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
)

// AnthropicConfig 描述 Anthropic 连接参数。
//
// APIKey 字段保留是为了配置层面的向后兼容；NewAnthropic 不再读它——
// 底层 *anthropic.Client 已带 apikey，由调用方从 ClientPool 注入。
type AnthropicConfig struct {
	BaseURL   string // 空 = 官方 https://api.anthropic.com
	Model     string // 如 claude-sonnet-4-6
	APIKey    string // 仅用于配置传递；client 已经从 ClientPool 注入
	MaxTokens int    // Anthropic 这个字段必填，0 时本包默认填 4096
}

const anthropicDefaultMaxTokens = 4096

// anthropicGen 实现 Generator。
//
// 无状态：tools 不再构造时绑定，而是每次 Generate(ctx, msgs, tools) 动态传入，
// 修复 v1 Factory 缓存 Generator 导致跨 task tools 错乱的并发 bug。
type anthropicGen struct {
	client    anthropic.Client
	model     anthropic.Model
	maxTokens int64
	provider  string
}

// NewAnthropic 构造无状态 Generator。
//
// client 由调用方从 ClientPool 取（共享 HTTP 连接池）；tools 不在此绑定。
func NewAnthropic(_ context.Context, providerKey string, c AnthropicConfig, client *anthropic.Client) (Generator, error) {
	if client == nil {
		return nil, errors.New("anthropic: client 必填（从 ClientPool 取）")
	}
	if c.Model == "" {
		return nil, errors.New("anthropic: model 必填")
	}
	maxTokens := c.MaxTokens
	if maxTokens <= 0 {
		maxTokens = anthropicDefaultMaxTokens
	}
	return &anthropicGen{
		client:    *client,
		model:     anthropic.Model(c.Model),
		maxTokens: int64(maxTokens),
		provider:  providerKey,
	}, nil
}

func (g *anthropicGen) Provider() string { return g.provider }
func (g *anthropicGen) Model() string    { return string(g.model) }

// Generate 发起一次 messages 调用；tools 每次动态传入。
func (g *anthropicGen) Generate(ctx context.Context, msgs []Message, tools []ToolSchema) (Result, error) {
	systemBlocks, anthropicMsgs, err := toAnthropicMessages(msgs)
	if err != nil {
		return Result{}, fmt.Errorf("convert messages: %w", err)
	}
	anthropicTools, err := toAnthropicTools(tools)
	if err != nil {
		return Result{}, fmt.Errorf("convert tools: %w", err)
	}

	req := anthropic.MessageNewParams{
		Model:     g.model,
		MaxTokens: g.maxTokens,
		Messages:  anthropicMsgs,
	}
	if len(systemBlocks) > 0 {
		req.System = systemBlocks
	}
	if len(anthropicTools) > 0 {
		req.Tools = anthropicTools
	}

	resp, err := g.client.Messages.New(ctx, req)
	if err != nil {
		return Result{}, fmt.Errorf("anthropic generate: %w", err)
	}
	return fromAnthropicResponse(resp, g.provider, string(g.model)), nil
}

// toAnthropicMessages 把内部 Message 转成 Anthropic 原生类型。
//
//	system 提取到外层 SystemBlocks（不能进 messages）；
//	user / assistant 各自转 MessageParam；
//	tool（即 ToolResult）必须包在 user role 的 ToolResultBlock 里。
//
// 不合并相邻 tool 消息（Anthropic 允许单 ToolResult 也允许聚合，单条更简单）。
func toAnthropicMessages(in []Message) ([]anthropic.TextBlockParam, []anthropic.MessageParam, error) {
	var systemBlocks []anthropic.TextBlockParam
	out := make([]anthropic.MessageParam, 0, len(in))

	for _, m := range in {
		switch m.Role {
		case RoleSystem:
			if m.Content != "" {
				systemBlocks = append(systemBlocks, anthropic.TextBlockParam{Text: m.Content})
			}
		case RoleUser:
			// 含图走 multipart blocks；否则走原 text-only 路径（兼容旧测试 + 性能）。
			if len(m.ContentParts) > 0 {
				blocks, err := contentPartsToAnthropicUserBlocks(m.ContentParts)
				if err != nil {
					return nil, nil, fmt.Errorf("user content parts: %w", err)
				}
				if len(blocks) > 0 {
					out = append(out, anthropic.NewUserMessage(blocks...))
				}
			} else if m.Content != "" {
				out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
			}
		case RoleAssistant:
			blocks := make([]anthropic.ContentBlockParamUnion, 0, 1+len(m.ToolCalls))
			if m.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			for _, tc := range m.ToolCalls {
				input := json.RawMessage(tc.Arguments)
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, input, tc.Name))
			}
			if len(blocks) > 0 {
				out = append(out, anthropic.NewAssistantMessage(blocks...))
			}
		case RoleTool:
			// 含图走 ToolResultBlockParam struct（NewToolResultBlock helper 只接 string content，
			// 没法传 image block）；否则走 helper 短路径。
			if len(m.ContentParts) > 0 {
				toolBlocks, err := contentPartsToAnthropicToolBlocks(m.ContentParts)
				if err != nil {
					return nil, nil, fmt.Errorf("tool content parts: %w", err)
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
		default:
			return nil, nil, fmt.Errorf("unsupported role: %q", m.Role)
		}
	}
	return systemBlocks, out, nil
}

// contentPartsToAnthropicUserBlocks 把内部 ContentPart[] 转成 Anthropic user message blocks。
// 用于 RoleUser 含图场景；与 contentPartsToAnthropicToolBlocks 命名对仗，区分 user/tool 上下文。
func contentPartsToAnthropicUserBlocks(parts []ContentPart) ([]anthropic.ContentBlockParamUnion, error) {
	out := make([]anthropic.ContentBlockParamUnion, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "text":
			if p.Text != "" {
				out = append(out, anthropic.NewTextBlock(p.Text))
			}
		case "image_url":
			if p.ImageURL == nil || p.ImageURL.Base64Data == "" {
				return nil, errors.New("image_url part: ImageURL/Base64Data 必填")
			}
			out = append(out, anthropic.NewImageBlockBase64(p.ImageURL.MediaType, p.ImageURL.Base64Data))
		default:
			return nil, fmt.Errorf("unsupported content part type: %q", p.Type)
		}
	}
	return out, nil
}

// contentPartsToAnthropicToolBlocks 转成 Anthropic ToolResultBlockParamContentUnion[]。
// 用于 RoleTool 含图场景（tool_result 子 content）；与 contentPartsToAnthropicUserBlocks 命名对仗。
func contentPartsToAnthropicToolBlocks(parts []ContentPart) ([]anthropic.ToolResultBlockParamContentUnion, error) {
	out := make([]anthropic.ToolResultBlockParamContentUnion, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "text":
			if p.Text != "" {
				out = append(out, anthropic.ToolResultBlockParamContentUnion{
					OfText: &anthropic.TextBlockParam{Text: p.Text},
				})
			}
		case "image_url":
			if p.ImageURL == nil || p.ImageURL.Base64Data == "" {
				return nil, errors.New("image_url part: ImageURL/Base64Data 必填")
			}
			out = append(out, anthropic.ToolResultBlockParamContentUnion{
				OfImage: &anthropic.ImageBlockParam{
					Source: anthropic.ImageBlockParamSourceUnion{
						OfBase64: &anthropic.Base64ImageSourceParam{
							Data:      p.ImageURL.Base64Data,
							MediaType: anthropic.Base64ImageSourceMediaType(p.ImageURL.MediaType),
						},
					},
				},
			})
		default:
			return nil, fmt.Errorf("unsupported content part type: %q", p.Type)
		}
	}
	return out, nil
}

// toAnthropicTools 把 ToolSchema 转成 Anthropic ToolUnionParam（OfTool 路径）。
//
// 关键：Anthropic 的 ToolInputSchemaParam 预设 type=object，
// 把我们 ToolSchema.Parameters（完整 JSON Schema）拆出 properties + required 即可。
func toAnthropicTools(tools []ToolSchema) ([]anthropic.ToolUnionParam, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	out := make([]anthropic.ToolUnionParam, len(tools))
	for i, t := range tools {
		schema := struct {
			Properties any      `json:"properties"`
			Required   []string `json:"required"`
		}{}
		if len(t.Parameters) > 0 {
			if err := json.Unmarshal(t.Parameters, &schema); err != nil {
				return nil, fmt.Errorf("tool %s parameters: %w", t.Name, err)
			}
		}
		input := anthropic.ToolInputSchemaParam{
			Properties: schema.Properties,
			Required:   schema.Required,
		}
		tp := &anthropic.ToolParam{
			Name:        t.Name,
			InputSchema: input,
		}
		if t.Description != "" {
			tp.Description = anthropic.String(t.Description)
		}
		out[i] = anthropic.ToolUnionParam{OfTool: tp}
	}
	return out, nil
}

// fromAnthropicResponse 把 Message（响应）转成内部 Result。
func fromAnthropicResponse(resp *anthropic.Message, provider, model string) Result {
	res := Result{
		FinishReason: string(resp.StopReason),
		Provider:     provider,
		Model:        model,
		Usage: Usage{
			InTokens:     int(resp.Usage.InputTokens),
			OutTokens:    int(resp.Usage.OutputTokens),
			CachedTokens: int(resp.Usage.CacheReadInputTokens),
		},
	}

	var contentBuilder []byte
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				if len(contentBuilder) > 0 {
					contentBuilder = append(contentBuilder, '\n')
				}
				contentBuilder = append(contentBuilder, block.Text...)
			}
		case "tool_use":
			args := block.Input
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			res.ToolCalls = append(res.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: args,
			})
		}
	}
	res.Content = string(contentBuilder)
	return res
}
