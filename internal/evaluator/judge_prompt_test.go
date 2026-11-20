package evaluator

import (
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

// 场景块拼在评估者系统提示词末尾；无场景时不加。
func TestJudgeSystemPrompt_AppendsScenario(t *testing.T) {
	plain := NewRouterJudge(nil, zerolog.Nop()).WithCharter("章程").systemPrompt()
	if strings.Contains(plain, "<scenario") {
		t.Fatalf("无场景时不应有场景块:\n%s", plain)
	}
	block := "<scenario name=\"渗透测试\">\n正文\n</scenario>"
	got := NewRouterJudge(nil, zerolog.Nop()).WithCharter("章程").WithScenario(block).systemPrompt()
	if got != plain+"\n\n"+block {
		t.Fatalf("场景块应拼在末尾:\n%s", got)
	}
}

// 判定 JSON 的 refute_reason 原样带出，取值的合法性由落标处统一判定。
func TestParseVerdict_RefuteReason(t *testing.T) {
	v, err := parseVerdict(`{"verdict":"refuted","confidence":0.6,"reasoning":"目标限流","refute_reason":"not_reproducible"}`)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if v.RefuteReason != RefuteNotReproducible {
		t.Fatalf("refute_reason 应原样带出，得 %q", v.RefuteReason)
	}
}
