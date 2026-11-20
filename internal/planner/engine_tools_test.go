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
// 在审计链里落两条记录（实测 search_corpus 每次调用都成对出现）。
func TestPlanTools_WrapsOnlyGraphTools(t *testing.T) {
	extra := &stubTool{name: "search_corpus"}
	e := NewEngine(nil, zerolog.Nop()).
		WithExtraTools([]registry.Tool{extra}).
		WithToolWrapper(func(t registry.Tool) registry.Tool { return wrappedStub{t} })

	got := e.planTools(nil, []string{"*"})

	byName := map[string]registry.Tool{}
	for _, tool := range got {
		byName[tool.Name()] = tool
	}
	assert.Len(t, byName, 3)
	for _, name := range []string{"observe_state", "evaluate_progress"} {
		assert.IsType(t, wrappedStub{}, byName[name], "%s 应经引擎包装", name)
	}
	assert.Same(t, extra, byName["search_corpus"], "额外工具不应被引擎再次包装")
}

func TestPlanTools_FiltersByWhitelist(t *testing.T) {
	e := NewEngine(nil, zerolog.Nop()).
		WithExtraTools([]registry.Tool{&stubTool{name: "search_corpus"}})

	got := e.planTools(nil, []string{"observe_state"})

	names := make([]string, 0, len(got))
	for _, tool := range got {
		names = append(names, tool.Name())
	}
	assert.Equal(t, []string{"observe_state"}, names)
}
