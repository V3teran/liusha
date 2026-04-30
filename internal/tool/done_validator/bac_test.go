package done_validator

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

// fakeFactReader 用 ReadState 返回固定的 State JSON（或注入 err）。
type fakeFactReader struct {
	state []byte
	err   error
}

func (f *fakeFactReader) ReadState(_ context.Context, _ string) ([]byte, error) {
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

// stateWithEvidence 构造一个有 evidence + boundary 的 memory_facts JSON。
func stateWithEvidence(evidence, boundary []string) []byte {
	type fact struct {
		Category string `json:"category"`
		Content  string `json:"content"`
	}
	facts := map[string]any{}
	if len(evidence) > 0 {
		evs := make([]fact, 0, len(evidence))
		for _, c := range evidence {
			evs = append(evs, fact{Category: "evidence", Content: c})
		}
		facts["evidence"] = evs
	}
	if len(boundary) > 0 {
		bds := make([]fact, 0, len(boundary))
		for _, c := range boundary {
			bds = append(bds, fact{Category: "boundary", Content: c})
		}
		facts["boundaries"] = bds
	}
	factsRaw, _ := json.Marshal(facts)
	state := map[string]json.RawMessage{
		"facts": factsRaw,
		"ideas": json.RawMessage(`{}`),
		"hints": json.RawMessage(`{}`),
	}
	out, _ := json.Marshal(state)
	return out
}

// emptyState 构造一个 facts/ideas/hints 全空的 State。
func emptyState() []byte {
	state := map[string]json.RawMessage{
		"facts": json.RawMessage(`{}`),
		"ideas": json.RawMessage(`{}`),
		"hints": json.RawMessage(`{}`),
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
	)
	ok, missing := v.CanDone(context.Background(), json.RawMessage(`{"reason":"all_similar"}`))
	if ok {
		t.Fatal("state 空应拒绝")
	}
	if !slices.Contains(missing, "evidence_or_boundary") {
		t.Fatalf("missing 应含 evidence_or_boundary: %v", missing)
	}
}

// TestBACValidator_AcceptAllSimilar —— state 含 evidence + boundary，reason=all_similar。
func TestBACValidator_AcceptAllSimilar(t *testing.T) {
	v := NewBACValidator(
		&fakeFactReader{state: stateWithEvidence(
			[]string{"replay 3 identities, all 200"},
			[]string{"similarity 0.95 across identities"},
		)},
		&fakeFindingChecker{},
		"eid-1",
	)
	ok, missing := v.CanDone(context.Background(), json.RawMessage(`{"reason":"all_similar"}`))
	if !ok {
		t.Fatalf("应放行，但拒绝 missing=%v", missing)
	}
	if len(missing) != 0 {
		t.Fatalf("missing 应空: %v", missing)
	}
}

// TestBACValidator_AcceptHeuristicSkip —— heuristic_skip 也是合法 reason。
func TestBACValidator_AcceptHeuristicSkip(t *testing.T) {
	v := NewBACValidator(
		&fakeFactReader{state: stateWithEvidence(
			[]string{"static asset path /static/x.png"},
			nil,
		)},
		&fakeFindingChecker{},
		"eid-1",
	)
	ok, _ := v.CanDone(context.Background(), json.RawMessage(`{"reason":"heuristic_skip"}`))
	if !ok {
		t.Fatal("heuristic_skip 应放行")
	}
}

// TestBACValidator_FindingWrittenButMissing —— reason=finding_written 但 finding 表查不到。
func TestBACValidator_FindingWrittenButMissing(t *testing.T) {
	checker := &fakeFindingChecker{exists: false}
	v := NewBACValidator(
		&fakeFactReader{state: stateWithEvidence([]string{"e1"}, []string{"b1"})},
		checker,
		"eid-x",
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
	)
	ok, missing := v.CanDone(context.Background(),
		json.RawMessage(`{"reason":"all_similar"}`))
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
	)
	ok, missing := v.CanDone(context.Background(), json.RawMessage(`not json`))
	if ok {
		t.Fatal("非法 args 应拒绝")
	}
	if !slices.Contains(missing, "reason") {
		t.Fatalf("missing 应含 reason（解析失败按缺 reason 处理）: %v", missing)
	}
}
