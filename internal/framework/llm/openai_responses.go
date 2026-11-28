package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIResponsesConfig 描述一个 OpenAI Responses 协议 provider 的连接参数。密钥不在此结构——
// 底层 *http.Client 的传输层已带凭据（ClientPool.GetOrCreateHTTP）。
type OpenAIResponsesConfig struct {
	BaseURL string // 如 https://api.example.com/v1 ；对话请求发到它后面的 /responses
	Model   string
	// Limit 是这个账户上这个模型的输出上限（ClientPool.OutputLimitFor 构造）：被上游嫌大而拒绝时
	// 自动下调。必填——单次输出上限是 provider 配置里的事实，适配器不替它猜。
	Limit *OutputLimit
	// ReasoningEffort 是思考深度，经 reasoning.effort 下发；空 = 不下发。
	ReasoningEffort ReasoningEffort
	SupportsVision  bool // true=图片按 input_image 下发；false=降级为占位文本
}

// openAIResponsesGen 实现 Generator。不经 SDK：这个协议的事件名是 response.<…>.<…> 一族，
// 通用 SSE 解码器逐事件读、按事件体里的 type 分派即可，请求与应答的形状由下面的类型如实描述。
type openAIResponsesGen struct {
	client          *http.Client
	baseURL         string
	model           string
	provider        string
	limit           *OutputLimit
	reasoningEffort ReasoningEffort
	supportsVision  bool
}

// NewOpenAIResponses 构造无状态 Generator。client 由调用方从 ClientPool 取（带凭据、共享连接池）。
func NewOpenAIResponses(_ context.Context, providerCode string, c OpenAIResponsesConfig, client *http.Client) (Generator, error) {
	if client == nil {
		return nil, errors.New("OpenAI Responses: client 必填（从 ClientPool 取）")
	}
	if c.Model == "" {
		return nil, errors.New("OpenAI Responses: Model 必填")
	}
	if strings.TrimSpace(c.BaseURL) == "" {
		return nil, errors.New("OpenAI Responses: Base URL 必填")
	}
	if c.Limit == nil {
		return nil, errors.New("OpenAI Responses: 输出上限必填（ClientPool.OutputLimitFor）")
	}
	if !c.ReasoningEffort.Valid() {
		return nil, c.ReasoningEffort.invalidErr("OpenAI Responses")
	}
	return &openAIResponsesGen{
		client:          client,
		baseURL:         strings.TrimRight(c.BaseURL, "/"),
		model:           c.Model,
		provider:        providerCode,
		limit:           c.Limit,
		reasoningEffort: c.ReasoningEffort,
		supportsVision:  c.SupportsVision,
	}, nil
}

func (g *openAIResponsesGen) Provider() string { return g.provider }
func (g *openAIResponsesGen) Model() string    { return g.model }

// ── 请求 ─────────────────────────────────────────────────────────────────────

// responsesRequest 是创建一次响应的请求体。
//
// store=false、include 加密推理内容：liusha 自己管对话状态，每次都传完整历史，不在服务端留存。
// 推理模型的多轮工具调用要求把推理项随其后的调用项一起传回——不留存时推理内容只能以加密
// 形式带在应答里，下一轮原样放回输入。
type responsesRequest struct {
	Model           string              `json:"model"`
	Input           []json.RawMessage   `json:"input"`
	MaxOutputTokens int                 `json:"max_output_tokens,omitempty"`
	Temperature     *float64            `json:"temperature,omitempty"`
	Stream          bool                `json:"stream,omitempty"`
	Store           bool                `json:"store"`
	Include         []string            `json:"include"`
	Tools           []responsesTool     `json:"tools,omitempty"`
	Reasoning       *responsesReasoning `json:"reasoning,omitempty"`
}

// responsesTool 是函数工具：name / description / parameters 平铺，不嵌套在 function 下。
type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

type responsesReasoning struct {
	Effort ReasoningEffort `json:"effort"`
}

// 输入项按 type 判别：消息、函数调用、函数调用结果、推理项。
const (
	responsesItemMessage    = "message"
	responsesItemCall       = "function_call"
	responsesItemCallOutput = "function_call_output"
	responsesItemReasoning  = "reasoning"

	responsesPartInputText  = "input_text"
	responsesPartOutputText = "output_text"
	responsesPartInputImage = "input_image"

	responsesIncludeEncryptedReasoning = "reasoning.encrypted_content"
)

// responsesMessageItem 是一条输入消息：content 是字符串（纯文本）或内容块数组（带图）。
type responsesMessageItem struct {
	Type    string          `json:"type"`
	Role    MessageRole     `json:"role"`
	Content json.RawMessage `json:"content"`
}

// responsesContentPart 是消息的一个内容块：文本或图片（图片用 data URI 内联，不依赖外部文件服务）。
type responsesContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type responsesCall struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type responsesCallOutput struct {
	Type   string          `json:"type"`
	CallID string          `json:"call_id"`
	Output json.RawMessage `json:"output"`
}

// 未等到结果的调用的回执：循环在工具执行途中被中止时，剩下的调用没有结果——协议要求每个
// 函数调用都跟着它的结果，缺口由这条回执补上，模型看到的是「没执行」而不是一个非法请求。
const responsesCallAbandoned = "（未执行：本轮在工具执行途中被中止）"

// responsesInput 把内部消息转成协议的输入项。角色直接映射；assistant 的工具调用与 tool 的结果
// 按 call_id 配对；assistant 消息上带着的推理项原样放回它之后的调用项之前。
func responsesInput(msgs []Message, supportsVision bool) ([]json.RawMessage, error) {
	if len(msgs) == 0 {
		return nil, fmt.Errorf("messages 不能为空")
	}
	for i, m := range msgs {
		if m.Role == "" {
			return nil, fmt.Errorf("message[%d]: role 不能为空", i)
		}
		for j, tc := range m.ToolCalls {
			if len(tc.Arguments) > 0 && !json.Valid(tc.Arguments) {
				return nil, fmt.Errorf("message[%d].tool_calls[%d]: arguments 不是合法 JSON", i, j)
			}
		}
	}
	answered := make(map[string]bool)
	for _, m := range msgs {
		if m.Role == RoleTool {
			answered[m.ToolCallID] = true
		}
	}

	items := make([]json.RawMessage, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case RoleSystem, RoleUser:
			content, err := responsesMessageBody(m, supportsVision, responsesPartInputText)
			if err != nil {
				return nil, err
			}
			items = append(items, mustJSON(responsesMessageItem{Type: responsesItemMessage, Role: m.Role, Content: content}))
		case RoleAssistant:
			// 推理项必须跟着它之后的产出（正文或调用）：都没有时不放回去。
			if m.Thinking != nil && len(m.Thinking.ReasoningItem) > 0 && (m.Content != "" || len(m.ContentParts) > 0 || len(m.ToolCalls) > 0) {
				items = append(items, m.Thinking.ReasoningItem)
			}
			if m.Content != "" || len(m.ContentParts) > 0 {
				content, err := responsesMessageBody(m, supportsVision, responsesPartOutputText)
				if err != nil {
					return nil, err
				}
				items = append(items, mustJSON(responsesMessageItem{Type: responsesItemMessage, Role: RoleAssistant, Content: content}))
			}
			for _, tc := range m.ToolCalls {
				args := tc.Arguments
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				items = append(items, mustJSON(responsesCall{Type: responsesItemCall, CallID: tc.ID, Name: tc.Name, Arguments: string(args)}))
			}
			// 没等到结果的调用紧跟回执
			for _, tc := range m.ToolCalls {
				if !answered[tc.ID] {
					items = append(items, mustJSON(responsesCallOutput{Type: responsesItemCallOutput, CallID: tc.ID, Output: mustJSON(responsesCallAbandoned)}))
				}
			}
		case RoleTool:
			if m.ToolCallID == "" {
				return nil, fmt.Errorf("tool 消息缺少 tool_call_id")
			}
			output, err := responsesToolOutput(m, supportsVision)
			if err != nil {
				return nil, err
			}
			items = append(items, mustJSON(responsesCallOutput{Type: responsesItemCallOutput, CallID: m.ToolCallID, Output: output}))
		default:
			return nil, fmt.Errorf("unsupported role: %q", m.Role)
		}
	}
	return items, nil
}

// responsesMessageBody 是一条消息正文的协议形态：纯文本是字符串；带图且模型支持视觉时是内容块
// 数组（textPart 取 input_text 或 output_text，按消息是谁写的）；不支持视觉时图片换成占位文本。
func responsesMessageBody(m Message, supportsVision bool, textPart string) (json.RawMessage, error) {
	if len(m.ContentParts) == 0 {
		return mustJSON(m.Content), nil
	}
	if !supportsVision {
		return mustJSON(stripImagesToText(m.ContentParts)), nil
	}
	parts := make([]responsesContentPart, 0, len(m.ContentParts))
	for _, p := range m.ContentParts {
		switch p.Type {
		case "text":
			if p.Text != "" {
				parts = append(parts, responsesContentPart{Type: textPart, Text: p.Text})
			}
		case "image_url": // 内部内容块的 wire 值（见 ContentPart）
			if p.ImageURL == nil || p.ImageURL.Base64Data == "" {
				return nil, fmt.Errorf("image_url part: ImageURL/Base64Data 必填")
			}
			parts = append(parts, responsesContentPart{Type: responsesPartInputImage, ImageURL: "data:" + p.ImageURL.MediaType + ";base64," + p.ImageURL.Base64Data})
		default:
			return nil, fmt.Errorf("unsupported content part type: %q", p.Type)
		}
	}
	return mustJSON(parts), nil
}

// responsesToolOutput 是工具结果的协议形态：纯文本是字符串；带截图且模型支持视觉时是内容块
// 数组（协议允许结果带图，不必像 Chat Completions 那样挪到用户消息里）。
func responsesToolOutput(m Message, supportsVision bool) (json.RawMessage, error) {
	return responsesMessageBody(m, supportsVision, responsesPartInputText)
}

func responsesTools(tools []ToolSchema) ([]responsesTool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	out := make([]responsesTool, len(tools))
	for i, t := range tools {
		params := t.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		} else if !json.Valid(params) {
			return nil, fmt.Errorf("tool %s parameters: 不是合法 JSON", t.Name)
		}
		out[i] = responsesTool{Type: "function", Name: t.Name, Description: t.Description, Parameters: params, Strict: false}
	}
	return out, nil
}

// input 转换输入：先按各协议共同的不变式校验（首条非 system 是 user、没有连续的 assistant、
// 工具结果与调用配对），不满足时保留最近若干轮再试——历史压缩把窗口起点对齐到 assistant 时
// 严格实现会拒绝。
func (g *openAIResponsesGen) input(msgs []Message) ([]json.RawMessage, error) {
	if err := validateMessagesForAPI(msgs); err != nil {
		msgs = truncateMessagesToFit(msgs, 20)
		if err := validateMessagesForAPI(msgs); err != nil {
			return nil, fmt.Errorf("OpenAI Responses: messages 校验失败且无法修复: %w", err)
		}
	}
	return responsesInput(msgs, g.supportsVision)
}

// request 组装请求体（不含输出上限，由 post 按当次的上限填）。
func (g *openAIResponsesGen) request(msgs []Message, tools []ToolSchema, temperature *float64) (*responsesRequest, error) {
	input, err := g.input(msgs)
	if err != nil {
		return nil, err
	}
	outTools, err := responsesTools(tools)
	if err != nil {
		return nil, err
	}
	req := &responsesRequest{
		Model:       g.model,
		Input:       input,
		Temperature: temperature,
		Store:       false,
		Include:     []string{responsesIncludeEncryptedReasoning},
		Tools:       outTools,
	}
	if g.reasoningEffort != "" {
		req.Reasoning = &responsesReasoning{Effort: g.reasoningEffort}
	}
	return req, nil
}

// ── 应答 ─────────────────────────────────────────────────────────────────────

// responsesResponse 是一次响应的对象。output 的每一项保留原文：推理项要原样回传，不能
// 经结构体转一圈丢了没声明的字段。
type responsesResponse struct {
	ID                string            `json:"id"`
	Status            string            `json:"status"` // completed | incomplete | failed | …
	Output            []json.RawMessage `json:"output"`
	Error             *responsesError   `json:"error"`
	IncompleteDetails *struct {
		Reason string `json:"reason"` // max_output_tokens | content_filter | …
	} `json:"incomplete_details"`
	Usage *responsesUsage `json:"usage"`
}

type responsesItem struct {
	Type             string             `json:"type"`
	CallID           string             `json:"call_id"`
	Name             string             `json:"name"`
	Arguments        string             `json:"arguments"`
	Content          []responsesContent `json:"content"`
	EncryptedContent string             `json:"encrypted_content"`
}

type responsesContent struct {
	Type    string `json:"type"` // output_text | refusal
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

type responsesError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type responsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"` // 含推理 token
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

// finishReason 是协议给出的结束原因，原样透出：completed；incomplete 时给出未完成的原因
// （输出达到上限时是 max_output_tokens，调用方据此识别截断）；其余取状态本身。
func (r *responsesResponse) finishReason() string {
	if r.Status == "incomplete" && r.IncompleteDetails != nil && r.IncompleteDetails.Reason != "" {
		return r.IncompleteDetails.Reason
	}
	return r.Status
}

// result 把应答折成 Result：消息项的正文与拒绝说明拼成 Content，函数调用项成为 ToolCall
// （ID 取 call_id，回传结果时按它配对），第一个带加密内容的推理项原文留在 Thinking 上供下一轮回传。
func (r *responsesResponse) result(provider, model string) (Result, error) {
	res := Result{FinishReason: r.finishReason(), Provider: provider, Model: model}
	if r.Usage != nil {
		res.Usage = Usage{InTokens: r.Usage.InputTokens, OutTokens: r.Usage.OutputTokens, CachedTokens: r.Usage.InputTokensDetails.CachedTokens}
	}
	for _, raw := range r.Output {
		var it responsesItem
		if err := json.Unmarshal(raw, &it); err != nil {
			return Result{}, fmt.Errorf("OpenAI Responses: 应答的 output 项不是合法 JSON: %w", err)
		}
		switch it.Type {
		case responsesItemMessage:
			for _, c := range it.Content {
				res.Content += c.Text + c.Refusal
			}
		case responsesItemCall:
			args := json.RawMessage(it.Arguments)
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			res.ToolCalls = append(res.ToolCalls, ToolCall{ID: it.CallID, Name: it.Name, Arguments: args})
		case responsesItemReasoning:
			if it.EncryptedContent != "" && (res.Thinking == nil || len(res.Thinking.ReasoningItem) == 0) {
				res.Thinking = &ThinkingBlock{ReasoningItem: raw}
			}
		}
	}
	return res, nil
}

// parseResponse 解析应答对象：2xx 但不是响应对象（网关把错误页包成 200）判为不符合协议；
// 状态是 failed 的带上上游给的说明。
func parseResponse(body []byte) (*responsesResponse, error) {
	var r responsesResponse
	if err := json.Unmarshal(body, &r); err != nil || (r.ID == "" && r.Status == "" && r.Output == nil && r.Error == nil) {
		return nil, fmt.Errorf("OpenAI Responses: 2xx 响应体不是响应对象: %w", ErrUnexpectedResponse)
	}
	if r.Status == "failed" {
		return nil, r.failure()
	}
	return &r, nil
}

func (r *responsesResponse) failure() error {
	if r.Error != nil && r.Error.Message != "" {
		return fmt.Errorf("OpenAI Responses: 上游生成失败（%s）: %s", r.Error.Code, r.Error.Message)
	}
	return fmt.Errorf("OpenAI Responses: 上游生成失败（状态 %s）", r.Status)
}

// ── 请求的发送 ───────────────────────────────────────────────────────────────

// send 发出一次创建请求：2xx 把响应交给调用方（响应体由调用方读完并关闭），其余读完错误体、
// 关闭后归一为 HTTPError（限流带上恢复时刻）。
func (g *openAIResponsesGen) send(ctx context.Context, body []byte, stream bool) (*http.Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 == 2 {
		return resp, nil
	}
	up := &UpstreamError{Status: resp.StatusCode, Body: readBody(resp)}
	_ = resp.Body.Close()
	he := &HTTPError{Code: resp.StatusCode, Inner: up}
	if resp.StatusCode == http.StatusTooManyRequests {
		he.RetryAt = retryAtOf(resp.Header, string(up.Body), time.Now())
	}
	return nil, he
}

// post 发出创建请求。输出上限被上游嫌大（400 且点名上限字段）时降一半重发。
func (g *openAIResponsesGen) post(ctx context.Context, req *responsesRequest, requested int) (*http.Response, error) {
	limit := g.limit.forCall(requested)
	for {
		req.MaxOutputTokens = limit
		body, err := json.Marshal(req)
		if err != nil {
			return nil, fmt.Errorf("OpenAI Responses: 编码请求失败: %w", err)
		}
		resp, err := g.send(ctx, body, req.Stream)
		if err == nil {
			return resp, nil
		}
		if next, ok := g.limit.rejected(err, limit); ok {
			limit = next
			continue
		}
		return nil, err
	}
}

// readBody 读完响应体（上限 1 MiB：错误体不会更大，防异常上游拖垮内存），不负责关闭。
func readBody(resp *http.Response) []byte {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return b
}

// Generate 发起一次响应请求；tools 每次动态传入。
func (g *openAIResponsesGen) Generate(ctx context.Context, msgs []Message, tools []ToolSchema) (Result, error) {
	return g.GenerateWithOptions(ctx, msgs, tools, GenerateOptions{})
}

// GenerateWithOptions 实现 OptionsGenerator：按调用透传采样温度与输出预算。
func (g *openAIResponsesGen) GenerateWithOptions(ctx context.Context, msgs []Message, tools []ToolSchema, opts GenerateOptions) (Result, error) {
	req, err := g.request(msgs, tools, opts.Temperature)
	if err != nil {
		return Result{}, fmt.Errorf("OpenAI Responses: 组装请求失败: %w", err)
	}
	resp, err := g.post(ctx, req, opts.MaxTokens)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	r, err := parseResponse(readBody(resp))
	if err != nil {
		return Result{}, err
	}
	return r.result(g.provider, g.model)
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}
