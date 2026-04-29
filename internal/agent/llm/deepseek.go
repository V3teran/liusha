package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/deepseek"
	einoschema "github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

// DeepSeekConfig 是 DeepSeek provider 的最小配置集。
type DeepSeekConfig struct {
	BaseURL   string
	Model     string
	APIKey    string
	MaxTokens int
}

type deepseekGen struct {
	model    *deepseek.ChatModel
	provider string
	modelID  string
}

// NewDeepSeek 构造一个 DeepSeek Generator。tools 在此一次性绑定，后续 Generate 调用复用。
//
// 注意：返回的 Generator 不安全跨 goroutine 并发使用，每个 task handler 建独立实例。
func NewDeepSeek(ctx context.Context, c DeepSeekConfig, tools []ToolSchema) (Generator, error) {
	cm, err := deepseek.NewChatModel(ctx, &deepseek.ChatModelConfig{
		BaseURL:   c.BaseURL,
		Model:     c.Model,
		APIKey:    c.APIKey,
		MaxTokens: c.MaxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("new deepseek: %w", err)
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
	return &deepseekGen{model: cm, provider: "deepseek", modelID: c.Model}, nil
}

func (g *deepseekGen) Provider() string { return g.provider }
func (g *deepseekGen) Model() string    { return g.modelID }

// Generate 发起一次同步对话；tools 参数被忽略（已在 New 时绑定）。
func (g *deepseekGen) Generate(ctx context.Context, msgs []Message, _ []ToolSchema) (Result, error) {
	einoMsgs := toEinoMessages(msgs)
	out, err := g.model.Generate(ctx, einoMsgs)
	if err != nil {
		return Result{}, fmt.Errorf("deepseek generate: %w", err)
	}
	return fromEinoMessage(out, g.provider, g.modelID), nil
}

// toEinoMessages 把内部 Message 转为 eino schema.Message。
func toEinoMessages(in []Message) []*einoschema.Message {
	out := make([]*einoschema.Message, len(in))
	for i, m := range in {
		em := &einoschema.Message{
			Role:       einoschema.RoleType(m.Role),
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			em.ToolCalls = append(em.ToolCalls, einoschema.ToolCall{
				ID: tc.ID,
				Function: einoschema.FunctionCall{
					Name:      tc.Name,
					Arguments: string(tc.Arguments),
				},
			})
		}
		out[i] = em
	}
	return out
}

// toEinoTools 把 ToolSchema（含原始 JSON Schema）转为 eino ToolInfo。
func toEinoTools(tools []ToolSchema) ([]*einoschema.ToolInfo, error) {
	out := make([]*einoschema.ToolInfo, len(tools))
	for i, t := range tools {
		sc := &jsonschema.Schema{}
		if len(t.Parameters) > 0 {
			if err := json.Unmarshal(t.Parameters, sc); err != nil {
				return nil, fmt.Errorf("tool %s params: %w", t.Name, err)
			}
		}
		out[i] = &einoschema.ToolInfo{
			Name:        t.Name,
			Desc:        t.Description,
			ParamsOneOf: einoschema.NewParamsOneOfByJSONSchema(sc),
		}
	}
	return out, nil
}

// fromEinoMessage 把 eino schema.Message 转为内部 Result。
func fromEinoMessage(m *einoschema.Message, provider, model string) Result {
	res := Result{
		Content:  m.Content,
		Provider: provider,
		Model:    model,
	}
	if m.ResponseMeta != nil {
		res.FinishReason = m.ResponseMeta.FinishReason
		if m.ResponseMeta.Usage != nil {
			res.Usage = Usage{
				InTokens:     m.ResponseMeta.Usage.PromptTokens,
				OutTokens:    m.ResponseMeta.Usage.CompletionTokens,
				CachedTokens: m.ResponseMeta.Usage.PromptTokenDetails.CachedTokens,
			}
		}
	}
	for _, tc := range m.ToolCalls {
		res.ToolCalls = append(res.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}
	return res
}
