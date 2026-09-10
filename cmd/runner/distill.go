package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/insight"
	"github.com/V3teran/liusha/internal/provider"
)

const distillMaxEntries = 5

type distilledEntry struct {
	Title   string   `json:"title"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

const distillInstruction = `你是渗透知识蒸馏器。看完本次渗透交战的会话轨迹、坐实的漏洞、过程情报后，提炼出【跨目标可复用】的知识沉淀。

只提炼满足全部三条的：① 验证过的（实战确认有效，非猜测）；② 可复用跨目标的（对『这类目标/技术』通用），不是本次目标专属细节；③ 非显然的（通用知识里没有的）。

不满足就宁缺毋滥——本次没有可复用打法时返回空数组。绝不提炼：本次目标专属情报、一次性事实、通用 OWASP 理论。

严格输出 JSON 数组（不要任何前后缀、不要 markdown 代码块），每项 {"title":"一句话主题","content":"具体怎么做、关键手法/payload","tags":["技术标签如 sso:cas、jwt"]}。无可沉淀时输出 []。`

func (h handler) distillCorpus(ctx context.Context, taskID, convID, tier, host string) {
	if h.corpus == nil {
		return
	}
	material := h.gatherDistillMaterial(ctx, taskID, convID, tier, host)
	if material == "" {
		h.logger.Info().Str("task_id", taskID).Msg("收尾蒸馏跳过：无素材")
		return
	}
	h.logger.Info().Str("task_id", taskID).Int("material_bytes", len(material)).Msg("收尾蒸馏开始")

	entries := h.runDistill(ctx, material)
	saved := 0
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
			continue
		}
		saved++
	}
	h.logger.Info().Str("task_id", taskID).Int("distilled", len(entries)).Int("saved", saved).Msg("收尾蒸馏完成")
}

func (h handler) gatherDistillMaterial(ctx context.Context, taskID, convID, tier, host string) string {
	var b strings.Builder

	if hist := h.conversationContext(ctx, convID, tier, ""); hist != "" {
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
			if section := insight.FormatSection(grouped); section != "" {
				b.WriteString("\n")
				b.WriteString(section)
			}
		}
	}

	return strings.TrimSpace(b.String())
}

func (h handler) runDistill(ctx context.Context, material string) []distilledEntry {
	p, err := h.router.For(ctx, provider.ComplexitySimple)
	if err != nil {
		h.logger.Warn().Err(err).Msg("蒸馏：解析 inspector provider 失败（跳过）")
		return nil
	}

	compaction, _ := h.settings.Compaction(ctx)
	timeout := compaction.CompactorTimeoutSeconds
	if timeout <= 0 {
		timeout = 30
	}
	dctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	resp, err := p.Complete(dctx, provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: distillInstruction},
			{Role: provider.RoleUser, Content: material},
		},
		MaxTokens: 2048,
	})
	if err != nil {
		h.logger.Warn().Err(err).Msg("蒸馏：provider 调用失败（跳过）")
		return nil
	}

	entries, err := parseDistilled(resp.Content)
	if err != nil {
		h.logger.Warn().Err(err).Msg("蒸馏：产出 JSON 解析失败（跳过）")
		return nil
	}
	if len(entries) > distillMaxEntries {
		entries = entries[:distillMaxEntries]
	}
	return entries
}

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
