package executor

import (
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
)

type captureProvider struct{ req llm.Request }

func (p *captureProvider) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	p.req = req
	return llm.Response{Content: `{"status":"completed","summary":"s","observations":[]}`}, nil
}
func (p *captureProvider) Stream(_ context.Context, _ llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, llm.ErrStreamingUnsupported
}
func (p *captureProvider) CountTokens(_ context.Context, _ llm.Request) (int, error) { return 0, nil }
func (p *captureProvider) ModelID() string                                           { return "m" }
func (p *captureProvider) ProviderID() string                                        { return "p" }

// 降级推导的假设走同一条落图与裁决管道，配方格式只能有一个权威——输出契约。
// 此前降级请求只发一条 user 消息（章程与契约都不在），又自带「repro 为字符串数组」
// 的格式，按它产出的假设缺 domain 信封与 assert，在复现门必然判无效。
func TestFallbackExtractHypothesis_UsesContract(t *testing.T) {
	provider := &captureProvider{}
	e := NewEngine(EngineConfig{Logger: zerolog.Nop()})
	cfg := &runtime.ReActConfig{SystemPrompt: "章程\n\n输出契约", Objective: "检测 /search 的反射型 XSS", LLMProvider: provider}

	if _, err := e.fallbackExtractHypothesis(context.Background(), cfg, "act-1"); err != nil {
		t.Fatalf("fallbackExtractHypothesis: %v", err)
	}
	msgs := provider.req.Messages
	if len(msgs) != 2 || msgs[0].Role != llm.RoleSystem || msgs[0].Content != cfg.SystemPrompt {
		t.Fatalf("降级请求应以系统提示词（章程 + 输出契约）开头，得 %+v", msgs)
	}
	user := msgs[1].Content
	if !strings.Contains(user, cfg.Objective) {
		t.Errorf("降级请求应携带任务目标:\n%s", user)
	}
	if strings.Contains(user, `"repro": [`) || strings.Contains(user, "每步一个字符串") {
		t.Errorf("降级请求不得自带与契约冲突的配方格式:\n%s", user)
	}
}
