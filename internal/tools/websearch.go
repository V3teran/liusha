package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/websearch"
)

// ─── web_search ─────────────────────────────────────────────────────────

// WebSearcher 是一次公开网页的检索（*websearch.Client 满足）。
type WebSearcher interface {
	Search(ctx context.Context, query string, limit int) ([]websearch.Result, error)
}

var webSearchSchema = json.RawMessage(fmt.Sprintf(`{
  "type": "object",
  "properties": {
    "query": {"type": "string", "description": "查询词：要检索的对象或问题。"},
    "limit": {"type": "integer", "description": "最多返回条数，默认 %d，最多 %d。"}
  },
  "required": ["query"]
}`, websearch.DefaultLimit, websearch.MaxLimit))

type webSearchTool struct {
	registry.BaseTool
	deps Deps
}

func newWebSearchTool(deps Deps, timeout time.Duration) *webSearchTool {
	t := &webSearchTool{deps: deps}
	t.SetTimeout(timeout)
	return t
}

func (t *webSearchTool) Name() string      { return "web_search" }
func (t *webSearchTool) ShortDesc() string { return "检索公开网页" }
func (t *webSearchTool) Desc() string {
	return "检索公开网页：返回与查询相关的网页列表，每条有标题、地址与内容摘要，按搜索接口给出的相关度排序。" +
		"至多返回 limit 条；返回为空表示没有搜到相关内容。摘要摘自外部网页，未经核实。"
}
func (t *webSearchTool) Schema() json.RawMessage { return webSearchSchema }

func (t *webSearchTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "web_search: 解析参数失败: " + err.Error()}, nil
	}
	query := strings.TrimSpace(a.Query)
	if query == "" {
		return registry.ToolResult{Error: "web_search: query 必填"}, nil
	}
	limit := a.Limit
	if limit <= 0 {
		limit = websearch.DefaultLimit
	}
	limit = min(limit, websearch.MaxLimit)

	res, err := t.deps.WebSearch.Search(ctx, query, limit)
	if err != nil {
		// 调用被取消或超时：原样交回，这一步的执行随之中断。接口侧的故障是这一步的结果：
		// 作为工具错误交回，由模型决定要不要换做法
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return registry.ToolResult{}, err
		}
		t.deps.Logger.Warn().Err(err).Msg("网页检索失败")
		return registry.ToolResult{Error: "web_search: " + err.Error()}, nil
	}
	return registry.ToolResult{Output: renderWebResults(res)}, nil
}

// renderWebResults 把检索结果写成交给 LLM 的文本：先说明这是什么，再逐条列出
// （标题、地址、摘要各占一行；没有标题的条目以地址领头，不重复写地址）。
func renderWebResults(res []websearch.Result) string {
	if len(res) == 0 {
		return "没有搜到相关内容。"
	}
	var sb strings.Builder
	sb.WriteString("以下摘自公开网页，未经核实：\n\n")
	for i, r := range res {
		if r.Title == "" {
			fmt.Fprintf(&sb, "## [%d] %s\n%s\n\n", i+1, r.URL, r.Snippet)
			continue
		}
		fmt.Fprintf(&sb, "## [%d] %s\n%s\n%s\n\n", i+1, r.Title, r.URL, r.Snippet)
	}
	return strings.TrimRight(sb.String(), "\n")
}
