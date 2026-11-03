// compaction.go 实现 runtime.Distiller：把 ReAct 压缩器裁掉的历史前缀经同角色
// provider 蒸馏成结构化纪要。失败语义由压缩器兜底（降级纯截断），本实现只管
// 尽力产出高质量纪要；经 Router 取 provider——审计埋点在 Router 装配层，蒸馏
// 调用与其它 LLM 调用同一审计面。
package cognition

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// compactionDistiller 是纪要蒸馏器（四 agent 各一份，装配时构造；auditCtx
// 携带任务/run 归属，蒸馏调用与其它 LLM 调用同一审计面）。engine 侧压缩器
// 经 runtime.Distiller 接口消费。
//
// 固定 medium 档：纪要本就是有损摘要（≤400 字小节体），蒸馏质量对推理档位
// 不敏感，按角色复杂度取档只会让 planner 的每次压缩都烧最贵的模型。
type compactionDistiller struct {
	router   *llm.Router
	auditCtx llm.CallMeta
	logger   zerolog.Logger
}

// newCompactionDistiller 构造蒸馏器（审计归属：role=compaction + 任务/run）。
func newCompactionDistiller(router *llm.Router, taskID, agentRunID string, logger zerolog.Logger) *compactionDistiller {
	return &compactionDistiller{
		router:   router,
		auditCtx: llm.CallMeta{TaskID: taskID, AgentRunID: agentRunID, Role: "compaction"},
		logger:   logger.With().Str("component", "compaction_distiller").Logger(),
	}
}

// 蒸馏输入的体量钳制：被裁前缀可能远超蒸馏 prompt 本身装得下的体量，
// 逐条截尾 + 总量封顶（最旧的先丢，与压缩同向），保证蒸馏调用自身必有界。
const (
	distillMaxRunesPerMessage = 1000
	distillMaxTranscriptRunes = 20000
)

const distillSystemPrompt = `你是智能体执行链路的上下文压缩器。把被截断的执行历史蒸馏成一段紧凑纪要，供同一智能体在后续轮次接续工作。

按以下小节组织纪要（无内容的小节省略）：
- 进展：已完成、已确认的关键结果
- 关键观察：影响后续决策的事实，保留数据（主机/端口/路径/凭据名/版本号等）
- 已试死路径：失败或被否定的方向及原因（防止后续重试）
- 待办：未完成、被中断的事项

要求：只依据给出的历史，禁止编造；输出纯文本，不超过 400 字。`

// Distill 实现 runtime.Distiller。任何失败返回 error（压缩器降级纯截断）。
func (d *compactionDistiller) Distill(ctx context.Context, role string, evicted []llm.Message, prior string) (string, error) {
	ctx = llm.WithCallMeta(ctx, d.auditCtx)
	p, err := d.router.For(ctx, llm.ComplexityMedium)
	if err != nil {
		return "", fmt.Errorf("compaction distill: provider: %w", err)
	}
	user := renderDistillTranscript(evicted)
	if prior != "" {
		user += "\n\n【上一份纪要（折叠合并进新纪要，勿丢失其结论）】\n" + prior
	}
	resp, err := p.Complete(ctx, llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: distillSystemPrompt},
			{Role: llm.RoleUser, Content: "为「" + role + "」角色压缩以下执行历史：\n\n" + user},
		},
		MaxTokens: 1200,
	})
	if err != nil {
		return "", fmt.Errorf("compaction distill: generate: %w", err)
	}
	return strings.TrimSpace(resp.Content), nil
}

// renderDistillTranscript 把被裁前缀渲染成蒸馏输入文本：逐条 [角色] 正文
// （工具调用行内联），逐条截尾、总量封顶。体量超限时自尾部反向收集——决策
// 相关性随时间衰减，紧邻尾部窗口的最新被裁段必须保留，截掉的只能是最旧的；
// 输出前反回时间正序，截除标记位于文首。
func renderDistillTranscript(evicted []llm.Message) string {
	lines := make([]string, 0, len(evicted))
	total := 0
	for i := len(evicted) - 1; i >= 0; i-- {
		var sb strings.Builder
		fmt.Fprintf(&sb, "[%s] ", evicted[i].Role)
		sb.WriteString(truncateRunes(evicted[i].Content, distillMaxRunesPerMessage))
		for _, tc := range evicted[i].ToolCalls {
			fmt.Fprintf(&sb, "\n  → 调用工具 %s(%s)", tc.Name, truncateRunes(string(tc.Arguments), 300))
		}
		line := sb.String()
		if total+len(line) > distillMaxTranscriptRunes && len(lines) > 0 {
			lines = append(lines, "…（更早的内容因体量上限未纳入蒸馏）")
			break
		}
		lines = append(lines, line)
		total += len(line)
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return strings.Join(lines, "\n")
}

// truncateRunes 按 rune 截尾（截断历史/蒸馏输入等 LLM 文本的通用口径）。
func truncateRunes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…（截断）"
}
