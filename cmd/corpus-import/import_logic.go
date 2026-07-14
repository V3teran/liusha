package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/einollm"
	"github.com/V3teran/liusha/internal/embedding"
)

// tagInstruction 让 light 模型为一段知识生成 title + tags（JSON 输出）。
const tagInstruction = `你是渗透知识库标注器。给定一段知识正文，生成：
- title：一句话主题（≤30 字，概括这段讲什么）
- tags：技术/场景标签数组（如 sso:cas、jwt、fastjson、waf:cloudflare、cve:2023-xxx），2-5 个，用于检索时按标签过滤

严格输出 JSON（无前后缀、无 markdown 代码块）：{"title":"...","tags":["...","..."]}`

// tagResult 是打标 LLM 的产出。
type tagResult struct {
	Title string   `json:"title"`
	Tags  []string `json:"tags"`
}

// importFile 读一个 markdown 文件，按 ## 段切条，逐条打标 + embed + 落库（source=expert）。返回导入条数。
func importFile(ctx context.Context, logger zerolog.Logger, store *corpus.Store, tagger *einollm.Factory, embedder *embedding.Client, path string) (int, error) {
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
		tag := autoTag(ctx, logger, tagger, content)

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

// splitMarkdown 按 ## / # 标题切段——每个标题及其下正文为一条知识。无标题的整篇作一条。
// 空白段跳过。标题行并入正文（保留上下文）。
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
			flush() // 遇新标题，收束上一段
		}
		cur.WriteString(ln)
		cur.WriteString("\n")
	}
	flush()
	return chunks
}

// autoTag 调 light 模型给一段知识打标；失败降级用正文首行当 title、空 tags（不阻塞导入）。
func autoTag(ctx context.Context, logger zerolog.Logger, tagger *einollm.Factory, content string) tagResult {
	fallback := tagResult{Title: firstLine(content), Tags: nil}

	model, err := tagger.For(ctx, "compactor") // light provider
	if err != nil {
		logger.Warn().Err(err).Msg("打标模型解析失败（降级：首行当 title）")
		return fallback
	}
	tctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	out, err := model.Generate(tctx, []*schema.Message{
		schema.SystemMessage(tagInstruction),
		schema.UserMessage(content),
	})
	if err != nil || out == nil {
		logger.Warn().Err(err).Msg("打标调用失败（降级：首行当 title）")
		return fallback
	}
	var tr tagResult
	if err := json.Unmarshal([]byte(cleanJSON(out.Content)), &tr); err != nil || tr.Title == "" {
		logger.Warn().Err(err).Msg("打标 JSON 解析失败（降级：首行当 title）")
		return fallback
	}
	return tr
}

// firstLine 取正文首个非空行（去 markdown 标题符），截断 30 字符当降级 title。
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

// cleanJSON 剥掉 LLM 可能加的 ```json 代码块包裹。
func cleanJSON(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
