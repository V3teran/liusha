package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/framework/llm"
)

func (r *DefaultReActRuntime) toolFailureLimit(config *ReActConfig) int {
	if config.MaxConsecutiveToolFailures > 0 {
		return config.MaxConsecutiveToolFailures
	}
	return 8
}

// callLLM 调用 LLM
func (r *DefaultReActRuntime) callLLM(
	ctx context.Context,
	config *ReActConfig,
	messageHistory []llm.Message,
) (*llmResponse, error) {
	// 应用消息预处理链（如果配置了）
	processedMessages := messageHistory
	if config.MessageModifierChain != nil {
		var err error
		processedMessages, err = config.MessageModifierChain.Apply(ctx, messageHistory)
		if err != nil {
			return nil, fmt.Errorf("message modifier chain failed: %w", err)
		}
	}

	// 消息已经是 llm.Message 格式，直接使用
	llmMessages := processedMessages

	// 构建 LLM 请求（温度按调用透传——Validate 已保证 0-1，>0 才有意义下发）
	request := llm.Request{
		Messages:  llmMessages,
		MaxTokens: config.MaxTokens,
	}
	if config.Temperature > 0 {
		t := config.Temperature
		request.Temperature = &t
	}

	// 添加工具定义（函数调用）
	if len(r.tools) > 0 {
		request.Tools = r.convertToolsToLLMFormat()
	}

	// 调用 LLM：流式开关生效——StreamEnabled 走 Stream 聚合（TTFT 可测、首 token
	// 即回调），否则非流式 Complete。两条路径产出同构的 llmResponse。
	var response llm.Response
	var err error
	if config.StreamEnabled {
		response, err = r.callLLMStreaming(ctx, config, request)
	} else {
		response, err = config.LLMProvider.Complete(ctx, request)
	}
	if err != nil {
		return nil, err
	}

	// 解析响应
	return r.parseLLMResponse(response)
}

// callLLMStreaming 流式调用并聚合为完整响应：text 增量拼接（首个增量即触发
// OnThought 前置回调的素材就绪）、StreamToolCall 收集、StreamDone 补 usage。
func (r *DefaultReActRuntime) callLLMStreaming(
	ctx context.Context,
	config *ReActConfig,
	request llm.Request,
) (llm.Response, error) {
	ch, err := config.LLMProvider.Stream(ctx, request)
	if err != nil {
		// provider 不支持流式 → 显式降级非流式（调用方语义仍是"拿到一次生成"）
		if strings.Contains(err.Error(), "不支持流式") {
			return config.LLMProvider.Complete(ctx, request)
		}
		return llm.Response{}, err
	}

	var resp llm.Response
	var sb strings.Builder
	for ev := range ch {
		switch ev.Kind {
		case llm.StreamText:
			sb.WriteString(ev.Content)
		case llm.StreamToolCall:
			if ev.Tool != nil {
				resp.ToolCalls = append(resp.ToolCalls, *ev.Tool)
			}
		case llm.StreamDone:
			if ev.Usage != nil {
				resp.Usage = *ev.Usage
			}
		case llm.StreamError:
			if ev.Err != nil {
				return llm.Response{}, fmt.Errorf("stream error: %w", ev.Err)
			}
			return llm.Response{}, fmt.Errorf("stream closed with error event")
		}
		if ctx.Err() != nil {
			return llm.Response{}, ctx.Err()
		}
	}
	resp.Content = sb.String()
	resp.FinishReason = "stop"
	return resp, nil
}

// llmResponse 是 LLM 响应的内部表示
type llmResponse struct {
	Content   string
	ToolCalls []llm.ToolCall
}

// convertToolsToLLMFormat 转换工具为 LLM 格式
func (r *DefaultReActRuntime) convertToolsToLLMFormat() []llm.ToolSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]llm.ToolSchema, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, llm.ToolSchema{
			Name:        tool.Name(),
			Description: tool.Desc(),
			Parameters:  tool.Schema(),
		})
	}

	return tools
}

// parseLLMResponse 解析 LLM 响应
