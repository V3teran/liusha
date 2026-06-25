package attackgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/conversation"
)

// Milestone 是按子代理聚合的执行里程碑：用 LLM 把该子代理的全部推理总结成一句"它完成了什么"。
// 派生层产物（见 docs/attack-graph-design.md §8）：异步可重算，绝不替代原文真相。
type Milestone struct {
	Agent     string `json:"agent"`      // 子代理名（orchestrator/reconnaissance/exploitation）
	Summary   string `json:"summary"`    // LLM 一句话总结
	NodeCount int    `json:"node_count"` // 该子代理的推理节点数（信息量提示）
}

// Summarizer 把一段文本总结成一句话。窄接口便于测试注入 fake；生产由 cmd/api 用 llm.Router 适配。
type Summarizer interface {
	Summarize(ctx context.Context, prompt string) (string, error)
}

// reasoningTextMax 是喂给摘要 LLM 的单 agent 推理拼接上限（防超长 prompt；mimo 1M 上下文足够，留余量）。
const reasoningTextMax = 24000

// Milestones 按子代理聚合 message 事件流里的 reasoning，调 LLM 总结成里程碑列表（保子代理首次出现序）。
// 任一 agent 总结失败仅记 fallback（"（摘要生成失败）"），不阻断整体。
func Milestones(ctx context.Context, messages []conversation.Message, s Summarizer) ([]Milestone, error) {
	if s == nil {
		return nil, fmt.Errorf("summarizer 为空")
	}

	order := []string{}            // agent 首次出现序
	texts := map[string][]string{} // agent → 推理文字片段
	counts := map[string]int{}     // agent → 推理节点数

	for _, m := range messages {
		if m.Kind != conversation.KindEvent || len(m.Metadata) == 0 {
			continue
		}
		var ev traceEvent
		if err := json.Unmarshal(m.Metadata, &ev); err != nil || ev.Kind != evReasoning {
			continue
		}
		agent := ev.AgentName
		if agent == "" {
			agent = "(未归属)"
		}
		if _, seen := texts[agent]; !seen {
			order = append(order, agent)
		}
		texts[agent] = append(texts[agent], ev.Text)
		counts[agent]++
	}

	milestones := make([]Milestone, 0, len(order))
	for _, agent := range order {
		joined := strings.Join(texts[agent], "\n")
		if len(joined) > reasoningTextMax {
			joined = joined[:reasoningTextMax]
		}
		summary, err := s.Summarize(ctx, milestonePrompt(agent, joined))
		if err != nil {
			summary = "（摘要生成失败）"
		}
		milestones = append(milestones, Milestone{
			Agent:     agent,
			Summary:   strings.TrimSpace(summary),
			NodeCount: counts[agent],
		})
	}
	return milestones, nil
}

// milestonePrompt 构造摘要请求：让 LLM 用一句中文话概括某子代理在本次渗透中完成了什么。
func milestonePrompt(agent, reasoning string) string {
	return fmt.Sprintf(
		"你在复盘一次自动化渗透测试。以下是子代理「%s」在本次任务中的全部推理记录。"+
			"请用一句简洁的中文（不超过40字）概括它完成了什么关键工作，只输出这句话、不要前缀：\n\n%s",
		agent, reasoning,
	)
}
