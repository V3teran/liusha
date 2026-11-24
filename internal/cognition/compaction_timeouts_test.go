package cognition

import (
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/runtime"
)

// 各角色压缩链的时间预算由本 run 的单步 LLM 上限推导：单步上限热改之后，新开始的 run 按新值算，
// 模型慢时蒸馏的时限跟着变大。
func TestDeps_CompactionTimeoutsFollowTheStepLimit(t *testing.T) {
	d := &Deps{StepLLMTimeout: 10 * time.Minute}
	got := d.compactionTimeouts()
	if got != runtime.CompactionTimeoutsFor(10*time.Minute) || got.Distill != 5*time.Minute {
		t.Fatalf("压缩链的时间预算应由单步 LLM 上限推导，实际 %+v", got)
	}
	d.StepLLMTimeout = time.Minute
	if got := d.compactionTimeouts(); got.Distill != 30*time.Second {
		t.Fatalf("单步上限变化后预算应跟着变，实际 %+v", got)
	}
}
