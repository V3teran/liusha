package einoagent_test

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/einoagent"
)

// TestRunSolo_usesProvidedName 验证 RunSolo 以传入 name/desc 装配单代理并跑出终态文字。
// 用无 tool call 的 fakeModel（不调工具即自然收尾）。
func TestRunSolo_usesProvidedName(t *testing.T) {
	ctx := context.Background()
	m := &fakeModel{reply: "分析完成"}

	res, err := einoagent.RunSolo(ctx, "traffic-analysis", "分析一条流量", m, nil,
		"你是分析师", "GET / HTTP/1.1", 5, nil, nil, zerolog.Nop())
	if err != nil {
		t.Fatalf("RunSolo: %v", err)
	}
	if res.FinalText == "" {
		t.Error("FinalText 为空")
	}
}
