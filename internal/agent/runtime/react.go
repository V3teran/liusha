package runtime

import (
	"context"
	"fmt"

	einoschema "github.com/cloudwego/eino/schema"

	"github.com/V3teran/liusha/internal/agent/llm"
	"github.com/V3teran/liusha/internal/worker"
)

// RunParams 是 ReAct 循环的入参。
type RunParams struct {
	Payload      worker.AgentRunPayload
	SystemPrompt string
	MaxSteps     int
	Gen          llm.Generator // 直接引用 llm.Generator，无需重复定义
	Sink         Sink
}

// Run 执行 ReAct 循环（Think → Act → Observe），Plan 5 骨架版：
//   - 无 ToolCalls (final answer) → Sink.Observe(final_answer) → nil
//   - 有 ToolCalls → Sink.Observe(tool_call_deferred) + 追加占位 Tool 消息 → 继续
//   - 超 MaxSteps 仍无 final → 返回 error
func Run(ctx context.Context, p RunParams) error {
	if p.MaxSteps <= 0 {
		return fmt.Errorf("MaxSteps 必须 > 0")
	}
	msgs := []*einoschema.Message{
		{Role: einoschema.System, Content: p.SystemPrompt},
		{Role: einoschema.User, Content: buildUserPrompt(p.Payload)},
	}

	for step := 0; step < p.MaxSteps; step++ {
		resp, err := p.Gen.Generate(ctx, msgs)
		if err != nil {
			return fmt.Errorf("step %d LLM Generate 失败: %w", step, err)
		}

		if len(resp.ToolCalls) == 0 {
			return p.Sink.Observe(ctx, Observation{
				EngagementID: p.Payload.EngagementID,
				TaskID:       p.Payload.TaskID,
				Role:         p.Payload.Role,
				Kind:         "final_answer",
				Content:      resp.Content,
				Step:         step,
			})
		}

		msgs = append(msgs, resp)
		for _, tc := range resp.ToolCalls {
			if err := p.Sink.Observe(ctx, Observation{
				EngagementID: p.Payload.EngagementID,
				TaskID:       p.Payload.TaskID,
				Role:         p.Payload.Role,
				Kind:         "tool_call_deferred",
				ToolName:     tc.Function.Name,
				Content:      tc.Function.Arguments,
				Step:         step,
			}); err != nil {
				return fmt.Errorf("step %d Sink.Observe 失败: %w", step, err)
			}
			msgs = append(msgs, &einoschema.Message{
				Role:       einoschema.Tool,
				Content:    fmt.Sprintf("工具 %s 尚未在 Plan 5 中接入（骨架阶段），请基于现有上下文直接推理给出结论。", tc.Function.Name),
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
			})
		}
	}

	return fmt.Errorf("超过最大步数 %d 未收敛", p.MaxSteps)
}

// buildUserPrompt 只注入 UUID / role / inputNodeIDs，不加载节点 payload。
// Plan 5 明确承认：LLM 看到的只是 UUID 字符串；推理结果仅用于链路贯通，不落图。
// Plan 6 会替换为加载真实 node.Payload 作为上下文。
func buildUserPrompt(p worker.AgentRunPayload) string {
	if len(p.InputNodeIDs) == 0 {
		return fmt.Sprintf(
			"Role: %s\nEngagement: %s\n[Plan 5 骨架：无工具、无图上下文]\n请示范一次 Think→Act→Observe 流程。",
			p.Role, p.EngagementID,
		)
	}
	ids := make([]string, len(p.InputNodeIDs))
	for i, id := range p.InputNodeIDs {
		ids[i] = id.String()
	}
	return fmt.Sprintf(
		"Role: %s\nEngagement: %s\n候选输入节点 ID（无 payload）: %v\n[Plan 5 骨架：无工具、无图上下文]",
		p.Role, p.EngagementID, ids,
	)
}
