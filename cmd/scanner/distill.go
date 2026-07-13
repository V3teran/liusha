package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/lead"
)

// distill.go：收尾反思蒸馏（P3 写入三路之一，见 lesson→corpus 设计 §5.2）。
//
// task 正常 complete 时触发：取「压缩对话轨迹 + 本次 finding + 本 host 情报黑板」喂 light 模型，
// 提炼出 0~N 条【跨目标可复用】知识写入 corpus（source=agent）。best-effort——任何失败只 warn，
// 不阻塞收尾。与 agent 直写 write_corpus 并存（收尾兜底把散落经验提炼一遍，content_hash 去重）。

// distillMaxEntries 是单次蒸馏产出的 corpus 条数上限（防 light 模型话痨污染库）。
const distillMaxEntries = 5

// distilledEntry 是蒸馏 LLM 的结构化产出项。
type distilledEntry struct {
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

// distillInstruction 是蒸馏 system 指引：只提炼跨目标可复用打法，判据同 write_corpus。
const distillInstruction = `你是渗透知识蒸馏器。看完本次渗透交战的对话轨迹、坐实的漏洞、过程情报后，提炼出【跨目标可复用】的知识沉淀。

只提炼满足全部三条的：① 验证过的（实战确认有效，非猜测）；② 可复用跨目标的（对『这类目标/技术』通用，如某 SSO 的登录逆向套路，凡用此 SSO 的系统皆可复用），不是本次目标专属细节；③ 非显然的（通用知识里没有的）。

不满足就宁缺毋滥——本次没有可复用打法时返回空数组。绝不提炼：本次目标专属情报、一次性事实、通用 OWASP 理论。

严格输出 JSON 数组（不要任何前后缀、不要 markdown 代码块），每项 {"title":"一句话主题","content":"具体怎么做、关键手法/payload","tags":["技术标签如 sso:cas、jwt"]}。无可沉淀时输出 []。`

// distillCorpus 收尾蒸馏：best-effort，失败只 warn 不阻塞。emb/corpus 任一为 nil 则跳过。
func (h handler) distillCorpus(ctx context.Context, taskID, convID, role, host string) {
	if h.corpus == nil {
		return
	}
	material := h.gatherDistillMaterial(ctx, taskID, convID, role, host)
	if material == "" {
		return // 无素材（空对话+无 finding+无 lead），没什么可提炼
	}

	entries := h.runDistill(ctx, material)
	for _, e := range entries {
		if e.Content == "" || e.Title == "" {
			continue
		}
		var vec []float32
		if h.embedder != nil {
			if vs, err := h.embedder.EmbedPassage(ctx, []string{e.Content}); err == nil && len(vs) == 1 {
				vec = vs[0]
			}
		}
		if _, err := h.corpus.Add(ctx, corpus.Entry{
			Title:        e.Title,
			Content:      e.Content,
			Tags:         e.Tags,
			Source:       corpus.SourceAgent,
			SourceTaskID: taskID,
			Embedding:    vec,
		}); err != nil {
			h.logger.Warn().Err(err).Str("task_id", taskID).Msg("蒸馏写入 corpus 失败（不阻塞收尾）")
		}
	}
	if len(entries) > 0 {
		h.logger.Info().Str("task_id", taskID).Int("distilled", len(entries)).Msg("收尾蒸馏已沉淀跨目标知识")
	}
}

// gatherDistillMaterial 拼蒸馏素材：压缩对话轨迹（复用 conversationContext）+ 本次 finding + 本 host 情报黑板。
// 三份都空则返空串（caller 跳过蒸馏）。
func (h handler) gatherDistillMaterial(ctx context.Context, taskID, convID, role, host string) string {
	var b strings.Builder

	if hist := h.conversationContext(ctx, convID, role, ""); hist != "" {
		b.WriteString(hist)
		b.WriteString("\n")
	}

	if fs, err := h.findings.ListByTask(ctx, taskID); err == nil && len(fs) > 0 {
		b.WriteString("\n## 本次坐实的漏洞\n")
		for _, f := range fs {
			fmt.Fprintf(&b, "- [%s] %s\n", f.Severity, strings.TrimSpace(f.Summary))
		}
	}

	if host != "" && h.leads != nil {
		if grouped, err := h.leads.ReadRecent(ctx, host); err == nil {
			if section := lead.FormatSection(grouped); section != "" {
				b.WriteString("\n")
				b.WriteString(section)
			}
		}
	}

	return strings.TrimSpace(b.String())
}

// runDistill 调 light 模型蒸馏素材为结构化知识条目。失败/超时/解析失败返 nil（best-effort）。
func (h handler) runDistill(ctx context.Context, material string) []distilledEntry {
	model, err := h.einoFactory.For(ctx, "compactor") // light provider（与对话蒸馏同源）
	if err != nil {
		h.logger.Warn().Err(err).Msg("蒸馏：解析 compactor 模型失败（跳过）")
		return nil
	}

	timeout := h.cfg.React.HistoryCompact.CompactorTimeoutSeconds
	if timeout <= 0 {
		timeout = 30
	}
	dctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	out, err := model.Generate(dctx, []*schema.Message{
		schema.SystemMessage(distillInstruction),
		schema.UserMessage(material),
	})
	if err != nil || out == nil {
		h.logger.Warn().Err(err).Msg("蒸馏：light 模型调用失败（跳过）")
		return nil
	}

	entries, err := parseDistilled(out.Content)
	if err != nil {
		h.logger.Warn().Err(err).Msg("蒸馏：产出 JSON 解析失败（跳过）")
		return nil
	}
	if len(entries) > distillMaxEntries {
		entries = entries[:distillMaxEntries]
	}
	return entries
}

// parseDistilled 解析 LLM 产出的 JSON 数组，容忍 ```json 代码块包裹。
func parseDistilled(raw string) ([]distilledEntry, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return nil, nil
	}
	var entries []distilledEntry
	if err := json.Unmarshal([]byte(s), &entries); err != nil {
		return nil, fmt.Errorf("unmarshal distilled: %w", err)
	}
	return entries, nil
}
