package planner

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"

	"github.com/V3teran/liusha/internal/registry"
)

type stubTool struct {
	registry.BaseTool
	name string
}

func (s *stubTool) Name() string            { return s.name }
func (s *stubTool) ShortDesc() string       { return s.name }
func (s *stubTool) Desc() string            { return s.name }
func (s *stubTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (s *stubTool) Execute(context.Context, json.RawMessage) (registry.ToolResult, error) {
	return registry.ToolResult{}, nil
}

type wrappedStub struct{ registry.Tool }

// 额外工具由装配层包装后注入，引擎只包装自有的图读工具：再包一层会让每次调用
// 在审计链里落两条记录（实测 corpus_search 每次调用都成对出现）。
func TestPlanTools_WrapsOnlyGraphTools(t *testing.T) {
	extra := &stubTool{name: "corpus_search"}
	e := NewEngine(nil, zerolog.Nop()).
		WithExtraTools([]registry.Tool{extra}).
		WithToolWrapper(func(t registry.Tool) registry.Tool { return wrappedStub{t} })

	got := e.planTools(nil, []string{"*"})

	byName := map[string]registry.Tool{}
	for _, tool := range got {
		byName[tool.Name()] = tool
	}
	assert.Len(t, byName, 3)
	for _, name := range []string{"graph_read", "progress_evaluate"} {
		assert.IsType(t, wrappedStub{}, byName[name], "%s 应经引擎包装", name)
	}
	assert.NotEqual(t, wrappedStub{}, byName["corpus_search"], "额外工具不应被引擎再次包装进审计链")
}

// 每轮规划是一次新的推理，看不到前几轮发出过的知识库查询：引擎在本次运行内记下它们，写进
// 状态摘要（「同一方向只检索一次」才有据可依）。
func TestPlanTools_RecordsKnowledgeQueries(t *testing.T) {
	e := NewEngine(nil, zerolog.Nop()).WithExtraTools([]registry.Tool{&stubTool{name: "corpus_search"}})

	for _, args := range []string{`{"query":" 诱饵 真假分辨 "}`, `{"query":""}`, `{"query":"诱饵 真假分辨"}`, `not json`} {
		for _, tool := range e.planTools(nil, []string{"*"}) {
			if tool.Name() == "corpus_search" {
				_, _ = tool.Execute(context.Background(), json.RawMessage(args))
			}
		}
	}

	assert.Equal(t, []string{"诱饵 真假分辨"}, e.knowledgeQueries, "去掉首尾空白、空查询不记、重复只记一次")
	section := buildKnowledgeQueriesSection(e.knowledgeQueries)
	assert.Contains(t, section, "## 本次运行已检索过的知识库查询\n- 诱饵 真假分辨\n")
	assert.Empty(t, buildKnowledgeQueriesSection(nil))
}

func TestPlanTools_FiltersByWhitelist(t *testing.T) {
	e := NewEngine(nil, zerolog.Nop()).
		WithExtraTools([]registry.Tool{&stubTool{name: "corpus_search"}})

	got := e.planTools(nil, []string{"graph_read"})

	names := make([]string, 0, len(got))
	for _, tool := range got {
		names = append(names, tool.Name())
	}
	assert.Equal(t, []string{"graph_read"}, names)
}
