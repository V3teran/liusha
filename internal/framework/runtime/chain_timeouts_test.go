package runtime

import (
	"testing"
	"time"
)

// 压缩链的时间预算由单步 LLM 看门狗上限推导：蒸馏的时限是它的一半，链的总超时比蒸馏略长——
// 蒸馏先于链到期，慢的蒸馏走降级路径（裁掉前缀、不留纪要），而不是耗尽链超时让整步推理报错。
// 随单步上限走：模型慢时把上限调大，蒸馏的时限跟着变大；写死的常数做不到这一点。
func TestCompactionTimeoutsFollowTheStepLimit(t *testing.T) {
	for _, step := range []time.Duration{30 * time.Second, 5 * time.Minute, 15 * time.Minute} {
		got := CompactionTimeoutsFor(step)
		if got.Distill != step/2 {
			t.Fatalf("单步上限 %v：蒸馏时限应为它的一半 %v，实际 %v", step, step/2, got.Distill)
		}
		if got.Chain <= got.Distill || got.Chain > got.Distill+time.Minute {
			t.Fatalf("单步上限 %v：链超时应略长于蒸馏时限，实际链 %v 蒸馏 %v", step, got.Chain, got.Distill)
		}
	}
	// 出厂单步上限 300 秒时为 150 秒：实测依据见 CompactionTimeoutsFor。
	if got := CompactionTimeoutsFor(5 * time.Minute); got.Distill != 150*time.Second {
		t.Fatalf("出厂单步上限下蒸馏时限应为 150 秒，实际 %v", got.Distill)
	}
}

// 装配出的压缩链用的就是给它的预算：蒸馏拿 Distill，链的总超时是 Chain。
func TestCompactionChainTakesItsTimeouts(t *testing.T) {
	timeouts := CompactionTimeoutsFor(8 * time.Minute)
	chain := NewCompactionChain(1<<20, "test", nil, timeouts)
	if chain.timeoutSec != int(timeouts.Chain/time.Second) {
		t.Fatalf("链的总超时应为 %v，实际 %d 秒", timeouts.Chain, chain.timeoutSec)
	}
	compactor, ok := chain.modifiers[0].(*ContextCompactor)
	if !ok {
		t.Fatalf("链的第一个修改器应是压缩器，实际 %T", chain.modifiers[0])
	}
	if compactor.distillTimeout != timeouts.Distill {
		t.Fatalf("蒸馏时限应为 %v，实际 %v", timeouts.Distill, compactor.distillTimeout)
	}
}

// 测试里装配压缩器、压缩链时用的时间预算（被测逻辑只关心裁切与纪要，不关心时限）。
var (
	testTimeouts       = CompactionTimeouts{Distill: time.Minute, Chain: 65 * time.Second}
	testDistillTimeout = testTimeouts.Distill
)
