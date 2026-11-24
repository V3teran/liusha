package cognition

import (
	"testing"
	"time"
)

// 一轮规划是若干次 LLM 调用，每次调用各自受单步 LLM 看门狗约束；整轮的预算不能比单次
// 调用的上限还短——那样调用慢一点整轮就被掐断，下一轮从头再来。
func TestPlanningRoundBudgetCoversTwoFullLengthCalls(t *testing.T) {
	step := 5 * time.Minute
	if got := planningRoundBudget(step); got != 2*step {
		t.Fatalf("一轮规划的预算应为单步上限的 2 倍（%v），实际 %v", 2*step, got)
	}
}
