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
