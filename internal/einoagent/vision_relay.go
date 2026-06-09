package einoagent

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// vision_relay.go：TODO-1 截图视觉回灌（照搬 react 的 SupportsVision + pendingImages→user message 模式）。
//
// 问题：run_command（EnhancedInvokableTool）返回 ToolResult 含截图 image part，eino 默认把它放进
// tool message。但小米 mimo 等国产 provider 校验 tool-role image_url part 要求每 part 带 text →
// 400「Param Incorrect: `text` is not set」（确定性，重试救不了）。active e2e 实测：截图缺失
// 把 orchestrator 的 browser-use 登录打瘫（文本盲打）。
//
// 解法（react openai_compat.go:151-159 已验证不 400）：图**不留 tool message，转投紧随的 user message**
// （OpenAI 标准位）。本 middleware：
//   - WrapToolCall：拦截 ToolResult，**抽走 image part 存 pending（剥后 tool message 只剩 text，永不 400）**
//   - BeforeChatModel：把 pending 图 flush 成 user message（UserInputMultiContent）插 state.Messages
//   - supportsVision=false：抽走 image part 直接丢弃（仅留文本占位，run_command Files 已标 image=true）
//
// 每个 agent run 一个独立实例（闭包持 pending + mutex）；并发 exploitation 各自独立，无竞争。
// 同一 agent 内多 tool call 并行 → pending 加锁。

// NewVisionRelayMiddleware 造截图回灌 middleware。supportsVision 决定回灌（true）或丢弃降级（false）。
func NewVisionRelayMiddleware(supportsVision bool) adk.AgentMiddleware {
	var mu sync.Mutex
	var pending []schema.MessageInputPart

	return adk.AgentMiddleware{
		WrapToolCall: compose.ToolMiddleware{
			// 只有 EnhancedInvokable（run_command）可能返回 image part；Invokable（InferTool 系）无图。
			EnhancedInvokable: func(next compose.EnhancedInvokableToolEndpoint) compose.EnhancedInvokableToolEndpoint {
				return func(ctx context.Context, in *compose.ToolInput) (*compose.EnhancedInvokableToolOutput, error) {
					out, err := next(ctx, in)
					if out == nil || out.Result == nil {
						return out, err
					}
					textParts, imgParts := splitToolParts(out.Result.Parts)
					if len(imgParts) == 0 {
						return out, err
					}
					// 剥图：tool message 只保留 text part（永不触发 mimo 400）
					out.Result = &schema.ToolResult{Parts: textParts}
					if supportsVision {
						mu.Lock()
						pending = append(pending, imgParts...)
						mu.Unlock()
					}
					// 非 vision：丢弃图（仅文本占位，截图元信息在 text 的 Files 里标 image=true）
					return out, err
				}
			},
		},
		BeforeChatModel: func(_ context.Context, state *adk.ChatModelAgentState) error {
			mu.Lock()
			defer mu.Unlock()
			if len(pending) == 0 {
				return nil
			}
			// 累积的截图 flush 成一条 user message（紧随上轮 tool message 之后），vision provider 真识图。
			state.Messages = append(state.Messages, &schema.Message{
				Role:                  schema.User,
				UserInputMultiContent: pending,
			})
			pending = nil
			return nil
		},
	}
}

// splitToolParts 把 ToolResult parts 拆成 text part（留 tool message）+ image 转 MessageInputPart（转 user message）。
func splitToolParts(parts []schema.ToolOutputPart) (text []schema.ToolOutputPart, images []schema.MessageInputPart) {
	for _, p := range parts {
		if p.Type == schema.ToolPartTypeImage && p.Image != nil {
			images = append(images, schema.MessageInputPart{
				Type:  schema.ChatMessagePartTypeImageURL,
				Image: &schema.MessageInputImage{MessagePartCommon: p.Image.MessagePartCommon},
			})
			continue
		}
		text = append(text, p)
	}
	return text, images
}
