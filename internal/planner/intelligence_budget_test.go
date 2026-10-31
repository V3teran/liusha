package planner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// 自适应预算：deadline 型失败放大下一轮预算（翻倍、封顶基准 3 倍），
// 非 deadline 失败不动预算。回归背景：GLM 慢速期 3 分钟基准预算必然超时，
// 同预算重试是确定性失败——规划循环 12 分钟停摆三次复现。
func TestIntelligenceBudgetEscalation(t *testing.T) {
	i := NewEngine(nil, testLogger())
	if i.budget != i.timeout {
		t.Fatalf("初始预算应等于基准: %v", i.budget)
	}

	// 两次 deadline 失败：预算 3m → 6m → 9m（封顶 3×3m=9m）
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

	// 非 deadline 失败不动预算
	i.adaptBudget(errors.New("http 429"))
	if i.budget != 9*time.Minute {
		t.Fatalf("非 deadline 失败不得动预算: %v", i.budget)
	}

	// 成功回落基准（Plan 内联语义：i.budget = i.timeout）
	i.budget = i.timeout
	if i.budget != 3*time.Minute {
		t.Fatalf("成功后应回落基准: %v", i.budget)
	}
}

// fmtWrap 模拟被上层 %w 包装过的错误链。
type fmtWrap struct{ err error }

func (w fmtWrap) Error() string { return "wrapped: " + w.err.Error() }
func (w fmtWrap) Unwrap() error { return w.err }

func testLogger() zerolog.Logger { return zerolog.Nop() }
