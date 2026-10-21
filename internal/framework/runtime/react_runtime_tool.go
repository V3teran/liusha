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

// executeTool 执行工具。failed 区分「结果性失败」（ErrorMask 语义：错误经
// ToolResult.Error 传递、err 为 nil——连续失败止损的计数依据，此前只看 err
// 导致生产链路永不计数、止损恒死）与成功。
func (r *DefaultReActRuntime) executeTool(ctx context.Context, toolCall llm.ToolCall) (observation string, failed bool, err error) {
	r.mu.RLock()
	tool, exists := r.tools[toolCall.Name]
	r.mu.RUnlock()

	if !exists {
		return "", true, fmt.Errorf("工具 %s 未注册", toolCall.Name)
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

	select {
	case result = <-resultCh:
		// 成功获取结果
	case err = <-errCh:
		return "", true, err
	case <-ctx.Done():
		return "", true, fmt.Errorf("工具 %s 执行超时（%s）", toolCall.Name, timeout)
	}

	// 结果性失败：错误文本即观察内容（链尾 ErrorMask 把错误装进结果字段）
	if result.Error != "" {
		return result.Error, true, nil
	}

	// 返回工具输出（Output 字段已经是字符串）
	return result.Output, false, nil
}

// saveCheckpoint 保存当前执行状态到检查点
