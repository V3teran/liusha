package runtime

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/registry"
)

func (r *reactRuntime) parseLLMResponse(response llm.Response) (*llmResponse, error) {
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
//
// 成功输出经 config.ToolOutputTailBytes 全局钳制（尾部保留）后才作为观察进入
// 上下文——个别工具内的领域化裁剪先行，这里兜底保证任何单条工具结果有界。
func (r *reactRuntime) executeTool(ctx context.Context, config *ReActConfig, toolCall llm.ToolCall) (observation string, failed bool, err error) {
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
		// 工具参数是模型生成的 JSON：转义噪声在漏斗处归一，工具实现拿到的
		// 永远是合法字节（合法输入零改动直通）
		result, err := tool.Execute(ctx, llm.SanitizeJSON(toolCall.Arguments))
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

	// 返回工具输出（Output 字段已经是字符串），超限时尾部保留——输出末段是
	// 结果所在（与 run_tail_bytes 同一教义），头部横幅/噪音可弃。
	return clampTail(result.Output, config.ToolOutputTailBytes), false, nil
}

// clampTail 把输出收敛到 maxBytes 字节的尾部（maxBytes<=0 原样直通）。截口对齐
// UTF-8 边界：字节裁切可能落在多字节 rune 中间，丢弃不完整的残字节。
func clampTail(out string, maxBytes int) string {
	if maxBytes <= 0 || len(out) <= maxBytes {
		return out
	}
	tail := out[len(out)-maxBytes:]
	for len(tail) > 0 && tail[0]&0xC0 == 0x80 { // 续字节（10xxxxxx）= 残 rune，前移
		tail = tail[1:]
	}
	return "...[前段已截断]\n" + tail
}
