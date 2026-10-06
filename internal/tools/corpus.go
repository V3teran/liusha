package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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

type searchCorpusTool struct {
	registry.BaseTool
	deps Deps
}

func newSearchCorpusTool(deps Deps, timeout time.Duration, safe bool) *searchCorpusTool {
	t := &searchCorpusTool{deps: deps}
	t.SetTimeout(timeout)
	t.SetConcurrencySafe(safe)
	return t
}

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
		fmt.Fprintf(&sb, "## [%d] %s\n%s\n\n", i+1, e.Title, e.Content)
	}
	return registry.ToolResult{Output: sb.String()}, nil
}
