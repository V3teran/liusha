package einoagent

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// compaction.go：eino 路径的 ReAct 历史压缩（gap③，替代 react.LLMHistoryCompactor）。
//
// 挂 AgentMiddleware.BeforeChatModel：每次模型调用前，若消息数超阈值，把"老 turn"用 light
// 模型蒸馏成 1 条摘要消息替换（防 context 爆）。passive 单流量很少触发；active 300 步必触发，
// 故做成 passive/active 可复用件。

//go:embed compaction_prompt.md
var compactionSystemPrompt string

const (
	defaultCompactTriggerCount = 40 // 消息数超此触发蒸馏
	defaultCompactKeepTail     = 12 // 保留最近 N 条不压（保近因 + tool 配对完整）
	summaryTag                 = "历史片段摘要"
)

// CompactionConfig 调蒸馏触发/保留窗口；零值用默认。
type CompactionConfig struct {
	TriggerCount int
	KeepTail     int
}

// NewCompactionMiddleware 造 BeforeChatModel 钩子：消息数超阈值时把老 turn 蒸馏成 1 条摘要。
//
// **安全裁切**：只在 turn 边界裁（assistant 决策 + 其 tool 结果为一个 turn），绝不切断
// tool_call↔tool result 配对（否则 provider 校验失败）。蒸馏失败不阻塞——保持原消息继续。
//
// sink 非 nil 时：压缩发生即发一条 ScanEventCompaction（Text=蒸馏摘要、Result=「压缩了 N 条」），
// 让前端对话流显示压缩卡，用户对长对话的上下文裁剪有感知（对齐 Claude Code 的 compaction 可见）。
func NewCompactionMiddleware(compactor model.BaseChatModel, cfg CompactionConfig, sink EventSink) adk.AgentMiddleware {
	trigger := cfg.TriggerCount
	if trigger <= 0 {
		trigger = defaultCompactTriggerCount
	}
	keepTail := cfg.KeepTail
	if keepTail <= 0 {
		keepTail = defaultCompactKeepTail
	}

	return adk.AgentMiddleware{
		BeforeChatModel: func(ctx context.Context, state *adk.ChatModelAgentState) error {
			msgs := state.Messages
			if len(msgs) <= trigger {
				return nil
			}
			sysEnd := leadingSystemCount(msgs)
			cut := safeCut(msgs, sysEnd, keepTail)
			if cut <= sysEnd+1 { // 不足两条可压，跳过
				return nil
			}
			summary, err := distill(ctx, compactor, msgs[sysEnd:cut])
			if err != nil || summary == "" {
				return nil // 失败不阻塞，保持原消息
			}
			summaryMsg := schema.UserMessage(fmt.Sprintf("<%s>\n%s", summaryTag, summary))
			rebuilt := make([]*schema.Message, 0, sysEnd+1+(len(msgs)-cut))
			rebuilt = append(rebuilt, msgs[:sysEnd]...)
			rebuilt = append(rebuilt, summaryMsg)
			rebuilt = append(rebuilt, msgs[cut:]...)
			state.Messages = rebuilt
			if sink != nil {
				// Result 载「压缩条数」可读串（不碰 token 字段，避免污染计费聚合——见 token 统计验证）。
				sink.OnScanEvent(ctx, ScanEvent{
					Kind:      ScanEventCompaction,
					Text:      summary,
					Result:    fmt.Sprintf("压缩了 %d 条历史消息", cut-sysEnd),
					AgentName: agentNameFromCtx(ctx),
				})
			}
			return nil
		},
	}
}

// leadingSystemCount 数开头连续的 system 消息（eino 把 instruction 放最前 system）。
func leadingSystemCount(msgs []*schema.Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role == schema.System {
			n++
		} else {
			break
		}
	}
	return n
}

// safeCut 返回裁切点 cut：msgs[sysEnd:cut] 是若干完整 turn，保留 >= keepTail 条尾。
// turn 起点 = role 非 tool 的消息（tool 结果归属前一 assistant，不能作裁切起点）。
// 取最大的合法 turn 边界 → 尽量多压，保留尾部完整 turn。
func safeCut(msgs []*schema.Message, sysEnd, keepTail int) int {
	cut := sysEnd
	for i := sysEnd; i < len(msgs); i++ {
		if msgs[i].Role == schema.Tool {
			continue // tool 结果不能作裁切起点（会拆配对）
		}
		if len(msgs)-i >= keepTail {
			cut = i
		} else {
			break
		}
	}
	return cut
}

// distill 把老消息序列化成文本喂 compactor，返回摘要文本。
func distill(ctx context.Context, compactor model.BaseChatModel, old []*schema.Message) (string, error) {
	var b strings.Builder
	for _, m := range old {
		for _, tc := range m.ToolCalls {
			fmt.Fprintf(&b, "%s[tool_call %s]: %s\n", m.Role, tc.Function.Name, tc.Function.Arguments)
		}
		if text := strings.TrimSpace(m.Content); text != "" {
			fmt.Fprintf(&b, "%s: %s\n", m.Role, text)
		}
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("distill: 老消息为空")
	}
	out, err := compactor.Generate(ctx, []*schema.Message{
		schema.SystemMessage(compactionSystemPrompt),
		schema.UserMessage(b.String()),
	})
	if err != nil {
		return "", fmt.Errorf("distill generate: %w", err)
	}
	if out == nil {
		return "", fmt.Errorf("distill: 空返回")
	}
	return strings.TrimSpace(out.Content), nil
}
