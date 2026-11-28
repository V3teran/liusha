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
	"net/http"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
)

// AnthropicConfig 描述 Anthropic 连接参数。
// 密钥不在此结构——底层 *anthropic.Client 已带 apikey，由调用方从 ClientPool 注入。
type AnthropicConfig struct {
	BaseURL string // 空 = 官方 https://api.anthropic.com
	Model   string // 如 claude-sonnet-4-6
	// Limit 是这个账户上这个模型的输出上限（ClientPool.OutputLimitFor 构造）：协议要求 max_tokens
	// 必填，所以配置的值必须大于 0；被上游嫌大而拒绝时自动下调。
	Limit *OutputLimit
	// ThinkingBudget 是扩展思考预算（thinking.budget_tokens）。>0 时请求开启
	// 扩展思考：模型先流式产出 thinking block（内部 CoT），再产出正文/tool_use。
	// 0 = 不开启。必须 < MaxTokens。
	ThinkingBudget int64
}

// anthropicGen 实现 Generator。
//
// 无状态：tools 不再构造时绑定，而是每次 Generate(ctx, msgs, tools) 动态传入，
// 修复 v1 Factory 缓存 Generator 导致跨 task tools 错乱的并发 bug。
type anthropicGen struct {
	client         anthropic.Client
	model          anthropic.Model
	limit          *OutputLimit
	thinkingBudget int64
	provider       string
}

// NewAnthropic 构造无状态 Generator。
//
// client 由调用方从 ClientPool 取（共享 HTTP 连接池）；tools 不在此绑定。
// 输出上限必填且必须大于 0：协议要求 max_tokens 显式给出，框架不替调用方猜一个缺省——缺省只有
// 一个出处（业务侧 llmconfig.DefaultMaxTokens），在配置入口落定，这里拿不到值就是调用方的错。
func NewAnthropic(_ context.Context, providerCode string, c AnthropicConfig, client *anthropic.Client) (Generator, error) {
	if client == nil {
		return nil, errors.New("anthropic: client 必填（从 ClientPool 取）")
	}
	if c.Model == "" {
		return nil, errors.New("anthropic: model 必填")
	}
	if c.Limit == nil || c.Limit.configured <= 0 {
		return nil, errors.New("anthropic: 输出上限必填且必须大于 0（协议要求 max_tokens 显式给出，由调用方按 provider 配置给值：ClientPool.OutputLimitFor）")
	}
	if c.ThinkingBudget > 0 && c.ThinkingBudget >= int64(c.Limit.configured) {
		return nil, fmt.Errorf("anthropic: thinking budget %d 必须小于 max_tokens %d", c.ThinkingBudget, c.Limit.configured)
	}
	return &anthropicGen{
		client:         *client,
		model:          c.Model,
		limit:          c.Limit,
		thinkingBudget: c.ThinkingBudget,
		provider:       providerCode,
	}, nil
}

func (g *anthropicGen) Provider() string { return g.provider }
func (g *anthropicGen) Model() string    { return g.model }

// Generate 发起一次 messages 调用；tools 每次动态传入。
func (g *anthropicGen) Generate(ctx context.Context, msgs []Message, tools []ToolSchema) (Result, error) {
	return g.GenerateWithOptions(ctx, msgs, tools, GenerateOptions{})
}

// GenerateWithOptions 实现 OptionsGenerator：按调用透传采样温度等可选参数。
//
// 非流式接口也走流式再聚合：协议的非流式请求在服务端以 10 分钟为上限，输出上限一大（SDK 按
// 「1 小时 × 上限 ÷ 128000」估算超过 10 分钟）就不再接受非流式调用。适配器只保留流式这一条
// 链路——聚合出的结果与非流式同构，也免得两条链路各自出偏差。
func (g *anthropicGen) GenerateWithOptions(ctx context.Context, msgs []Message, tools []ToolSchema, opts GenerateOptions) (Result, error) {
	ch, err := g.stream(ctx, msgs, tools, opts.MaxTokens, opts.Temperature)
	if err != nil {
		return Result{}, err
	}
	resp, err := CollectStream(ctx, ch, nil, nil)
	if err != nil {
		return Result{}, fmt.Errorf("anthropic generate: %w", err)
	}
	return resultOf(resp, g.provider, g.model), nil
}

// toAnthropicMessages 把内部 Message 转成 Anthropic 原生类型。
//
//	system 提取到外层 SystemBlocks（不能进 messages）；
//	user / assistant 各自转 MessageParam；
//	tool（即 ToolResult）必须包在 user role 的 ToolResultBlock 里。
//
// 相邻的 tool 消息（并行工具调用的各个结果）合进同一条 user 消息：协议要求 assistant 的每个
// tool_use 都在紧随其后的那条消息里有对应的 tool_result，分成多条 user 消息时，严格实现协议
// 的端点会以「tool_use 没有紧随的 tool_result」拒绝请求。
func toAnthropicMessages(in []Message) ([]anthropic.TextBlockParam, []anthropic.MessageParam, error) {
	var systemBlocks []anthropic.TextBlockParam
	out := make([]anthropic.MessageParam, 0, len(in))

	// toolResults 是正在累积的那条 tool_result user 消息在 out 中的下标（-1 = 没有）。
	toolResults := -1
	for i, m := range in {
		if m.Role != RoleTool {
			toolResults = -1
		}
		switch m.Role {
		case RoleSystem:
			if m.Content != "" {
				systemBlocks = append(systemBlocks, anthropic.TextBlockParam{Text: m.Content})
			}
		case RoleUser:
			// 含图走 multipart blocks；纯文本走 text-only 路径（单 string content，载荷更小）。
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
			blocks := make([]anthropic.ContentBlockParamUnion, 0, 2+len(m.ToolCalls))
			// anthropic 规范：开启扩展思考的多轮 tool_use 中，assistant 消息须原样
			// 回传 thinking block（含 signature），且必须是首个 content block。
			if m.Thinking != nil && m.Thinking.Text != "" {
				blocks = append(blocks, anthropic.NewThinkingBlock(m.Thinking.Signature, m.Thinking.Text))
			}
			if m.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			for j, tc := range m.ToolCalls {
				input := tc.Arguments
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				// 参数原样嵌进请求体：非法 JSON 会让整个请求体非法（与 openai 协议同一边界校验）。
				if !json.Valid(input) {
					return nil, nil, fmt.Errorf("message[%d].tool_calls[%d]: arguments 不是合法 JSON", i, j)
				}
				blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, input, tc.Name))
			}
			if len(blocks) > 0 {
				out = append(out, anthropic.NewAssistantMessage(blocks...))
			}
		case RoleTool:
			// 含图走 ToolResultBlockParam struct（NewToolResultBlock helper 只接 string content，
			// 没法传 image block）；否则走 helper 短路径。
			var block anthropic.ContentBlockParamUnion
			if len(m.ContentParts) > 0 {
				toolBlocks, err := contentPartsToAnthropicToolBlocks(m.ContentParts)
				if err != nil {
					return nil, nil, fmt.Errorf("tool content parts: %w", err)
				}
				block = anthropic.ContentBlockParamUnion{OfToolResult: &anthropic.ToolResultBlockParam{
					ToolUseID: m.ToolCallID,
					Content:   toolBlocks,
				}}
			} else {
				block = anthropic.NewToolResultBlock(m.ToolCallID, m.Content, false)
			}
			if toolResults >= 0 {
				out[toolResults].Content = append(out[toolResults].Content, block)
			} else {
				out = append(out, anthropic.NewUserMessage(block))
				toolResults = len(out) - 1
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

// ─── StreamChat（Provider 桥接用可选能力） ──────────────────────────────────────

// StreamChat 流式生成：thinking/text 增量 + 聚合 tool_call + done（带 SDK Accumulate 的 usage）。
func (g *anthropicGen) StreamChat(ctx context.Context, msgs []Message, tools []ToolSchema, maxTokens int) (<-chan StreamEvent, error) {
	return g.stream(ctx, msgs, tools, maxTokens, nil)
}

// stream 建一条事件流。上游嫌输出上限过大时在处理协程里降一半重建（建流的错误要到首次读取
// 才出现）。temperature 只在不开启扩展思考时下发：该模式下协议只允许 1，不设即协议默认。
func (g *anthropicGen) stream(ctx context.Context, msgs []Message, tools []ToolSchema, maxTokens int, temperature *float64) (<-chan StreamEvent, error) {
	systemBlocks, anthropicMsgs, err := toAnthropicMessages(msgs)
	if err != nil {
		return nil, fmt.Errorf("convert messages: %w", err)
	}
	anthropicTools, err := toAnthropicTools(tools)
	if err != nil {
		return nil, fmt.Errorf("convert tools: %w", err)
	}

	limit := g.limit.forCall(maxTokens)
	ch := make(chan StreamEvent, 32)
	go processAnthropicStream(ctx, ch, g.limit, limit, func(limit int) *ssestream.Stream[anthropic.MessageStreamEventUnion] {
		params := anthropicRequest(systemBlocks, anthropicMsgs, anthropicTools, g.model, int64(limit), g.thinkingBudget)
		if g.thinkingBudget == 0 && temperature != nil {
			params.Temperature = anthropic.Float(*temperature)
		}
		return g.client.Messages.NewStreaming(ctx, params)
	})
	return ch, nil
}

// wrapAnthropicErr 把 SDK 的 *anthropic.Error 归一成 *HTTPError（供重试分类读状态码）。
// 限流响应一并带上恢复时刻：SDK 的错误对象持有原始响应，标准的 Retry-After 头优先，
// 其次是错误体里写明的重置时间。
func wrapAnthropicErr(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		return err
	}
	he := &HTTPError{Code: apiErr.StatusCode, Inner: err}
	if apiErr.StatusCode == http.StatusTooManyRequests && apiErr.Response != nil {
		he.RetryAt = retryAtOf(apiErr.Response.Header, apiErr.Error(), time.Now())
	}
	return he
}
