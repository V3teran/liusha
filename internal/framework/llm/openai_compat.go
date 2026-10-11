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
	"strings"

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
func messageShape(msgs []openai.ChatCompletionMessage) string {
	parts := make([]string, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case openai.ChatMessageRoleSystem:
			parts = append(parts, "sys")
		case openai.ChatMessageRoleUser:
			parts = append(parts, "user")
		case openai.ChatMessageRoleAssistant:
			if len(m.ToolCalls) > 0 {
				parts = append(parts, fmt.Sprintf("a(%d)", len(m.ToolCalls)))
			} else {
				parts = append(parts, "a")
			}
		case openai.ChatMessageRoleTool:
			parts = append(parts, "t")
		default:
			parts = append(parts, m.Role)
		}
	}
	const maxParts = 40
	if len(parts) > maxParts {
		parts = append(parts[:maxParts], "…")
	}
	return strings.Join(parts, ",")
}

// validateMessagesForAPI 校验 messages 数组是否符合 OpenAI 协议规范（GLM 等严格实现）。
//
// 检查规则：// 1. 不允许连续的 assistant 消息（会触发 GLM "messages 参数非法"）
// 2. assistant 含 tool_calls 时，后续必须跟对应数量的 tool 消息
// 3. tool 消息必须有 tool_call_id 且与前面的 tool_call.id 匹配
func validateMessagesForAPI(msgs []Message) error {
	if len(msgs) == 0 {
		return nil
	}

	// 跟踪待回应的 tool_call IDs
	pendingToolCallIDs := make(map[string]bool)

	// 规则 4：首条非 system 消息必须是 user（部分严格实现要求对话从用户轮开始，
	// 历史压缩把窗口起点对齐到 assistant 时会触发 400 "messages 参数非法"）
	for _, m := range msgs {
		if m.Role == RoleSystem {
			continue
		}
		if m.Role != RoleUser {
			return fmt.Errorf("首条非 system 消息是 %s（要求 user）——严格实现会 400", m.Role)
		}
		break
	}

	for i, m := range msgs {
		// 规则 1：检查连续 assistant
		if i > 0 && m.Role == RoleAssistant && msgs[i-1].Role == RoleAssistant {
			return fmt.Errorf("message[%d]: 不允许连续 assistant 消息（会触发 GLM API 400）", i)
		}

		// 规则 2：assistant 有 tool_calls 时，记录待回应的 IDs
		if m.Role == RoleAssistant && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				if tc.ID == "" {
					return fmt.Errorf("message[%d]: tool_call 缺少 ID", i)
				}
				pendingToolCallIDs[tc.ID] = true
			}
		}

		// 规则 3：tool 消息必须匹配前面的 tool_call_id
		if m.Role == RoleTool {
			if m.ToolCallID == "" {
				return fmt.Errorf("message[%d]: tool 消息缺少 tool_call_id", i)
			}
			if !pendingToolCallIDs[m.ToolCallID] {
				return fmt.Errorf("message[%d]: tool_call_id=%s 没有对应的 tool_call", i, m.ToolCallID)
			}
			delete(pendingToolCallIDs, m.ToolCallID)
		}
	}

	// 未回应的 tool_calls（末尾 assistant 带 tool_calls 但结果未追加）是合法的
	// 中间构建状态，不判错——调用方随后会补上 tool 消息。

	return nil
}

// truncateMessagesToFit 截断 messages 保留最近 N 轮对话（降级策略）。
//
// 保留逻辑：
// - system 消息永远保留
// - 保留最近 maxRounds 轮 user/assistant/tool 交互
// - 确保 tool_calls 和 tool 消息配对完整（关键修复：向前扫描找到完整的 assistant.tool_calls）
func truncateMessagesToFit(msgs []Message, maxRounds int) []Message {
	if len(msgs) <= maxRounds*2 {
		return msgs
	}

	// 分离 system 和对话消息
	var systemMsgs []Message
	var conversationMsgs []Message

	for _, m := range msgs {
		if m.Role == RoleSystem {
			systemMsgs = append(systemMsgs, m)
		} else {
			conversationMsgs = append(conversationMsgs, m)
		}
	}

	// 保留最近的对话消息（确保 tool_calls 完整）
	startIdx := len(conversationMsgs) - maxRounds*2
	if startIdx < 0 {
		startIdx = 0
	}

	// 向前扫描，找到一个安全的截断点：
	// 1. 不能从 tool 消息开始（必须包含它对应的 assistant.tool_calls）
	// 2. 找到最近的一个 assistant 消息（有 tool_calls）或 user 消息作为起点
	for startIdx > 0 {
		msg := conversationMsgs[startIdx]

		// 如果是 tool 消息，必须向前找到对应的 assistant.tool_calls
		if msg.Role == RoleTool {
			startIdx--
			continue
		}

		// 如果是 assistant 且有 tool_calls，检查后面是否有对应的 tool 消息
		if msg.Role == RoleAssistant && len(msg.ToolCalls) > 0 {
			// 收集这个 assistant 的所有 tool_call IDs
			toolCallIDs := make(map[string]bool)
			for _, tc := range msg.ToolCalls {
				toolCallIDs[tc.ID] = true
			}

			// 向后扫描，检查是否所有 tool_call 都有对应的 tool 消息
			allMatched := true
			for j := startIdx + 1; j < len(conversationMsgs); j++ {
				if conversationMsgs[j].Role == RoleTool {
					delete(toolCallIDs, conversationMsgs[j].ToolCallID)
				} else if conversationMsgs[j].Role == RoleAssistant {
					// 遇到下一个 assistant，停止检查
					break
				}
			}

			// 如果还有未匹配的 tool_call，说明截断点在 tool_calls 中间，需要继续向前
			if len(toolCallIDs) > 0 {
				allMatched = false
			}

			if allMatched {
				break // 找到了安全的截断点
			}
		}

		// user 消息总是安全的截断点
		if msg.Role == RoleUser {
			break
		}

		startIdx--
	}

	result := make([]Message, 0, len(systemMsgs)+len(conversationMsgs)-startIdx)
	result = append(result, systemMsgs...)
	result = append(result, conversationMsgs[startIdx:]...)

	return result
}

// toOpenAIMessages 把内部 Message 转成 sashabaranov ChatCompletionMessage。
//
// Role 直接映射（system/user/assistant/tool）；assistant 的 tool_calls 与
// tool 的 tool_call_id 都按 OpenAI 协议保留。
//
// supportsVision 控制 ContentParts 的处理：
//   - true（gpt-4o / qwen3-vl 等真支持 vision 的 OpenAI 协议族）：走 MultiContent 数组路径，
//     image_url part 用 base64 data URI 内联，LLM 真正激活 vision encoder。
//   - false（deepseek-chat / qwen2.5 等纯文本 provider）：合并 text part、丢弃 image_url，
//     合并所有 text part、把 image_url 换成占位文本，LLM 知道这里曾截过图但当前看不到。
//
// DeepSeek 兼容性：assistant + tool_calls 但 content 空时，DeepSeek 严格校验
// 会报 "missing field content" 400。OpenAI 的 ChatCompletionMessage.Content 是
// `omitempty`，空 string 会被序列化器省略 → 这里在该场景下塞一个空格保留字段。
// MultiContent 路径下 SDK 强制 Content/MultiContent 互斥（ErrContentFieldsMisused），
// 因此走 MultiContent 时 Content 必须空——vision provider 不撞 DeepSeek 兼容场景。
func toOpenAIMessages(in []Message, supportsVision bool) ([]openai.ChatCompletionMessage, error) {
	// 前置验证：防止非法 messages 导致 API 400 错误
	if len(in) == 0 {
		return nil, fmt.Errorf("messages 不能为空")
	}
	for i, m := range in {
		// 验证 role 非空
		if m.Role == "" {
			return nil, fmt.Errorf("message[%d]: role 不能为空", i)
		}
		// 验证 tool_calls 的 arguments 是合法 JSON
		for j, tc := range m.ToolCalls {
			if len(tc.Arguments) > 0 {
				var temp interface{}
				if err := json.Unmarshal(tc.Arguments, &temp); err != nil {
					return nil, fmt.Errorf("message[%d].tool_calls[%d]: arguments 不是合法 JSON: %w", i, j, err)
				}
			}
		}
	}

	out := make([]openai.ChatCompletionMessage, 0, len(in))

	// pendingImages 累积 tool 结果里的图片块，待 flush 成一条 user message。
	// 为什么图不能留在 tool message：OpenAI 协议规定 tool role 的 content 必须是 string，
	// 不接受 image multipart（严格实现如 GLM 等报 400 Param Incorrect；部分宽容实现才没暴露）。
	// 全 provider 通用做法是把图放 user role（user 图文混合是唯一无争议都支持的形态）。
	var pendingImages []openai.ChatMessagePart
	flushImages := func() {
		if len(pendingImages) == 0 {
			return
		}
		parts := make([]openai.ChatMessagePart, 0, 1+len(pendingImages))
		parts = append(parts, openai.ChatMessagePart{Type: openai.ChatMessagePartTypeText, Text: toolScreenshotNote})
		parts = append(parts, pendingImages...)
		out = append(out, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, MultiContent: parts})
		pendingImages = nil
	}

	for _, m := range in {
		// 进入非 tool message 前先 flush：OpenAI 协议要求 assistant.tool_calls 后必须紧跟
		// 全部对应 tool message，再接其它 role——并行 tool calls 时不能在 tool 之间插 user。
		if m.Role != RoleTool {
			flushImages()
		}

		om := openai.ChatCompletionMessage{
			Role:       string(m.Role),
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		switch {
		case len(m.ContentParts) > 0 && supportsVision && m.Role == RoleTool:
			// tool 结果含图：文本留 tool message（保 tool_call_id 关联），图累积到 pendingImages
			// 待 flush 成 user message。纯文本 tool 结果（无图）走这里也只设 Content，不产生空 user。
			om.Content = textOnlyFromParts(m.ContentParts)
			imgs, err := imagePartsToOpenAI(m.ContentParts)
			if err != nil {
				return nil, fmt.Errorf("openai tool image: %w", err)
			}
			pendingImages = append(pendingImages, imgs...)
		case len(m.ContentParts) > 0 && supportsVision:
			// 非 tool role（如 user 含图）：MultiContent 合法，Content 必须空（SDK 互斥校验）。
			parts, err := contentPartsToOpenAIParts(m.ContentParts)
			if err != nil {
				return nil, fmt.Errorf("openai multipart: %w", err)
			}
			om.MultiContent = parts
		case len(m.ContentParts) > 0:
			// 降级：vision 不支持 → 把图换成占位文本，让 LLM 知道有图但看不到。
			om.Content = stripImagesToText(m.ContentParts)
		default:
			content := m.Content
			// 兼容 DeepSeek 等严格 OpenAI 协议实现：每条 message 必须含 content 字段（OpenAI 协议默认 omitempty）。
			// 涵盖：assistant+tool_calls 时 content 空 / tool_result Output 为空 / 其它边角空 content。
			// 特殊处理：如果这条消息将会有 tool_calls，不要设置空格（有些 provider 如 GLM 要求为空字符串）
			if content == "" && len(m.ToolCalls) == 0 {
				content = " "
			}
			om.Content = content
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
	// 会话以 tool message 收尾时（刚执行完工具、正要 LLM 看图回应），flush 末尾累积的图。
	flushImages()
	return out, nil
}

// openaiImageType 是 image_url part 的类型标识（供本包与测试断言共用）。
const openaiImageType = openai.ChatMessagePartTypeImageURL

// toolScreenshotNote 是承载 tool 截图的 user message 引导文本，避免 image-only multipart
// 在严格 provider 上的边角问题，并提示 LLM 这是上一步工具的视觉产物。
const toolScreenshotNote = "（上一步工具返回的截图）"

// textOnlyFromParts 抽取 ContentParts 里的文本拼成 string（丢弃图片块）。
// 全空时返回单空格——OpenAI 协议要求 tool message 含非空 content。
func textOnlyFromParts(parts []ContentPart) string {
	pieces := make([]string, 0, len(parts))
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			pieces = append(pieces, p.Text)
		}
	}
	if len(pieces) == 0 {
		return " "
	}
	return strings.Join(pieces, "\n")
}

// imagePartsToOpenAI 抽取 ContentParts 里的 image_url 块转成 OpenAI multipart（丢弃文本块）。
func imagePartsToOpenAI(parts []ContentPart) ([]openai.ChatMessagePart, error) {
	out := make([]openai.ChatMessagePart, 0, len(parts))
	for _, p := range parts {
		if p.Type != "image_url" {
			continue
		}
		if p.ImageURL == nil || p.ImageURL.Base64Data == "" {
			return nil, errors.New("image_url part: ImageURL/Base64Data 必填")
		}
		out = append(out, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeImageURL,
			ImageURL: &openai.ChatMessageImageURL{
				URL: "data:" + p.ImageURL.MediaType + ";base64," + p.ImageURL.Base64Data,
			},
		})
	}
	return out, nil
}

// contentPartsToOpenAIParts 把内部 ContentPart[] 转成 sashabaranov ChatMessagePart[]。
//
// 用户/工具 message 共用——OpenAI 协议下 user 和 tool role 的 multipart content 结构相同。
// image_url 用 data URI 内联（data:image/png;base64,...），无需外部文件服务、与沙箱无网依赖对齐。
func contentPartsToOpenAIParts(parts []ContentPart) ([]openai.ChatMessagePart, error) {
	out := make([]openai.ChatMessagePart, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "text":
			if p.Text == "" {
				continue
			}
			out = append(out, openai.ChatMessagePart{
				Type: openai.ChatMessagePartTypeText,
				Text: p.Text,
			})
		case "image_url":
			if p.ImageURL == nil || p.ImageURL.Base64Data == "" {
				return nil, errors.New("image_url part: ImageURL/Base64Data 必填")
			}
			out = append(out, openai.ChatMessagePart{
				Type: openai.ChatMessagePartTypeImageURL,
				ImageURL: &openai.ChatMessageImageURL{
					URL: "data:" + p.ImageURL.MediaType + ";base64," + p.ImageURL.Base64Data,
				},
			})
		default:
			return nil, fmt.Errorf("unsupported content part type: %q", p.Type)
		}
	}
	return out, nil
}

// stripImagesToText 把 ContentParts 降级成 OpenAI 兼容的 string content。
// text part 原样合并；image_url part 替换成占位文本（保留"有图但无法看"信号）。
// LLM 看到占位文本知道这里曾经有图——可决定换种验证方式（curl/state/eval）。
func stripImagesToText(parts []ContentPart) string {
	pieces := make([]string, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "text":
			if p.Text != "" {
				pieces = append(pieces, p.Text)
			}
		case "image_url":
			pieces = append(pieces, "[Image removed - model doesn't support vision]")
		}
	}
	return strings.Join(pieces, "\n")
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

// ─── StreamChat / CountTokens（Provider 桥接用可选能力） ───────────────────────

// StreamChat 流式生成：事件含 text 增量、聚合后的 tool_call、done（带 usage 估算）。
// maxTokens ≤ 0 时走构造时的配置默认。支持 ctx 取消（事件通道随当前事件后关闭）。
func (g *openAICompatGen) StreamChat(ctx context.Context, msgs []Message, tools []ToolSchema, maxTokens int) (<-chan StreamEvent, error) {
	// 1. 准备请求参数
	creq, err := prepareOpenAICompatRequest(msgs, tools, g.model, maxTokens, g.maxTokens, g.supportsVision)
	if err != nil {
		return nil, err
	}

	// 2. 创建流式请求
	stream, err := g.client.CreateChatCompletionStream(ctx, creq)
	if err != nil {
		return nil, wrapOpenAICompatErr(err)
	}

	// 3. 启动异步处理
	ch := make(chan StreamEvent, 32)
	go processOpenAICompatStream(ctx, stream, ch)

	return ch, nil
}

// CountTokens 本地估算输入 token 数（OpenAI 协议无专用计数端点）。
// 每 message 约 4 token overhead，文本按字符数估算；精度足够触发压缩决策。
func (g *openAICompatGen) CountTokens(_ context.Context, msgs []Message, tools []ToolSchema) (int, error) {
	total := 3
	for _, m := range msgs {
		total += 4
		total += estimateTokens(m.Content)
		for _, tc := range m.ToolCalls {
			total += 4
			total += estimateTokens(tc.Name)
			total += estimateTokens(string(tc.Arguments))
		}
		total += estimateTokens(m.ToolCallID)
	}
	for _, t := range tools {
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
	ascii, cjk := 0, 0
	for _, r := range s {
		if r > 0x2E7F {
			cjk++
		} else {
			ascii++
		}
	}
	return ascii/4 + cjk
}

// wrapOpenAICompatErr 把 SDK 的 *openai.APIError 归一成 *HTTPError（供重试分类读状态码）。
func wrapOpenAICompatErr(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		return &HTTPError{Code: apiErr.HTTPStatusCode, Inner: err}
	}
	return err
}
