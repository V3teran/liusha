package monitor

import "testing"

// 监察的系统提示词即章程；场景块拼在末尾，无场景时就是章程本身。
func TestBuildSystemPrompt_AppendsScenario(t *testing.T) {
	if got := (&Agent{charter: "章程"}).buildSystemPrompt(); got != "章程" {
		t.Fatalf("无场景时应为章程本身: %q", got)
	}
	block := "<scenario name=\"渗透测试\">\n正文\n</scenario>"
	if got := (&Agent{charter: "章程", scenario: block}).buildSystemPrompt(); got != "章程\n\n"+block {
		t.Fatalf("场景块应拼在末尾: %q", got)
	}
}
