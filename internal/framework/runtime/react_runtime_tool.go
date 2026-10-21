package runtime

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/registry"
)

func (r *DefaultReActRuntime) parseLLMResponse(response llm.Response) (*llmResponse, error) {
	result := &llmResponse{
		Content:   response.Content,
		ToolCalls: response.ToolCalls,
		Thinking:  response.Thinking,
		Usage:     response.Usage,
	}

	return result, nil
}

// thinkingText nil-safe 取思考原文。
func thinkingText(t *llm.ThinkingBlock) string {
	if t == nil {
		return ""
	}
	return t.Text
}

// executeTool 执行工具
func (r *DefaultReActRuntime) executeTool(ctx context.Context, toolCall llm.ToolCall) (string, error) {
	r.mu.RLock()
	tool, exists := r.tools[toolCall.Name]
	r.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("工具 %s 未注册", toolCall.Name)
	}

	// 获取工具超时时间
	timeout := tool.Timeout()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// 异步执行工具 + 超时保护
	resultCh := make(chan registry.ToolResult, 1)
	errCh := make(chan error, 1)

	go func() {
		result, err := tool.Execute(ctx, toolCall.Arguments)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	// 等待结果或超时
	var result registry.ToolResult
	var err error

	select {
	case result = <-resultCh:
		// 成功获取结果
	case err = <-errCh:
		return "", err
	case <-ctx.Done():
		return "", fmt.Errorf("工具 %s 执行超时（%s）", toolCall.Name, timeout)
	}

	// 如果工具返回错误
	if result.Error != "" {
		return result.Error, nil
	}

	// 返回工具输出（Output 字段已经是字符串）
	return result.Output, nil
}

// saveCheckpoint 保存当前执行状态到检查点
