package einoagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// summarization.go：① run 内上下文压缩——全面采用 eino 自带 summarization middleware
// （github.com/cloudwego/eino/adk/middlewares/summarization，v0.8.0+）。
//
// 手写 compaction 已退役删除：触发阈值（token 预算）、trailing 保留、老消息蒸馏、Retry/Failover
// 全部由 eino 负责。本文件只做**装配**——把 liusha 侧的 ContextWindow / 触发比例 / EventSink
// 翻译成 eino 的 summarization.Config，不含任何压缩算法。
//
// 机制（vendor 源码复核 v0.8.13）：每次模型调用前 BeforeModelRewriteState 估算 token，超
// Trigger.ContextTokens 即把历史蒸馏成「system + 1 条摘要」；PreserveUserMessages（默认开）
// 把最近的 user 消息嵌回摘要，保留人类提问的「最近完整」。蒸馏走 light 模型，自带退避重试 + failover。
//
// 走 eino 原生 Handlers []adk.ChatModelAgentMiddleware 扩展点（非 struct 版 Middlewares）——
// orchestrator（deep.Config.Handlers）+ 每个子代理（ChatModelAgentConfig.Handlers）+ passive
// traffic-analysis 三类都挂，覆盖面与旧手写一致。

// SummarizationParams 是装配 eino summarization 所需的 liusha 侧参数。
type SummarizationParams struct {
	// ContextWindow 是 provider 总上下文窗口（tokens）。<=0 装配直接报错——
	// 不允许瞎猜窗口（32k 模型按 128k 算阈值会真爆 prompt）。
	ContextWindow int
	// TriggerRatio 是触发阈值占 ContextWindow 比例（如 0.75）。<=0 时回落 eino 默认（170k）。
	TriggerRatio float64
}

// NewSummarizationHandler 造 eino summarization 的 ChatModelAgentMiddleware（挂 Handlers 字段）。
//
// compactor 是 light 蒸馏模型（einollm.For("compactor") 解析出）。sink 非 nil 时，压缩发生经
// eino 的 Callback(before, after) 发一条 ScanEventCompaction——前端「压缩卡」可见上下文裁剪
// （对齐 Claude Code 的 compaction 可见）。
func NewSummarizationHandler(compactor model.BaseChatModel, p SummarizationParams, sink EventSink) (adk.ChatModelAgentMiddleware, error) {
	if compactor == nil {
		return nil, fmt.Errorf("NewSummarizationHandler: compactor 必填")
	}
	if p.ContextWindow <= 0 {
		return nil, fmt.Errorf("NewSummarizationHandler: ContextWindow 必须 > 0（provider 未配 context_window）")
	}

	cfg := &summarization.Config{
		Model: compactor,
		// 替换 eino 默认 char/4 估算——它对中文低估 3-4 倍（中文 BPE 约 1 token/字，char/4 当 4 算），
		// 致小窗口 provider（≤128k）上下文真撑爆窗口才触发压缩、provider 400。CJK 感知 counter 纠偏。
		TokenCounter: cjkTokenCounter,
	}
	if p.TriggerRatio > 0 {
		cfg.Trigger = &summarization.TriggerCondition{
			ContextTokens: int(float64(p.ContextWindow) * p.TriggerRatio),
		}
	}
	if sink != nil {
		cfg.Callback = func(ctx context.Context, before, after adk.ChatModelAgentState) error {
			cut := len(before.Messages) - len(after.Messages)
			if cut < 0 {
				cut = 0
			}
			sink.OnScanEvent(ctx, ScanEvent{
				Kind:      ScanEventCompaction,
				Text:      summaryTextFromState(after),
				Result:    fmt.Sprintf("压缩了 %d 条历史消息", cut),
				AgentName: agentNameFromCtx(ctx),
			})
			return nil
		}
	}

	return summarization.New(context.Background(), cfg)
}

// summaryTextFromState 取压缩后 state 末条（蒸馏摘要）的文本。
// eino 把摘要正文放进 UserInputMultiContent 的 text part 并清空 Content，故优先读 part、回退 Content。
func summaryTextFromState(state adk.ChatModelAgentState) string {
	if len(state.Messages) == 0 {
		return ""
	}
	m := state.Messages[len(state.Messages)-1]
	if m == nil {
		return ""
	}
	var b strings.Builder
	for _, part := range m.UserInputMultiContent {
		if part.Type == schema.ChatMessagePartTypeText && part.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(part.Text)
		}
	}
	if b.Len() > 0 {
		return b.String()
	}
	return m.Content
}

// cjkTokenCounter 是给 eino summarization 的自定义 TokenCounter（纯函数，无状态，并发安全）。
// 估算口径：CJK 字符 ≈ 1 token、其余 ≈ ÷4（英文/代码近似 4 字符/token）；含消息文本 + tool schema。
// 比 eino 默认 char/4 对中文准得多——确保压缩在真实撑爆窗口前触发（见 NewSummarizationHandler 注释）。
func cjkTokenCounter(_ context.Context, in *summarization.TokenCounterInput) (int, error) {
	if in == nil {
		return 0, nil
	}
	total := 0
	for _, m := range in.Messages {
		total += estimateCJKTokens(messageText(m))
	}
	// tool schema 也占 prompt token（25 个工具约数 k）；漏算会让触发偏晚。best-effort 序列化估。
	for _, t := range in.Tools {
		if t == nil {
			continue
		}
		if b, err := json.Marshal(t); err == nil {
			total += estimateCJKTokens(string(b))
		}
	}
	return total, nil
}

// messageText 抽一条消息参与 token 估算的全部文本：Content + 多模态 text part + tool_call 名/参数。
// （tool_call 参数如 sqlmap 长命令体积大，必须计入，否则严重低估。）
func messageText(m adk.Message) string {
	if m == nil {
		return ""
	}
	var b strings.Builder
	if m.Content != "" {
		b.WriteString(m.Content)
	}
	for _, p := range m.UserInputMultiContent {
		if p.Type == schema.ChatMessagePartTypeText && p.Text != "" {
			b.WriteString(p.Text)
		}
	}
	for _, tc := range m.ToolCalls {
		b.WriteString(tc.Function.Name)
		b.WriteString(tc.Function.Arguments)
	}
	return b.String()
}

// estimateCJKTokens 按「CJK 字符 ≈ 1 token、其余字符 ÷4」粗估 token 数。
func estimateCJKTokens(s string) int {
	cjk, other := 0, 0
	for _, r := range s {
		if isCJK(r) {
			cjk++
		} else {
			other++
		}
	}
	return cjk + (other+3)/4
}

// isCJK 判定一个 rune 是否 CJK 表意/假名/谚文/CJK 标点/全角（这些在 BPE 里约 1 token/字）。
func isCJK(r rune) bool {
	switch {
	case unicode.Is(unicode.Han, r),
		unicode.Is(unicode.Hiragana, r),
		unicode.Is(unicode.Katakana, r),
		unicode.Is(unicode.Hangul, r):
		return true
	case r >= 0x3000 && r <= 0x303F: // CJK 标点（、。〔〕…）
		return true
	case r >= 0xFF00 && r <= 0xFFEF: // 全角 ASCII / 标点（，：（））
		return true
	}
	return false
}
