package attackgraph

import (
	"context"
	"fmt"
	"strings"

	"golang.org/x/sync/errgroup"
)

// enrichConcurrency 是并发调 LLM 提炼判断的上限（对齐 milestoneConcurrency，防打爆 LLM 网关限速）。
const enrichConcurrency = milestoneConcurrency

// enrich 是投影第二趟：LLM 事后语义提炼（①），补齐 agent 未自标（②缺失）的任务线的「判断」。
//
// 骨架趟只出确定性节点 + agent 自标（mark_insight）；未被自标的 reasoning 只攒在 reasonByTask。
// 本趟对每条「有推理、无判断」的任务线，把该线的 reasoning 交 LLM 总结成一个 hypothesis 节点
// （Provenance=llm），插在任务与其探测之间——让「任务→判断→探测」故事线在历史数据/未自标场景也完整。
//
// ②优先：taskHasHypo[task]=true 的线跳过（agent 已自标，不重复兜底、不与自标冲突）。
// 各任务线相互独立，errgroup 并发提炼后再串行改图（改图非并发安全，故提炼与写图分离）。
// Summarizer 为 nil 直接返回（跳过①，等价只出骨架）。
func (b *builder) enrich(ctx context.Context, s Summarizer) {
	if s == nil {
		return
	}

	// 收集待提炼任务线（有推理、无判断），按节点序稳定。先收集、后改图：避免边遍历边追加。
	type pending struct {
		taskID string
		joined string
	}
	var todo []pending
	for _, n := range b.nodes {
		if n.Kind != KindTask || b.taskHasHypo[n.ID] {
			continue // 非任务，或 ②已覆盖
		}
		texts := b.reasonByTask[n.ID]
		if len(texts) == 0 {
			continue // 无推理可提炼
		}
		todo = append(todo, pending{
			taskID: n.ID,
			joined: truncateRunes(strings.Join(texts, "\n"), reasoningTextMax),
		})
	}
	if len(todo) == 0 {
		return
	}

	// 并发提炼（只读 s，不碰 b）：各线互不依赖，串行等就是延迟累加。
	titles := make([]string, len(todo))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(enrichConcurrency)
	for i := range todo {
		i := i // 循环变量捕获（与本仓库其余代码一致的显式重绑定）
		g.Go(func() error {
			summary, err := s.Summarize(gctx, hypothesisPrompt(todo[i].joined))
			if err != nil {
				return nil // 该线提炼失败：留空，下面跳过（宁缺毋滥，不插空判断）
			}
			titles[i] = strings.TrimSpace(summary)
			return nil
		})
	}
	_ = g.Wait() // 各 goroutine 恒返回 nil（失败已降级为空标题），Wait 仅等全部完成

	// 串行改图：插入 LLM 判断节点，并把该任务线的直属探测改挂到判断下（镜像 ② 的挂载点行为）。
	for i := range todo {
		title := firstLine(titles[i], hypothesisTitleMax)
		if title == "" {
			continue // 提炼失败/空 → 不插节点
		}
		hypoID := llmHypothesisID(todo[i].taskID)
		for j := range b.nodes {
			if b.nodes[j].ParentID == todo[i].taskID && b.nodes[j].Kind == KindProbe {
				b.nodes[j].ParentID = hypoID // 探测改挂判断下：故事线读作 任务→判断→探测
			}
		}
		b.add(Node{
			ID:         hypoID,
			Kind:       KindHypothesis,
			ParentID:   todo[i].taskID,
			Title:      title,
			Status:     StatusOpen,
			Provenance: ProvLLM,
		})
	}
}

// llmHypothesisID 给 LLM 提炼的判断节点造稳定合成 id（无单一来源 message，故不复用 message id）。
// 每任务线至多一个，同 taskID 幂等——重算投影得同一 id，前端节点不抖动。
func llmHypothesisID(taskID string) string {
	return "hypo-llm-" + taskID
}

// hypothesisPrompt 构造判断提炼请求：让 LLM 把一条任务线的推理总结成「它在追哪个猜想」。
func hypothesisPrompt(reasoning string) string {
	return fmt.Sprintf(
		"你在复盘一次自动化渗透测试的某条调查线。以下是该线的推理记录。"+
			"请用一句简洁的中文（不超过30字）概括这条线在追查/验证的核心猜想或判断，"+
			"只输出这句话、不要前缀：\n\n%s",
		reasoning,
	)
}
