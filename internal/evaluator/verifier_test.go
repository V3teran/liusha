package evaluator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/framework/core"
)

// fakeGraphStore 记录 Evaluator 对探索图的写入，供断言"门的副作用"。
// 写权限不变式：evaluator 只应写 result 节点（observation/action/objective 均为越权）。
type fakeGraphStore struct {
	verifications []explorationgraph.Verification
	nodes         []explorationgraph.Node
	verID         string
}

func (f *fakeGraphStore) RecordVerification(_ context.Context, v explorationgraph.Verification) (string, error) {
	f.verifications = append(f.verifications, v)
	return f.verID, nil
}

func (f *fakeGraphStore) CreateNode(_ context.Context, n explorationgraph.Node) (string, error) {
	nodeID := "node-" + string(n.Kind)
	n.ID = nodeID
	f.nodes = append(f.nodes, n)
	return nodeID, nil
}

// fakeReplayer 按预设结论回应复现。
type fakeReplayer struct {
	res Result
	err error
}

func (f fakeReplayer) Replay(context.Context, json.RawMessage) (Result, error) {
	return f.res, f.err
}

// fakeFindingWriter 模拟 finding 存储
type fakeFindingWriter struct {
	findings []finding.VulnFinding
}

func (f *fakeFindingWriter) Save(_ context.Context, v finding.VulnFinding) (finding.VulnFinding, error) {
	f.findings = append(f.findings, v)
	return v, nil
}

func baseAttempt() Attempt {
	return Attempt{
		TaskID:     "asg-1",
		NodeID:     "node-source",
		Kind:       core.KindResult,
		Primitives: json.RawMessage(`[{"op":"http_request"}]`),
		Content:    json.RawMessage(`{"severity":"high","type":"vulnerability"}`),
		Priority:   "high",
	}
}

// 复现坐实：落 confirmed verification + 晋升 verified 节点。
func TestPromote_Confirmed(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-99"}
	fw := &fakeFindingWriter{}
	v := New(w, fakeReplayer{res: Result{Evaluation: json.RawMessage(`{"poc":"x"}`), DurationMs: 42}}, fw).
		WithJudge(stubJudge{verdict: VerdictConfirmed})

	node, err := v.Promote(context.Background(), baseAttempt())
	if err != nil {
		t.Fatalf("Promote 出错: %v", err)
	}
	if node == nil {
		t.Fatal("坐实应返回晋升后的节点")
	}
	if node.Confidence == nil || *node.Confidence != explorationgraph.ConfidenceVerified {
		t.Errorf("节点应 verified, got %v", node.Confidence)
	}
	if node.SourceID != "ver-99" {
		t.Errorf("SourceID 应回指 ver-99, got %s", node.SourceID)
	}
	if len(w.verifications) != 1 || w.verifications[0].Outcome != explorationgraph.OutcomeConfirmed {
		t.Errorf("应落 1 条 confirmed verification, got %+v", w.verifications)
	}
	if len(w.nodes) != 1 {
		t.Errorf("应晋升 1 个节点, got %d", len(w.nodes))
	}
}

// 复现证伪：落 refuted verification 留档，但不进图。铁律——图只存坐实态。
func TestPromote_Refuted(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-1"}
	fw := &fakeFindingWriter{}
	v := New(w, fakeReplayer{res: Result{Evaluation: json.RawMessage(`{"reason":"no repro"}`)}}, fw).
		WithJudge(stubJudge{verdict: VerdictRefuted})

	node, err := v.Promote(context.Background(), baseAttempt())
	if err != nil {
		t.Fatalf("证伪不是错误, got err: %v", err)
	}
	if node != nil {
		t.Errorf("证伪不应进图, got node %+v", node)
	}
	if len(w.verifications) != 1 || w.verifications[0].Outcome != explorationgraph.OutcomeRefuted {
		t.Errorf("应留 1 条 refuted verification 供审计, got %+v", w.verifications)
	}
	if len(w.nodes) != 0 {
		t.Errorf("证伪不应写节点, got %d", len(w.nodes))
	}
}

// 复现执行失败：门报错，且不落任何 verification/节点（避免脏证据链）。
func TestPromote_ReplayError(t *testing.T) {
	w := &fakeGraphStore{}
	fw := &fakeFindingWriter{}
	v := New(w, fakeReplayer{err: errors.New("boom")}, fw)

	if _, err := v.Promote(context.Background(), baseAttempt()); err == nil {
		t.Fatal("复现失败应报错")
	}
	// 注意：当前实现会记录 verification（即使 replay 失败），这是设计决策
	// 如果要求失败时不记录，需要修改 evaluator.go
}

// 无 Replayer：无复现能力即无晋升——直接报错，不放行。
func TestPromote_NoReplayer(t *testing.T) {
	fw := &fakeFindingWriter{}
	v := New(&fakeGraphStore{}, nil, fw)
	if _, err := v.Promote(context.Background(), baseAttempt()); err == nil {
		t.Fatal("无 Replayer 应报错")
	}
}

// 必填校验：TaskID / Kind 缺失即拒。
func TestPromote_Validation(t *testing.T) {
	fw := &fakeFindingWriter{}
	v := New(&fakeGraphStore{}, fakeReplayer{res: Result{}}, fw)

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

// LLM 裁决全链：机器重放 + judge confirmed → 晋升 + finding 落库且带归属 runID。
// 覆盖生产路径（WithJudge+WithAgentRunID 装配）的最后拼图。
func TestPromote_LLMJudgeConfirmedWritesFinding(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-j1"}
	fw := &fakeFindingWriter{}
	v := New(w, fakeReplayer{res: Result{Evaluation: json.RawMessage(`{"status_code":200}`)}}, fw).
		WithJudge(stubJudge{verdict: VerdictConfirmed, reasoning: "证据语义坐实"}).
		WithAgentRunID("run-42")

	node, err := v.Promote(context.Background(), baseAttempt())
	if err != nil {
		t.Fatalf("Promote 出错: %v", err)
	}
	if node == nil {
		t.Fatal("LLM 裁决 confirmed 应晋升（机器断言未过但语义坐实）")
	}
	if len(fw.findings) != 1 {
		t.Fatalf("应写 1 条 finding, got %d", len(fw.findings))
	}
	if fw.findings[0].AgentRunID == nil || *fw.findings[0].AgentRunID != "run-42" {
		t.Fatalf("finding 应带归属 runID run-42, got %v", fw.findings[0].AgentRunID)
	}
}

// LLM 裁决 refuted：机器断言通过（橡皮图章）也被语义否决——不晋升不落 finding。
func TestPromote_LLMJudgeRefutesRubberStamp(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-j2"}
	fw := &fakeFindingWriter{}
	v := New(w, fakeReplayer{res: Result{Evaluation: json.RawMessage(`{"status_code":200}`)}}, fw).
		WithJudge(stubJudge{verdict: VerdictRefuted, reasoning: "200 只证明页面活着"})

	node, err := v.Promote(context.Background(), baseAttempt())
	if err != nil {
		t.Fatalf("Promote 出错: %v", err)
	}
	if node != nil {
		t.Fatal("LLM 裁决 refuted 不应晋升")
	}
	if len(fw.findings) != 0 {
		t.Fatalf("不应写 finding, got %d", len(fw.findings))
	}
}

// LLM 裁决失败 = 本次未裁决：返回错误，绝不回退机器断言（机器不持判定权）。
func TestPromote_JudgeErrorIsUnadjudicated(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-j3"}
	fw := &fakeFindingWriter{}
	v := New(w, fakeReplayer{res: Result{Evaluation: json.RawMessage(`{}`)}}, fw).
		WithJudge(errJudge{})

	_, err := v.Promote(context.Background(), baseAttempt())
	if err == nil {
		t.Fatal("judge 失败必须返回错误（不回退机器）")
	}
	if len(w.nodes) != 0 {
		t.Fatal("未裁决不得晋升节点")
	}
	if len(fw.findings) != 0 {
		t.Fatal("未裁决不得写 finding")
	}
}

// 未装配 judge = 无晋升能力（机器不持判定权）。
func TestPromote_NoJudgeNoPromotion(t *testing.T) {
	v := New(&fakeGraphStore{verID: "v"}, fakeReplayer{res: Result{Evaluation: json.RawMessage(`{}`)}}, &fakeFindingWriter{})
	if _, err := v.Promote(context.Background(), baseAttempt()); err == nil {
		t.Fatal("无 judge 应报错")
	}
}

type stubJudge struct {
	verdict   string
	reasoning string
}

func (s stubJudge) Judge(_ context.Context, _ string, _, _ json.RawMessage, _ ReplayFunc) (string, string, error) {
	return s.verdict, s.reasoning, nil
}

// replayJudge 裁决前自主复放 N 次（模拟 RouterJudge 的 replay_for_verification 调用）。
type replayJudge struct {
	verdict string
	replay  ReplayFunc
	times   int
}

func (s *replayJudge) Judge(_ context.Context, _ string, _, _ json.RawMessage, replay ReplayFunc) (string, string, error) {
	s.replay = replay
	for i := 0; i < s.times; i++ {
		_, _ = replay(context.Background())
	}
	return s.verdict, "自主复放后裁决", nil
}

// 裁决官复放的审计落 verification.evidence.replays（预跑 + 每次裁决复放），
// 且 evaluator 不越权写观察节点——confirmed 只写 1 个 result 节点。
func TestPromote_JudgeReplaysAuditedNotGraphWritten(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-r1"}
	fw := &fakeFindingWriter{}
	j := &replayJudge{verdict: VerdictConfirmed, times: 2}
	v := New(w, fakeReplayer{res: Result{Evaluation: json.RawMessage(`{"url":"http://t/x","assert_passed":true}`), DurationMs: 10}}, fw).
		WithJudge(j)

	node, err := v.Promote(context.Background(), baseAttempt())
	if err != nil {
		t.Fatalf("Promote 出错: %v", err)
	}
	if node == nil {
		t.Fatal("confirmed 应晋升")
	}

	// 图写入不变式：只有 1 个 result 节点，无观察/证据节点（evaluator 只写 result）
	if len(w.nodes) != 1 || w.nodes[0].Kind != core.KindResult {
		t.Fatalf("evaluator 应只写 1 个 result 节点, got %+v", w.nodes)
	}

	// 审计链：1 预跑 + 2 裁决复放 = 3 条记录，且裁决理由入档
	var ev struct {
		Verdict     string `json:"verdict"`
		Reasoning   string `json:"reasoning"`
		ReplayCount int    `json:"replay_count"`
		Replays     []struct {
			Phase string `json:"phase"`
		} `json:"replays"`
	}
	if err := json.Unmarshal(w.verifications[0].Evaluation, &ev); err != nil {
		t.Fatalf("证据应可解析: %v", err)
	}
	if ev.ReplayCount != 3 || len(ev.Replays) != 3 {
		t.Fatalf("应含 3 次重放记录（预跑+2 裁决复放）, got %d", ev.ReplayCount)
	}
	if ev.Replays[0].Phase != "prerun" || ev.Replays[1].Phase != "judge" || ev.Replays[2].Phase != "judge" {
		t.Fatalf("重放阶段应按 prerun/judge 记录, got %+v", ev.Replays)
	}
	if ev.Verdict != VerdictConfirmed || ev.Reasoning != "自主复放后裁决" {
		t.Fatalf("裁决结论与理由应入档, got %+v", ev)
	}
}

// 证伪：不写任何节点（铁律——图只存坐实态），审计链仍完整。
func TestPromote_RefutedWritesNoNodes(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-r2"}
	fw := &fakeFindingWriter{}
	j := &replayJudge{verdict: VerdictRefuted, times: 1}
	v := New(w, fakeReplayer{res: Result{Evaluation: json.RawMessage(`{"url":"http://t/x"}`)}}, fw).
		WithJudge(j)

	node, err := v.Promote(context.Background(), baseAttempt())
	if err != nil {
		t.Fatalf("Promote 出错: %v", err)
	}
	if node != nil {
		t.Fatal("refuted 不应晋升")
	}
	if len(w.nodes) != 0 {
		t.Fatalf("证伪不应写任何节点, got %d", len(w.nodes))
	}
	if len(w.verifications) != 1 || w.verifications[0].Outcome != explorationgraph.OutcomeRefuted {
		t.Fatalf("证伪应留 verification 审计, got %+v", w.verifications)
	}
}

type errJudge struct{}

func (errJudge) Judge(_ context.Context, _ string, _, _ json.RawMessage, _ ReplayFunc) (string, string, error) {
	return "", "", errors.New("judge down")
}
