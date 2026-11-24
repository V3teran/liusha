package executor

import (
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/runtime"
)

// 上下文压缩链跑在一步推理内部、两次进展之间，它的时间算在看门狗的无进展窗口里：
// 停摆窗口必须容得下「压缩链超时 + 单步 LLM 上限 + 工具超时上限」，余下的是给不记进展
// 的杂务留的固定余量——否则「压缩慢、主调用也慢」的一步会被误判为停摆而按失败结束。
// 两个上限取多少都与这层关系无关：压缩链的超时是直接计入窗口的。
func TestStallTimeoutCoversCompactionChain(t *testing.T) {
	for _, tc := range []struct{ stepLLM, stepTool time.Duration }{
		{5 * time.Minute, 30 * time.Minute}, // 出厂值
		{30 * time.Second, 5 * time.Second}, // 上限调得很小时同样成立
	} {
		window := StallTimeout(tc.stepLLM, tc.stepTool)
		longest := runtime.ChainTimeout + tc.stepLLM + tc.stepTool
		if window < longest {
			t.Fatalf("停摆窗口 %v 容不下一步的最长合法耗时 %v（上限 %v / %v）", window, longest, tc.stepLLM, tc.stepTool)
		}
		if slack := window - longest; slack != stallSlack {
			t.Fatalf("超出一步最长合法耗时的余量应为 %v，实际 %v", stallSlack, slack)
		}
	}
}
