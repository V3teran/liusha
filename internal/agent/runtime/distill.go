package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/agent/llm"
	"github.com/V3teran/liusha/internal/finding"
)

// StateAppender 是 DistillHook 写 memory_hints 的最小依赖。
// *engagement.Store 隐式满足该接口，测试可注入 stub。
type StateAppender interface {
	AppendHint(ctx context.Context, id string, entry []byte) error
}

// distillHintPriority 是 Distill 写入 memory_hints 时的固定优先级。
// 数值约定见 spec §3.4：finding 蒸馏来的 hint 一般高于 user-supplied 普通提示。
const distillHintPriority = 7

// distillSystemPrompt 让 light_provider 把单条 finding 浓缩成 ≤ 200 字的中文提示。
const distillSystemPrompt = `你把一条新发现（finding）浓缩成 ≤ 200 字的中文提示，写给同一 engagement 后续步骤的 AI 看。
要求：
- 只输出提示正文本身，禁止任何前后缀、引号或 JSON 包装；
- 关注"下一步该尝试什么 / 该避开什么"，不复述发现细节；
- 中文，单段，不超过 200 字。`

// NewDistillHook 把 finding.Save 成功事件转成 memory_hints 提示。
//
// 设计要点（黑客松借鉴创新 8）：
//   - 同步执行；finding.Store.fireSavedHooks 已 go func() 起单独 goroutine，外层不会阻塞；
//   - LLM 失败 / 空内容 / AppendHint 失败 → 仅 slog.Warn，不 panic（finding 已经持久化）；
//   - hint 内带 ≤ 200 字干货，priority=7，from_skill 取 finding.Tool 便于后续追溯。
func NewDistillHook(g llm.Generator, store StateAppender) finding.SavedHook {
	return func(ctx context.Context, eid string, f finding.Finding) {
		distill(ctx, g, store, eid, f)
	}
}

// distill 是单次蒸馏的可测函数主体。
func distill(ctx context.Context, g llm.Generator, store StateAppender, eid string, f finding.Finding) {
	user := buildDistillPrompt(f)

	res, err := g.Generate(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: distillSystemPrompt},
		{Role: llm.RoleUser, Content: user},
	}, nil)
	if err != nil {
		slog.Warn("distill llm call failed", "err", err, "engagement_id", eid, "finding_id", f.ID)
		return
	}

	content := strings.TrimSpace(res.Content)
	if content == "" {
		slog.Warn("distill llm returned empty hint", "engagement_id", eid, "finding_id", f.ID)
		return
	}

	entry, err := json.Marshal(map[string]any{
		"from_skill": f.Tool,
		"content":    content,
		"priority":   distillHintPriority,
		"ts":         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		slog.Warn("distill marshal hint failed", "err", err, "engagement_id", eid, "finding_id", f.ID)
		return
	}

	if err := store.AppendHint(ctx, eid, entry); err != nil {
		slog.Warn("distill append hint failed", "err", err, "engagement_id", eid, "finding_id", f.ID)
		return
	}
}

// buildDistillPrompt 把 finding 的关键字段拼成单条 user 消息。
//
// 只保留对"下一步决策"有用的元信息：kind / dedup_key / title / 简短 evidence。
func buildDistillPrompt(f finding.Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "新发现：\nkind=%s\ntitle=%s\ndedup_key=%s\ntool=%s\n",
		f.Kind, f.Title, f.DedupKey, f.Tool)
	if len(f.Evidence) > 0 {
		fmt.Fprintf(&b, "evidence=%s\n", truncate(string(f.Evidence), 400))
	}
	b.WriteString("\n请按 system 约束输出 ≤ 200 字提示。")
	return b.String()
}
