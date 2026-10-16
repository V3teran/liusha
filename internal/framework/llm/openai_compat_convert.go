package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

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
