package einoagent_test

import (
	"context"
	"os"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/einoagent"
	"github.com/V3teran/liusha/internal/einollm"
	"github.com/V3teran/liusha/internal/einotools"
	"github.com/V3teran/liusha/internal/finding"
)

// fakeStore 满足 einotools 的 FindingReader+FindingWriter，记录写入用于断言。
type fakeStore struct{ saved []finding.VulnFinding }

func (f *fakeStore) ListByOwnerAndHost(_ context.Context, _, _, _ string, _ int) ([]finding.VulnFinding, error) {
	return nil, nil
}
func (f *fakeStore) Save(_ context.Context, v finding.VulnFinding) (finding.VulnFinding, error) {
	v.ID = "live-1"
	f.saved = append(f.saved, v)
	return v, nil
}

// TestRunTrafficAnalysis_Live 是端到端 live 验证：真 einollm 工厂 + 真 einotools + RunTrafficAnalysis
// 跑一条 SQLi 流量，断言 LLM 调了 write_finding。缺 XIAOMI_API_KEY 时 skip（普通 CI 不跑）。
//
// 跑：XIAOMI_API_KEY=tp-xxx go test ./internal/einoagent/ -run Live -v
func TestRunTrafficAnalysis_Live(t *testing.T) {
	if os.Getenv("XIAOMI_API_KEY") == "" {
		t.Skip("缺 XIAOMI_API_KEY，跳过 live 验证")
	}

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"xiaomi_mimo": {
				Type:         "openai_compat",
				BaseURL:      "https://token-plan-cn.xiaomimimo.com/v1",
				DefaultModel: "mimo-v2.5",
				APIKeyEnv:    "XIAOMI_API_KEY",
			},
		},
		LLM: config.LLMConfig{DefaultProvider: "xiaomi_mimo"},
	}

	ctx := context.Background()
	// per-hunter 独立实例（铁律）：For 每次产新实例
	m, err := einollm.New(cfg).For(ctx, "traffic-analysis")
	if err != nil {
		t.Fatalf("einollm.For: %v", err)
	}

	store := &fakeStore{}
	wf, err := einotools.BuildWriteFinding(store, "passive_session", "owner-live", "hunter-live", "target.example", 7)
	if err != nil {
		t.Fatal(err)
	}
	rf, err := einotools.BuildReadFindings(store, "passive_session", "owner-live", "target.example")
	if err != nil {
		t.Fatal(err)
	}

	instruction := "你是渗透测试侦察兵。分析给你的一条 HTTP 流量（先看响应再回看请求），" +
		"判断涉及的漏洞类型；命中就调 write_finding 落库（写前可用 read_findings 查重）；" +
		"确实无洞就直接文字说明。每步先 reason 一句话再行动。"
	flow := "## 流量请求\nGET /user?id=1%27%20OR%20%271%27%3D%271 HTTP/1.1\nHost: target.example\n\n" +
		"## 流量响应\nHTTP/1.1 500 Internal Server Error\n\n" +
		"body: You have an error in your SQL syntax near \"OR '1'='1\" at line 1"

	res, err := einoagent.RunTrafficAnalysis(ctx, m, []tool.BaseTool{wf, rf}, instruction, flow, nil)
	if err != nil {
		t.Fatalf("RunTrafficAnalysis: %v", err)
	}

	t.Logf("ToolCalls=%v  FinalText=%q", res.ToolCalls, res.FinalText)
	calledWrite := false
	for _, n := range res.ToolCalls {
		if n == "write_finding" {
			calledWrite = true
		}
	}
	if !calledWrite {
		t.Errorf("SQLi 流量应触发 write_finding，实际 ToolCalls=%v", res.ToolCalls)
	}
	if len(store.saved) == 0 {
		t.Error("write_finding 调用后 store 应有落库记录")
	}
}
