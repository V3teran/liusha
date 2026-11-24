package planner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// 自适应预算：预算不足型失败放大下一轮预算（翻倍、封顶基准 3 倍），其它失败不动
// 预算。回归背景：GLM 慢速期 3 分钟基准预算必然超时，同预算重试是确定性失败——
// 规划循环 12 分钟停摆三次复现。
func TestIntelligenceBudgetEscalation(t *testing.T) {
	i := NewEngine(nil, testLogger()).WithRoundBudget(3 * time.Minute)
	if i.budget != i.roundBudget {
		t.Fatalf("初始预算应等于基准: %v", i.budget)
	}

	// 两次预算不足（一次预算用尽、一次排不到在途名额）：预算 3m → 6m → 9m（封顶 3×3m=9m）
	i.adaptBudget(context.DeadlineExceeded)
	if i.budget != 6*time.Minute {
		t.Fatalf("第一次放大应翻倍到 6m: %v", i.budget)
	}
	i.adaptBudget(fmtWrap{context.DeadlineExceeded})
	if i.budget != 9*time.Minute {
		t.Fatalf("第二次放大应到 9m（wrapped 错误同样生效）: %v", i.budget)
	}

	// 已封顶：继续失败不再放大
	i.adaptBudget(context.DeadlineExceeded)
	if i.budget != 9*time.Minute {
		t.Fatalf("封顶后不得放大: %v", i.budget)
	}

	// 与预算无关的失败不动预算
	i.adaptBudget(errors.New("http 500"))
	if i.budget != 9*time.Minute {
		t.Fatalf("与预算无关的失败不得动预算: %v", i.budget)
	}

	// 成功回落基准（Plan 内联语义：i.budget = i.roundBudget）
	i.budget = i.roundBudget
	if i.budget != 3*time.Minute {
		t.Fatalf("成功后应回落基准: %v", i.budget)
	}
}

// 排不到账户的在途名额同样算预算不足：这一轮为名额排队排到了预算的一半仍没排到，
// 换同一预算重来只会再排一次同样的队。实测中它紧随一次长调用出现——本轮的预算已被
// 前面的调用用掉，这一刻有没有空位决定了它被报成「排不到」还是「超时」，只认后者时
// 同一个原因得到两种处置。
func TestIntelligenceBudgetEscalatesWhenSlotsWereBusy(t *testing.T) {
	i := NewEngine(nil, testLogger()).WithRoundBudget(3 * time.Minute)
	busy := fmtWrap{fmtWrap{llm.ErrInflightBusy}} // 经运行时与重试层层层包装后的形态

	i.adaptBudget(busy)
	if i.budget != 6*time.Minute {
		t.Fatalf("排不到在途名额后应放大到 6m: %v", i.budget)
	}
	i.adaptBudget(busy)
	if i.budget != 9*time.Minute {
		t.Fatalf("再次排不到应放大到封顶 9m: %v", i.budget)
	}
	i.adaptBudget(busy)
	if i.budget != 9*time.Minute {
		t.Fatalf("封顶后不得放大: %v", i.budget)
	}
}

// fmtWrap 模拟被上层 %w 包装过的错误链。
type fmtWrap struct{ err error }

func (w fmtWrap) Error() string { return "wrapped: " + w.err.Error() }
func (w fmtWrap) Unwrap() error { return w.err }

func testLogger() zerolog.Logger { return zerolog.Nop() }

// 基准预算是整轮 ReAct 的墙钟上限，经 WithRoundBudget 给定；它同时是当前生效预算的起点
// （只改基准不动当前预算的话，首次放大之前这一轮仍按旧预算被掐断）。
func TestRoundBudgetIsTheStartingBudget(t *testing.T) {
	i := NewEngine(nil, testLogger())
	if i.budget != 0 || i.roundBudget != 0 {
		t.Fatalf("未给定预算时应为不设限，实际基准 %v 当前 %v", i.roundBudget, i.budget)
	}
	i.WithRoundBudget(10 * time.Minute)
	if i.roundBudget != 10*time.Minute || i.budget != 10*time.Minute {
		t.Fatalf("给定预算后基准与当前预算都应是它，实际基准 %v 当前 %v", i.roundBudget, i.budget)
	}
	// 放大后的预算在成功时回落到基准，而不是回到某个写死的值。
	i.adaptBudget(context.DeadlineExceeded)
	if i.budget != 20*time.Minute {
		t.Fatalf("预算不足后应放大到 20m，实际 %v", i.budget)
	}
	i.budget = i.roundBudget // Plan 内联语义：成功即回落
	if i.budget != 10*time.Minute {
		t.Fatalf("成功后应回落到给定的基准 10m，实际 %v", i.budget)
	}
}
