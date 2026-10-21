package executor

import "testing"

// terminalIsJSON 的围栏判定：模型常以 ```json 围栏包裹协议输出，守卫须剥后判定，
// 否则围栏形态的执行终稿错过协议叙述、以原文直出时间线。
func TestTerminalIsJSONFenceVariants(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{`{"a":1}`, true},
		{"```json\n{\"a\":1}\n```", true},
		{"```\n{\"a\":1}\n```", true},
		{"执行完成，共发现 3 个问题", false},
		// 混合文本（散文 + 内嵌 JSON）不叙述：散文部分不经协议摘要覆盖，原文直出。
		{"前文说明\n{\"a\":1}", false},
	}
	for _, c := range cases {
		if got := terminalIsJSON(c.in); got != c.want {
			t.Errorf("terminalIsJSON(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
