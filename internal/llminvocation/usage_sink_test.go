package llminvocation

import (
	"context"
	"fmt"
	"testing"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// 排不到账户的在途名额，是本地为账户限流做的节流：审计里与限流同列，不记成其它错误。
func TestErrorKindOf_InflightBusyIsRateLimited(t *testing.T) {
	err := fmt.Errorf("%w（上限 4）：排不到名额，本次调用未发出", llm.ErrInflightBusy)
	if got := ErrorKindOf(llm.Classify(err)); got != "rate_limited" {
		t.Fatalf("排不到在途名额应记为限流，实际 %q", got)
	}
}

// 错误类别取记录携带的分类，而不是在这里拿错误文本重判：哨兵错误只存在于错误链上，
// 文本「账户在途名额已满…」重判出来是「其它错误」。
func TestRecordUsage_TakesErrorKindFromRecord(t *testing.T) {
	s := &Store{ch: make(chan Invocation, 1), log: zerolog.Nop()}
	err := fmt.Errorf("%w（上限 4）：排队未果，没有请求发出", llm.ErrInflightBusy)

	UsageSink{Store: s}.RecordUsage(context.Background(), llm.UsageRecord{
		Provider: "p", Model: "m", Role: "planner",
		Err: err.Error(), ErrClass: llm.Classify(err),
	})

	inv := <-s.ch
	if inv.ErrorKind != "rate_limited" {
		t.Fatalf("排不到在途名额应记为限流，实际 %q", inv.ErrorKind)
	}
	if inv.Error != err.Error() {
		t.Fatalf("错误正文应原样留存，实际 %q", inv.Error)
	}
}

// 入队不看调用方的 ctx。这里记的常常正是一次超时或被取消的调用，它的 ctx 到这里时已经
// 结束——入队若随它一起放弃，丢掉的偏偏是失败现场（实测约一半的超时与取消记录无声丢失）。
func TestRecordUsage_SurvivesEndedContext(t *testing.T) {
	s := &Store{ch: make(chan Invocation, bufferSize), log: zerolog.Nop()}
	ctx, cancel := context.WithTimeout(context.Background(), -1)
	defer cancel()

	for i := 0; i < 32; i++ {
		UsageSink{Store: s}.RecordUsage(ctx, llm.UsageRecord{
			Provider: "p", Model: "m", Role: "planner",
			Err: ctx.Err().Error(), ErrClass: llm.Classify(ctx.Err()),
		})
	}

	if got := len(s.ch); got != 32 {
		t.Fatalf("ctx 已结束时的审计记录应全部入队，实际 32 条里只有 %d 条", got)
	}
}
