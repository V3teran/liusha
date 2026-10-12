package llm

import (
	"fmt"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

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
