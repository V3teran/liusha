package llm

import (
	"context"
	"time"
)

// testGen 是 llm 包测试共用的可编程 Generator stub，替代散落的 stubGen / mockGen / fakeGen。
//
// 三种使用场景按字段组合自动选择：
//   - 序列模式（seq 非空）：第 i 次 Generate 返 seq[i]（nil 表成功，content 走 content 字段）—— 重试场景
//   - 固定模式（res 非零）：每次 Generate 返同一 (res, err) —— instrument 测试
//   - 工厂默认（都不设）：Generate 返 Result{Provider, Model} —— factory 测试
//
// calls 字段记录 Generate 调用次数，外部可读用于断言（mockGen 旧用法）。
type testGen struct {
	provider string
	model    string
	tag      string // provider 为空时 Provider() 返此值
	res      Result
	err      error
	sleep    time.Duration
	seq      []error // 错误序列
	content  string  // seq 模式成功时 content；默认 "ok-<tag>"
	calls    int
}

func (g *testGen) Provider() string {
	if g.provider != "" {
		return g.provider
	}
	return g.tag
}

func (g *testGen) Model() string { return g.model }

func (g *testGen) Generate(_ context.Context, _ []Message, _ []ToolSchema) (Result, error) {
	if g.sleep > 0 {
		time.Sleep(g.sleep)
	}
	idx := g.calls
	g.calls++
	// tag 模式（替代旧 mockGen）：tag 非空时返 "ok-<tag>"，seq 控制错误序列
	if g.tag != "" {
		if idx < len(g.seq) {
			if e := g.seq[idx]; e != nil {
				return Result{}, e
			}
		}
		c := g.content
		if c == "" {
			c = "ok-" + g.tag
		}
		return Result{Content: c, Provider: g.Provider(), Model: g.model}, nil
	}
	// 固定模式（替代旧 stubGen / fakeGen）：直接返 (res, err)，caller 需自己显式设 res
	return g.res, g.err
}
