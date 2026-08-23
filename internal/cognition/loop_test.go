package cognition

import (
	"context"
	"errors"
	"testing"

	"github.com/V3teran/liusha/internal/planner"
	"github.com/V3teran/liusha/internal/verifier"
	"github.com/V3teran/liusha/internal/worldmodel"
)

// statefulPlanner 按调用序返回预设 intent 批次，覆盖「晋升改变 frontier」的动态场景。
type statefulPlanner struct {
	fn    func(call int) []planner.Move
	calls int
	err   error
}

func (s *statefulPlanner) Plan(context.Context, string) ([]planner.Move, error) {
	if s.err != nil {
		return nil, s.err
	}
	c := s.calls
	s.calls++
	return s.fn(c), nil
}

type fakeExecutor struct {
	fn func(in planner.Move) ([]verifier.Attempt, error)
}

func (o fakeExecutor) Execute(_ context.Context, in planner.Move) ([]verifier.Attempt, error) {
	return o.fn(in)
}

type fakePromoter struct {
	fn func(a verifier.Attempt) (*worldmodel.Node, error)
}

func (p fakePromoter) Promote(_ context.Context, a verifier.Attempt) (*worldmodel.Node, error) {
	return p.fn(a)
}

func move(kind planner.MoveKind, node string) planner.Move {
	return planner.Move{Kind: kind, OnNodeID: node}
}

// 恒晋升成功的 promoter：返回非 nil 节点。
func promoteOK() fakePromoter {
	return fakePromoter{fn: func(verifier.Attempt) (*worldmodel.Node, error) {
		return &worldmodel.Node{ID: "n"}, nil
	}}
}

func TestRun_EmptyTaskID_Errors(t *testing.T) {
	l := New(&statefulPlanner{fn: func(int) []planner.Move { return nil }}, fakeExecutor{}, promoteOK(), 0)
	if _, err := l.Run(context.Background(), ""); err == nil {
		t.Fatal("空 taskID 应报错")
	}
}

func TestRun_EmptyFrontier_StopsExhausted(t *testing.T) {
	l := New(
		&statefulPlanner{fn: func(int) []planner.Move { return nil }},
		fakeExecutor{fn: func(planner.Move) ([]verifier.Attempt, error) { return nil, nil }},
		promoteOK(), 0,
	)
	rep, err := l.Run(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.StopWhy != stopExhausted || rep.Steps != 0 {
		t.Fatalf("空 frontier 应立即耗尽终止, got %+v", rep)
	}
}

// 核心不变量：Executor 不产晋升时，环不得对同一 intent 空转到 maxSteps。
func TestRun_NoPromotion_DoesNotSpin(t *testing.T) {
	// Planner 恒返回同两条 intent（模拟 frontier 不因执行改变）。
	sp := &statefulPlanner{fn: func(int) []planner.Move {
		return []planner.Move{
			move(planner.MoveExploit, "a1"),
			move(planner.MoveExploit, "a2"),
		}
	}}
	var executed []string
	op := fakeExecutor{fn: func(in planner.Move) ([]verifier.Attempt, error) {
		executed = append(executed, in.OnNodeID)
		return nil, nil // 无候选晋升
	}}
	l := New(sp, op, promoteOK(), 100)
	rep, err := l.Run(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 两条各试一次即耗尽，不触顶。
	if rep.StopWhy != stopExhausted {
		t.Fatalf("应因无未试 intent 而耗尽终止, got %s", rep.StopWhy)
	}
	if rep.Steps != 2 {
		t.Fatalf("两条 intent 应各执行一次, got steps=%d executed=%v", rep.Steps, executed)
	}
}

// 晋升产生新节点 → 新 frontier → 被拾起，体现认知环推进。
func TestRun_PromotionEvolvesFrontier(t *testing.T) {
	sp := &statefulPlanner{fn: func(call int) []planner.Move {
		// 第 0 轮：recon t1。之后：exploit a1（模拟 recon 晋升出 a1 资产）。再之后：空。
		switch {
		case call == 0:
			return []planner.Move{move(planner.MoveEnumerate, "t1")}
		case call == 1:
			return []planner.Move{move(planner.MoveExploit, "a1")}
		default:
			return nil
		}
	}}
	op := fakeExecutor{fn: func(in planner.Move) ([]verifier.Attempt, error) {
		return []verifier.Attempt{{TaskID: "s1", Kind: worldmodel.KindAsset}}, nil
	}}
	l := New(sp, op, promoteOK(), 100)
	rep, err := l.Run(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Steps != 2 || rep.Promoted != 2 {
		t.Fatalf("应执行 2 步各晋升 1 节点, got %+v", rep)
	}
	if rep.StopWhy != stopExhausted {
		t.Fatalf("末轮空 frontier 应耗尽终止, got %s", rep.StopWhy)
	}
}

// 战术失败（Executor 返回普通 error）不炸环，交回重规划。
func TestRun_ExecutorError_DoesNotBreakLoop(t *testing.T) {
	sp := &statefulPlanner{fn: func(call int) []planner.Move {
		if call == 0 {
			return []planner.Move{move(planner.MoveExploit, "a1")}
		}
		return nil // 第二轮：a1 已试过且 planner 也不再给 → 耗尽
	}}
	op := fakeExecutor{fn: func(planner.Move) ([]verifier.Attempt, error) {
		return nil, errors.New("payload 打不通")
	}}
	l := New(sp, op, promoteOK(), 100)
	rep, err := l.Run(context.Background(), "s1")
	if err != nil {
		t.Fatalf("战术失败不应上抛: %v", err)
	}
	if rep.Steps != 1 || rep.Promoted != 0 {
		t.Fatalf("失败步计入 steps 但零晋升, got %+v", rep)
	}
}

// ctx 取消即时终止。
func TestRun_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sp := &statefulPlanner{fn: func(int) []planner.Move {
		return []planner.Move{move(planner.MoveEnumerate, "t1")}
	}}
	l := New(sp, fakeExecutor{fn: func(planner.Move) ([]verifier.Attempt, error) { return nil, nil }}, promoteOK(), 100)
	rep, err := l.Run(ctx, "s1")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("应返回 ctx.Canceled, got %v", err)
	}
	if rep.StopWhy != stopCanceled {
		t.Fatalf("StopWhy 应 canceled, got %s", rep.StopWhy)
	}
}

// Planner 报错上抛（区别于战术失败：规划是环的地基，坏了无法继续）。
func TestRun_PlannerError_Propagates(t *testing.T) {
	sp := &statefulPlanner{err: errors.New("读图失败")}
	l := New(sp, fakeExecutor{fn: func(planner.Move) ([]verifier.Attempt, error) { return nil, nil }}, promoteOK(), 100)
	if _, err := l.Run(context.Background(), "s1"); err == nil {
		t.Fatal("规划失败应上抛")
	}
}

// Executor 返回 ctx.DeadlineExceeded：视为环级中止，上抛而非吞掉当战术失败。
func TestRun_ExecutorCtxError_Propagates(t *testing.T) {
	sp := &statefulPlanner{fn: func(int) []planner.Move {
		return []planner.Move{move(planner.MoveExploit, "a1")}
	}}
	op := fakeExecutor{fn: func(planner.Move) ([]verifier.Attempt, error) {
		return nil, context.DeadlineExceeded
	}}
	l := New(sp, op, promoteOK(), 100)
	if _, err := l.Run(context.Background(), "s1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Executor 的 ctx 超时应上抛, got %v", err)
	}
}

// Verifier 门本身出错（非证伪）上抛：落库/复现执行故障不该被静默。
func TestRun_PromoteError_Propagates(t *testing.T) {
	sp := &statefulPlanner{fn: func(int) []planner.Move {
		return []planner.Move{move(planner.MoveExploit, "a1")}
	}}
	op := fakeExecutor{fn: func(planner.Move) ([]verifier.Attempt, error) {
		return []verifier.Attempt{{TaskID: "s1", Kind: worldmodel.KindFinding}}, nil
	}}
	vp := fakePromoter{fn: func(verifier.Attempt) (*worldmodel.Node, error) {
		return nil, errors.New("落库失败")
	}}
	l := New(sp, op, vp, 100)
	if _, err := l.Run(context.Background(), "s1"); err == nil {
		t.Fatal("晋升门出错应上抛")
	}
}

// Verifier 证伪（node=nil, err=nil）：不增 promoted，不算错误，环继续。
func TestRun_PromoteRefuted_NotCounted(t *testing.T) {
	sp := &statefulPlanner{fn: func(call int) []planner.Move {
		if call == 0 {
			return []planner.Move{move(planner.MoveExploit, "a1")}
		}
		return nil
	}}
	op := fakeExecutor{fn: func(planner.Move) ([]verifier.Attempt, error) {
		return []verifier.Attempt{{TaskID: "s1", Kind: worldmodel.KindFinding}}, nil
	}}
	vp := fakePromoter{fn: func(verifier.Attempt) (*worldmodel.Node, error) {
		return nil, nil // 证伪
	}}
	l := New(sp, op, vp, 100)
	rep, err := l.Run(context.Background(), "s1")
	if err != nil {
		t.Fatalf("证伪非错误: %v", err)
	}
	if rep.Attempts != 1 || rep.Promoted != 0 {
		t.Fatalf("证伪应计 attempt 不计 promoted, got %+v", rep)
	}
}

// maxSteps 触顶保护：Planner 每轮给全新 intent（frontier 永不耗尽），必须靠上限刹停。
func TestRun_MaxStepsGuard(t *testing.T) {
	sp := &statefulPlanner{fn: func(call int) []planner.Move {
		// 每轮给带唯一 node 的新 intent，晋升也不会让它消失 → 无限 frontier。
		return []planner.Move{move(planner.MoveExploit, string(rune('a'+call%26)) + string(rune('0'+call)))}
	}}
	op := fakeExecutor{fn: func(planner.Move) ([]verifier.Attempt, error) {
		return []verifier.Attempt{{TaskID: "s1", Kind: worldmodel.KindFinding}}, nil
	}}
	l := New(sp, op, promoteOK(), 5)
	rep, err := l.Run(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.StopWhy != stopMaxSteps || rep.Steps != 5 {
		t.Fatalf("应触顶终止于 5 步, got %+v", rep)
	}
}
