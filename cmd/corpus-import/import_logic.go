package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/embedding"
	"github.com/V3teran/liusha/internal/provider"
)

const tagInstruction = `你是渗透知识库标注器。给定一段知识正文，生成：
- title：一句话主题（≤30 字，概括这段讲什么）
- tags：技术/场景标签数组（如 sso:cas、jwt、fastjson、waf:cloudflare、cve:2023-xxx），2-5 个，用于检索时按标签过滤

严格输出 JSON（无前后缀、无 markdown 代码块）：{"title":"...","tags":["...","..."]}`

type tagResult struct {
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
}

func importFile(ctx context.Context, logger zerolog.Logger, store *corpus.Store, router *provider.Router, embedder *embedding.Client, path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("读文件: %w", err)
	}
	chunks := splitMarkdown(string(raw))
	if len(chunks) == 0 {
		logger.Warn().Str("file", path).Msg("无可导入内容（空文件或无正文段）")
		return 0, nil
	}

	n := 0
	for _, content := range chunks {
		tag := autoTag(ctx, logger, router, content)

		var vec []float32
		if embedder != nil {
			if vs, err := embedder.EmbedPassage(ctx, []string{content}); err == nil && len(vs) == 1 {
				vec = vs[0]
			}
		}

		if _, err := store.Add(ctx, corpus.Entry{
			Title:     tag.Title,
			Content:   content,
			Tags:      tag.Tags,
			Source:    corpus.SourceExpert,
			Embedding: vec,
		}); err != nil {
			logger.Warn().Err(err).Str("file", path).Msg("落库失败（跳过该条）")
			continue
		}
		n++
	}
	logger.Info().Str("file", path).Int("imported", n).Int("chunks", len(chunks)).Msg("文件导入完成")
	return n, nil
}

func splitMarkdown(md string) []string {
	lines := strings.Split(md, "\n")
	var chunks []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			chunks = append(chunks, s)
		}
		cur.Reset()
	}
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "#") {
			flush()
		}
		cur.WriteString(ln)
		cur.WriteString("\n")
	}
	flush()
	return chunks
}

func autoTag(ctx context.Context, logger zerolog.Logger, router *provider.Router, content string) tagResult {
	fallback := tagResult{Title: firstLine(content)}

	p, err := router.For(ctx, provider.ComplexitySimple)
	if err != nil {
		logger.Warn().Err(err).Msg("打标模型解析失败（降级：首行当 title）")
		return fallback
	}
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := p.Complete(tctx, provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: tagInstruction},
			{Role: provider.RoleUser, Content: content},
		},
		MaxTokens: 256,
	})
	if err != nil {
		logger.Warn().Err(err).Msg("打标调用失败（降级：首行当 title）")
		return fallback
	}
	var tr tagResult
	if err := json.Unmarshal([]byte(cleanJSON(resp.Content)), &tr); err != nil || tr.Title == "" {
		logger.Warn().Err(err).Msg("打标 JSON 解析失败（降级：首行当 title）")
		return fallback
	}
	return tr
}

func firstLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(strings.TrimLeft(ln, "# "))
		if ln == "" {
			continue
		}
		r := []rune(ln)
		if len(r) > 30 {
			return string(r[:30])
		}
		return ln
	}
	return "未命名知识"
}

func cleanJSON(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
