package verifier

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/V3teran/liusha/internal/worldmodel"
)

// fakeWorld 记录 Verifier 对世界模型的写入，供断言"门的副作用"。
type fakeWorld struct {
	verifications []worldmodel.Verification
	nodes         []worldmodel.Node
	verID         string
}

func (f *fakeWorld) RecordVerification(_ context.Context, v worldmodel.Verification) (string, error) {
	f.verifications = append(f.verifications, v)
	return f.verID, nil
}

func (f *fakeWorld) UpsertNode(_ context.Context, n worldmodel.Node) (worldmodel.Node, error) {
	n.ID = "node-" + string(n.Kind)
	f.nodes = append(f.nodes, n)
	return n, nil
}

// fakeReplayer 按预设结论回应复现。
type fakeReplayer struct {
	res Result
	err error
}

func (f fakeReplayer) Replay(context.Context, json.RawMessage) (Result, error) {
	return f.res, f.err
}

func baseAttempt() Attempt {
	return Attempt{
		TaskID:     "asg-1",
		LeadID:     "lead-sqli",
		Kind:       worldmodel.KindFinding,
		Target:     worldmodel.TargetRef{Domain: "web", RefKind: "host", Locator: "t.local"},
		Primitives: json.RawMessage(`[{"op":"http_request"}]`),
		Attrs:      json.RawMessage(`{"severity":"high"}`),
	}
}

// 复现坐实：落 confirmed verification + 晋升 confirmed 节点，verified_by 回指取证记录。
func TestPromote_Confirmed(t *testing.T) {
	w := &fakeWorld{verID: "ver-99"}
	v := New(w, fakeReplayer{res: Result{Confirmed: true, Evidence: json.RawMessage(`{"poc":"x"}`), DurationMs: 42}})

	node, err := v.Promote(context.Background(), baseAttempt())
	if err != nil {
		t.Fatalf("Promote 出错: %v", err)
	}
	if node == nil {
		t.Fatal("坐实应返回晋升后的节点")
	}
	if node.Confidence != worldmodel.ConfConfirmed {
		t.Errorf("节点应 confirmed, got %s", node.Confidence)
	}
	if node.VerifiedBy == nil || *node.VerifiedBy != "ver-99" {
		t.Errorf("VerifiedBy 应回指 ver-99, got %v", node.VerifiedBy)
	}
	if len(w.verifications) != 1 || w.verifications[0].Outcome != worldmodel.OutcomeConfirmed {
		t.Errorf("应落 1 条 confirmed verification, got %+v", w.verifications)
	}
	if len(w.nodes) != 1 {
		t.Errorf("应晋升 1 个节点, got %d", len(w.nodes))
	}
}

// 复现证伪：落 refuted verification 留档，但不进图。铁律——图只存坐实态。
func TestPromote_Refuted(t *testing.T) {
	w := &fakeWorld{verID: "ver-1"}
	v := New(w, fakeReplayer{res: Result{Confirmed: false, Evidence: json.RawMessage(`{"reason":"no repro"}`)}})

	node, err := v.Promote(context.Background(), baseAttempt())
	if err != nil {
		t.Fatalf("证伪不是错误, got err: %v", err)
	}
	if node != nil {
		t.Errorf("证伪不应进图, got node %+v", node)
	}
	if len(w.verifications) != 1 || w.verifications[0].Outcome != worldmodel.OutcomeRefuted {
		t.Errorf("应留 1 条 refuted verification 供审计, got %+v", w.verifications)
	}
	if len(w.nodes) != 0 {
		t.Errorf("证伪不应写节点, got %d", len(w.nodes))
	}
}

// 复现执行失败：门报错，且不落任何 verification/节点（避免脏证据链）。
func TestPromote_ReplayError(t *testing.T) {
	w := &fakeWorld{}
	v := New(w, fakeReplayer{err: errors.New("boom")})

	if _, err := v.Promote(context.Background(), baseAttempt()); err == nil {
		t.Fatal("复现失败应报错")
	}
	if len(w.verifications) != 0 || len(w.nodes) != 0 {
		t.Errorf("复现失败不应有任何写入, ver=%d node=%d", len(w.verifications), len(w.nodes))
	}
}

// 无 Replayer：无复现能力即无晋升——直接报错，不放行。
func TestPromote_NoReplayer(t *testing.T) {
	v := New(&fakeWorld{}, nil)
	if _, err := v.Promote(context.Background(), baseAttempt()); err == nil {
		t.Fatal("无 Replayer 应报错")
	}
}

// 必填校验：TaskID / Kind 缺失即拒。
func TestPromote_Validation(t *testing.T) {
	v := New(&fakeWorld{}, fakeReplayer{res: Result{Confirmed: true}})

	noScan := baseAttempt()
	noScan.TaskID = ""
	if _, err := v.Promote(context.Background(), noScan); err == nil {
		t.Error("缺 TaskID 应报错")
	}

	noKind := baseAttempt()
	noKind.Kind = ""
	if _, err := v.Promote(context.Background(), noKind); err == nil {
		t.Error("缺 Kind 应报错")
	}
}
