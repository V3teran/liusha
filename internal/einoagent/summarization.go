package einoagent

import (
	"context"
	"fmt"
	"strings"

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

	cfg := &summarization.Config{Model: compactor}
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
