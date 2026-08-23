package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/registry"
)

// ─── search_corpus ───────────────────────────────────────────────────────────

var searchCorpusSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "query": {"type": "string", "description": "检索关键词或自然语言描述。"},
    "tags":  {"type": "array", "items": {"type": "string"}, "description": "标签过滤（可选）。"},
    "top_k": {"type": "integer", "description": "最多返回条数，默认 5。"}
  },
  "required": ["query"]
}`)

type searchCorpusTool struct{ deps Deps }

func (t *searchCorpusTool) Name() string      { return "search_corpus" }
func (t *searchCorpusTool) ShortDesc() string { return "检索跨目标长期知识库" }
func (t *searchCorpusTool) Desc() string {
	return "检索跨目标长期知识库：沉淀的可复用打法、专家经验、历史教训。"
}
func (t *searchCorpusTool) Schema() json.RawMessage { return searchCorpusSchema }

func (t *searchCorpusTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Query string   `json:"query"`
		Tags  []string `json:"tags"`
		TopK  int      `json:"top_k"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "search_corpus: 解析参数失败: " + err.Error()}, nil
	}
	if a.Query == "" {
		return registry.ToolResult{Error: "search_corpus: query 必填"}, nil
	}
	if a.TopK <= 0 {
		a.TopK = 5
	}

	var queryVec []float32
	if t.deps.Embedder != nil {
		var err error
		queryVec, err = t.deps.Embedder.EmbedQuery(ctx, a.Query)
		if err != nil {
			// embedder 失败降级 sparse，不中断
			queryVec = nil
		}
	}

	entries, err := t.deps.Corpus.SearchHybrid(ctx, a.Query, queryVec, a.Tags, 20, a.TopK, t.deps.Reranker)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("search_corpus: %v", err)}, nil
	}
	if len(entries) == 0 {
		return registry.ToolResult{Output: "未找到相关知识条目。"}, nil
	}

	var sb strings.Builder
	for i, e := range entries {
		sb.WriteString(fmt.Sprintf("## [%d] %s\n%s\n\n", i+1, e.Title, e.Content))
	}
	return registry.ToolResult{Output: sb.String()}, nil
}

// ─── write_corpus ────────────────────────────────────────────────────────────

var writeCorpusSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "title":   {"type": "string", "description": "知识条目标题（简短精准）。"},
    "content": {"type": "string", "description": "知识正文（可复用打法/经验/教训）。"},
    "tags":    {"type": "array", "items": {"type": "string"}, "description": "标签列表（漏洞类型/工具名等）。"}
  },
  "required": ["title", "content"]
}`)

type writeCorpusTool struct{ deps Deps }

func (t *writeCorpusTool) Name() string      { return "write_corpus" }
func (t *writeCorpusTool) ShortDesc() string { return "向知识库沉淀可复用知识" }
func (t *writeCorpusTool) Desc() string {
	return "向跨目标长期知识库沉淀一条可复用知识（有质量门槛，防噪音）。"
}
func (t *writeCorpusTool) Schema() json.RawMessage { return writeCorpusSchema }

func (t *writeCorpusTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Title   string   `json:"title"`
		Content string   `json:"content"`
		Tags    []string `json:"tags"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "write_corpus: 解析参数失败: " + err.Error()}, nil
	}
	if a.Title == "" || a.Content == "" {
		return registry.ToolResult{Error: "write_corpus: title 和 content 必填"}, nil
	}

	entry := corpus.Entry{
		Title:        a.Title,
		Content:      a.Content,
		Tags:         a.Tags,
		Source:       corpus.SourceAgent,
		SourceTaskID: t.deps.TaskID,
	}
	saved, err := t.deps.Corpus.Add(ctx, entry)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("write_corpus: %v", err)}, nil
	}
	return registry.ToolResult{Output: fmt.Sprintf("知识条目已写入: id=%s title=%q", saved.ID, saved.Title)}, nil
}
