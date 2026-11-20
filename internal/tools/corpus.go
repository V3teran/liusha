package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/registry"
)

// ─── search_corpus ───────────────────────────────────────────────────────────

var searchCorpusSchema = json.RawMessage(fmt.Sprintf(`{
  "type": "object",
  "properties": {
    "query": {"type": "string", "description": "检索内容：关键词或自然语言描述。"},
    "top_k": {"type": "integer", "description": "最多返回条数，默认 %d，最多 %d。"}
  },
  "required": ["query"]
}`, corpus.DefaultLimit, corpus.MaxLimit))

type searchCorpusTool struct {
	registry.BaseTool
	deps Deps
}

func newSearchCorpusTool(deps Deps, timeout time.Duration) *searchCorpusTool {
	t := &searchCorpusTool{deps: deps}
	t.SetTimeout(timeout)
	return t
}

func (t *searchCorpusTool) Name() string      { return "search_corpus" }
func (t *searchCorpusTool) ShortDesc() string { return "检索跨任务长期知识库" }
func (t *searchCorpusTool) Desc() string {
	return "检索跨任务长期知识库：以往任务沉淀与人工录入的方法、经验与教训，范围是本任务场景下的知识与通用知识。" +
		"结果已按与查询的相关度过滤，至多返回 top_k 条；返回为空表示知识库里没有与查询相关的条目。" +
		"每条注明来源（任务沉淀或人工录入）与印证过它的任务数。"
}
func (t *searchCorpusTool) Schema() json.RawMessage { return searchCorpusSchema }

func (t *searchCorpusTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Query string `json:"query"`
		TopK  int    `json:"top_k"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "search_corpus: 解析参数失败: " + err.Error()}, nil
	}
	if strings.TrimSpace(a.Query) == "" {
		return registry.ToolResult{Error: "search_corpus: query 必填"}, nil
	}
	res, err := t.deps.Corpus.Search(ctx, corpus.Query{Text: a.Query, Scope: t.deps.CorpusScope, Limit: a.TopK})
	if err != nil {
		if errors.Is(err, corpus.ErrInvalid) {
			return registry.ToolResult{Error: "search_corpus: " + err.Error()}, nil
		}
		t.deps.Logger.Warn().Err(err).Msg("知识库检索失败")
		return registry.ToolResult{Error: "search_corpus: 知识库检索失败"}, nil
	}
	return registry.ToolResult{Output: renderSearchResult(res)}, nil
}

// renderSearchResult 把检索结果写成交给 LLM 的文本：先写检索方式，再逐条列出。
func renderSearchResult(res corpus.Result) string {
	var sb strings.Builder
	sb.WriteString("检索方式：")
	switch {
	case res.Fallback != "":
		sb.WriteString("字面匹配（语义检索暂不可用）")
	case res.Mode == corpus.ModeSemantic:
		sb.WriteString("语义相关度")
	default:
		sb.WriteString("字面匹配")
	}
	sb.WriteString("\n\n")
	if len(res.Hits) == 0 {
		sb.WriteString("未找到相关知识条目。")
		return sb.String()
	}
	for i, h := range res.Hits {
		fmt.Fprintf(&sb, "## [%d] %s\n%s\n%s\n\n", i+1, h.Entry.Title, provenance(h.Entry), h.Entry.Content)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func provenance(e corpus.Entry) string {
	src := "任务沉淀"
	if e.Source == corpus.SourceExpert {
		src = "人工录入"
	}
	if e.Confirmations == 0 {
		return "来源：" + src
	}
	return fmt.Sprintf("来源：%s，%d 个任务印证", src, e.Confirmations)
}
