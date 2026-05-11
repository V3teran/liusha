package done_validator

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/V3teran/liusha/internal/engagement"
)

// fakeFactReader 实现 FactReader（v1.2：只需 ReadStateScoped），返回固定 State 或 err。
type fakeFactReader struct {
	state []byte
	err   error
}

func (f *fakeFactReader) ReadStateScoped(_ context.Context, _ string, _ engagement.ReadOpts) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.state, nil
}

// fakeFindingChecker 模拟 finding 表的 dedup_key 命中。
type fakeFindingChecker struct {
	exists bool
	err    error
	gotKey string
	gotEID string
}

func (f *fakeFindingChecker) HasDedupKey(_ context.Context, eid, key string) (bool, error) {
	f.gotKey = key
	f.gotEID = eid
	if f.err != nil {
		return false, f.err
	}
	return f.exists, nil
}

// stateWithEvidence 构造 v1.2 notes 结构的 State JSON：
// 把 evidence 内容写成 kind=observation 的 notes，boundary 写成 kind=boundary，
// 都标记 task_id="tid-1"（与测试构造的 BACValidator 一致）。
func stateWithEvidence(evidence, boundary []string) []byte {
	type note struct {
		Kind    string `json:"kind"`
		Content string `json:"content"`
		TaskID  string `json:"agent_run_id"`
	}
	notes := []note{}
	for _, c := range evidence {
		notes = append(notes, note{Kind: "observation", Content: c, TaskID: "tid-1"})
	}
	for _, c := range boundary {
		notes = append(notes, note{Kind: "boundary", Content: c, TaskID: "tid-1"})
	}
	notesRaw, _ := json.Marshal(map[string]any{"notes": notes})
	state := map[string]json.RawMessage{
		"notes": notesRaw,
	}
	out, _ := json.Marshal(state)
	return out
}

// emptyState 构造一个 notes 空的 State。
func emptyState() []byte {
	state := map[string]json.RawMessage{
		"notes": json.RawMessage(`{}`),
	}
	out, _ := json.Marshal(state)
	return out
}

// TestBACValidator_RejectMissingReason —— args 中无 reason 字段。
func TestBACValidator_RejectMissingReason(t *testing.T) {
	v := NewBACValidator(
		&fakeFactReader{state: stateWithEvidence([]string{"e1"}, []string{"b1"})},
		&fakeFindingChecker{},
		"eid-1",
		"tid-1",
	)
	ok, missing := v.CanDone(context.Background(), json.RawMessage(`{}`))
	if ok {
		t.Fatal("缺 reason 应拒绝")
	}
	if !slices.Contains(missing, "reason") {
		t.Fatalf("missing 应含 reason: %v", missing)
	}
}

// TestBACValidator_RejectInvalidReason —— reason 不在合法集。
func TestBACValidator_RejectInvalidReason(t *testing.T) {
	v := NewBACValidator(
		&fakeFactReader{state: stateWithEvidence([]string{"e1"}, []string{"b1"})},
		&fakeFindingChecker{},
		"eid-1",
		"tid-1",
	)
	ok, missing := v.CanDone(context.Background(), json.RawMessage(`{"reason":"garbage"}`))
	if ok {
		t.Fatal("非法 reason 应拒绝")
	}
	if !slices.Contains(missing, "valid_reason") {
		t.Fatalf("missing 应含 valid_reason: %v", missing)
	}
}

// TestBACValidator_RejectEmptyState —— state 没 facts/evidence 也没 boundaries。
func TestBACValidator_RejectEmptyState(t *testing.T) {
	v := NewBACValidator(
		&fakeFactReader{state: emptyState()},
		&fakeFindingChecker{},
		"eid-1",
		"tid-1",
	)
	ok, missing := v.CanDone(context.Background(), json.RawMessage(`{"reason":"no_pattern_match"}`))
	if ok {
		t.Fatal("state 空应拒绝")
	}
	if !slices.Contains(missing, "evidence_or_boundary") {
		t.Fatalf("missing 应含 evidence_or_boundary: %v", missing)
	}
}

// TestBACValidator_RejectLegacyAllDiffer —— agentic 简化后 all_differ 已下线，应拒绝。
func TestBACValidator_RejectLegacyAllDiffer(t *testing.T) {
	v := NewBACValidator(
		&fakeFactReader{state: stateWithEvidence(
			[]string{"3 identities tested, all 200"},
			[]string{"similarity 0.95 across identities"},
		)},
		&fakeFindingChecker{},
		"eid-1",
		"tid-1",
	)
	ok, missing := v.CanDone(context.Background(), json.RawMessage(`{"reason":"all_differ"}`))
	if ok {
		t.Fatal("legacy all_differ 已下线应拒绝")
	}
	if !slices.Contains(missing, "valid_reason") {
		t.Fatalf("missing 应含 valid_reason: %v", missing)
	}
}

// TestBACValidator_RejectLegacyHeuristicSkip —— agentic 简化后 heuristic_skip 已下线，应拒绝。
func TestBACValidator_RejectLegacyHeuristicSkip(t *testing.T) {
	v := NewBACValidator(
		&fakeFactReader{state: stateWithEvidence(
			[]string{"static asset path /static/x.png"},
			nil,
		)},
		&fakeFindingChecker{},
		"eid-1",
		"tid-1",
	)
	ok, missing := v.CanDone(context.Background(), json.RawMessage(`{"reason":"heuristic_skip"}`))
	if ok {
		t.Fatal("legacy heuristic_skip 已下线应拒绝")
	}
	if !slices.Contains(missing, "valid_reason") {
		t.Fatalf("missing 应含 valid_reason: %v", missing)
	}
}

// TestBACValidator_FindingWrittenButMissing —— reason=finding_written 但 finding 表查不到。
func TestBACValidator_FindingWrittenButMissing(t *testing.T) {
	checker := &fakeFindingChecker{exists: false}
	v := NewBACValidator(
		&fakeFactReader{state: stateWithEvidence([]string{"e1"}, []string{"b1"})},
		checker,
		"eid-x",
		"tid-x",
	)
	ok, missing := v.CanDone(context.Background(),
		json.RawMessage(`{"reason":"finding_written","dedup_key":"bac.h:host:GET:/api/o/:id"}`))
	if ok {
		t.Fatal("finding 缺失应拒绝")
	}
	if !slices.Contains(missing, "finding") {
		t.Fatalf("missing 应含 finding: %v", missing)
	}
	if checker.gotKey != "bac.h:host:GET:/api/o/:id" {
		t.Fatalf("应把 dedup_key 透传给 checker: %q", checker.gotKey)
	}
	if checker.gotEID != "eid-x" {
		t.Fatalf("应把 eid 透传给 checker: %q", checker.gotEID)
	}
}

// TestBACValidator_FindingWrittenWithoutDedupKey —— reason=finding_written 但 args 无 dedup_key。
func TestBACValidator_FindingWrittenWithoutDedupKey(t *testing.T) {
	v := NewBACValidator(
		&fakeFactReader{state: stateWithEvidence([]string{"e1"}, []string{"b1"})},
		&fakeFindingChecker{exists: true},
		"eid-1",
		"tid-1",
	)
	ok, missing := v.CanDone(context.Background(),
		json.RawMessage(`{"reason":"finding_written"}`))
	if ok {
		t.Fatal("无 dedup_key 应拒绝")
	}
	if !slices.Contains(missing, "dedup_key") {
		t.Fatalf("missing 应含 dedup_key: %v", missing)
	}
}

// TestBACValidator_FindingWrittenOK —— reason=finding_written 且 finding 表存在。
func TestBACValidator_FindingWrittenOK(t *testing.T) {
	v := NewBACValidator(
		&fakeFactReader{state: stateWithEvidence([]string{"e1"}, []string{"b1"})},
		&fakeFindingChecker{exists: true},
		"eid-1",
		"tid-1",
	)
	ok, missing := v.CanDone(context.Background(),
		json.RawMessage(`{"reason":"finding_written","dedup_key":"bac.h:host:GET:/x"}`))
	if !ok {
		t.Fatalf("应放行，missing=%v", missing)
	}
}

// TestBACValidator_StateReadError —— ReadState 报错时整体失败。
func TestBACValidator_StateReadError(t *testing.T) {
	v := NewBACValidator(
		&fakeFactReader{err: errors.New("db down")},
		&fakeFindingChecker{},
		"eid-1",
		"tid-1",
	)
	ok, missing := v.CanDone(context.Background(),
		json.RawMessage(`{"reason":"no_pattern_match"}`))
	if ok {
		t.Fatal("ReadState 报错应拒绝")
	}
	if !slices.Contains(missing, "state_read_error") {
		t.Fatalf("missing 应含 state_read_error: %v", missing)
	}
}

// TestBACValidator_FindingCheckerError —— HasDedupKey 报错时整体失败。
func TestBACValidator_FindingCheckerError(t *testing.T) {
	v := NewBACValidator(
		&fakeFactReader{state: stateWithEvidence([]string{"e1"}, nil)},
		&fakeFindingChecker{err: errors.New("db down")},
		"eid-1",
		"tid-1",
	)
	ok, missing := v.CanDone(context.Background(),
		json.RawMessage(`{"reason":"finding_written","dedup_key":"k"}`))
	if ok {
		t.Fatal("HasDedupKey 报错应拒绝")
	}
	if !slices.Contains(missing, "finding_check_error") {
		t.Fatalf("missing 应含 finding_check_error: %v", missing)
	}
}

// TestBACValidator_AcceptNoPatternMatch —— no_pattern_match 是合法 reason。
func TestBACValidator_AcceptNoPatternMatch(t *testing.T) {
	v := NewBACValidator(
		&fakeFactReader{state: stateWithEvidence([]string{"e1"}, nil)},
		&fakeFindingChecker{},
		"eid-1",
		"tid-1",
	)
	ok, missing := v.CanDone(context.Background(),
		json.RawMessage(`{"reason":"no_pattern_match"}`))
	if !ok {
		t.Fatalf("no_pattern_match 应放行，missing=%v", missing)
	}
}

// TestBACValidator_RejectMalformedArgs —— args 非合法 JSON。
func TestBACValidator_RejectMalformedArgs(t *testing.T) {
	v := NewBACValidator(
		&fakeFactReader{state: stateWithEvidence([]string{"e1"}, nil)},
		&fakeFindingChecker{},
		"eid-1",
		"tid-1",
	)
	ok, missing := v.CanDone(context.Background(), json.RawMessage(`not json`))
	if ok {
		t.Fatal("非法 args 应拒绝")
	}
	if !slices.Contains(missing, "reason") {
		t.Fatalf("missing 应含 reason（解析失败按缺 reason 处理）: %v", missing)
	}
}
