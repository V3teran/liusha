package web

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/planner"
)

type fakeLister struct {
	calls   int
	batches [][]finding.VulnFinding
	err     error
}

func (f *fakeLister) ListByTaskAndHost(ctx context.Context, taskID, host string, limit int) ([]finding.VulnFinding, error) {
	if f.err != nil {
		return nil, f.err
	}
	i := f.calls
	f.calls++
	if i < len(f.batches) {
		return f.batches[i], nil
	}
	return nil, nil
}

func vf(id string, repro string) finding.VulnFinding {
	f := finding.VulnFinding{ID: id, Target: json.RawMessage(`{"host":"h","path":"/a"}`)}
	if repro != "" {
		f.Repro = json.RawMessage(repro)
	}
	return f
}

const goodRepro = `{"traffic_id":1,"modifications":{},"assert":{"status_code":200}}`

func TestExecutor_Execute_HarvestsOnlyNew(t *testing.T) {
	before := []finding.VulnFinding{vf("old-1", goodRepro)}
	after := []finding.VulnFinding{vf("new-1", goodRepro), vf("old-1", goodRepro)}
	lister := &fakeLister{batches: [][]finding.VulnFinding{before, after}}

	ran := false
	run := func(ctx context.Context, in planner.Move) error { ran = true; return nil }
	op := NewExecutor("scan-1", "task-1", "h", lister, run)

	got, err := op.Execute(context.Background(), planner.Move{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !ran {
		t.Fatal("agent 未运行")
	}
	if len(got) != 1 {
		t.Fatalf("应只收割 1 个新 finding，得 %d", len(got))
	}
	if got[0].TaskID != "scan-1" {
		t.Errorf("TaskID=%q", got[0].TaskID)
	}
}

func TestExecutor_Execute_SkipsNoRepro(t *testing.T) {
	after := []finding.VulnFinding{vf("new-1", ""), vf("new-2", goodRepro)}
	lister := &fakeLister{batches: [][]finding.VulnFinding{nil, after}}
	op := NewExecutor("s", "t", "h", lister, func(context.Context, planner.Move) error { return nil })

	got, err := op.Execute(context.Background(), planner.Move{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("无 repro 应跳过，得 %d", len(got))
	}
}

func TestExecutor_Execute_AgentError(t *testing.T) {
	lister := &fakeLister{batches: [][]finding.VulnFinding{nil}}
	sentinel := errors.New("boom")
	op := NewExecutor("s", "t", "h", lister, func(context.Context, planner.Move) error { return sentinel })

	_, err := op.Execute(context.Background(), planner.Move{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("应透传 agent 错误，得 %v", err)
	}
}

func TestExecutor_Execute_ListError(t *testing.T) {
	lister := &fakeLister{err: errors.New("db down")}
	op := NewExecutor("s", "t", "h", lister, func(context.Context, planner.Move) error { return nil })

	if _, err := op.Execute(context.Background(), planner.Move{}); err == nil {
		t.Fatal("快照失败应报错")
	}
}
