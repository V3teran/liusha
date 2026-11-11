package evaluator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
)

// fatalJudge：陈述含 "bad" 的假设报 LLM 未配置（evaluator 致命错误）；其余假设
// 一直裁决到 ctx 取消，用于模拟在途验证。
type fatalJudge struct {
	slowStarted chan struct{}
	slowDone    atomic.Bool
}

func (j *fatalJudge) Judge(ctx context.Context, hypothesis string, _, _ json.RawMessage, _ ReplayFunc) (Verdict, error) {
	if strings.Contains(hypothesis, "bad") {
		<-j.slowStarted // 保证在途验证已开始，再触发致命错误
		return Verdict{}, fmt.Errorf("judge: %w", llm.ErrNotConfigured)
	}
	close(j.slowStarted)
	<-ctx.Done()
	time.Sleep(50 * time.Millisecond) // 收尾耗时：Run 必须等它结束
	j.slowDone.Store(true)
	return Verdict{}, ctx.Err()
}

// 致命错误终结 Run 时，在途验证必须先被取消并收尾：生命周期只等待 Run，Run 一旦提前
// 返回，任务收尾（沙盒释放、目标终态）会与仍在跑的裁决交错。
func TestRun_FatalWaitsForInFlightVerifications(t *testing.T) {
	judge := &fatalJudge{slowStarted: make(chan struct{})}
	gate := New(&fakeGraphStore{verID: "ver-fatal"}, fakeReplayer{res: Result{Evaluation: json.RawMessage(`{}`)}}, &fakeClaimSink{}).
		WithJudge(judge)
	memGraph := explorationgraph.NewMemoryStore()
	a := NewAgent(AgentConfig{
		TaskID: "t-fatal", Gate: gate, EventBus: bus.New(context.Background()), Graph: memGraph,
		MaxConcurrent: 2, PollInterval: time.Hour, Logger: zerolog.Nop(),
	})
	for _, id := range []string{"obs-slow", "obs-bad"} {
		c := explorationgraph.ConfidenceUnverified
		if _, err := memGraph.CreateNode(context.Background(), explorationgraph.Node{
			ID: id, TaskID: "t-fatal", Kind: core.KindObservation, Confidence: &c,
			Content: json.RawMessage(`{"statement":"` + id + `","repro":{"domain":"generic","recipe":{"steps":"s"},"assert":{"description":"d"}}}`),
		}); err != nil {
			t.Fatal(err)
		}
	}

	done := make(chan error, 1)
	go func() { done <- a.Run(context.Background()) }()

	select {
	case err := <-done:
		if !llm.IsConfigError(err) {
			t.Fatalf("Run 应以 LLM 配置错误终结，得 %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("致命错误后 Run 应取消在途验证并返回，而不是等到验证自然超时")
	}
	if !judge.slowDone.Load() {
		t.Fatal("Run 返回时在途验证尚未结束")
	}
}
