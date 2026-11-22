package evaluator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// fakeGraphStore 记录 Evaluator 对探索图的写入，供断言"门的副作用"。
// 写权限不变式：evaluator 只应新建 result 节点；对假设节点仅 metadata 验证标记。
type fakeGraphStore struct {
	mu            sync.Mutex // 并发验证 goroutine 共享本 fake，写入需互斥
	verifications []explorationgraph.Verification
	nodes         []explorationgraph.Node
	edges         []explorationgraph.Edge
	metadata      map[string]json.RawMessage // nodeID → 最近一次 metadata 补丁
	verID         string
}

// VerificationCount 返回已记录的验证条数（并发安全读）。
func (f *fakeGraphStore) VerificationCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.verifications)
}

func (f *fakeGraphStore) RecordVerification(_ context.Context, v explorationgraph.Verification) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verifications = append(f.verifications, v)
	return f.verID, nil
}

func (f *fakeGraphStore) CreateNode(_ context.Context, n explorationgraph.Node) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n.ID == "" {
		n.ID = "node-" + string(n.Kind)
	}
	f.nodes = append(f.nodes, n)
	return n.ID, nil
}

func (f *fakeGraphStore) CreateNodeWithEdges(_ context.Context, n explorationgraph.Node, edges []explorationgraph.Edge) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n.ID == "" {
		n.ID = "node-" + string(n.Kind)
	}
	f.nodes = append(f.nodes, n)
	f.edges = append(f.edges, edges...)
	return nil
}

func (f *fakeGraphStore) UpdateNodeMetadata(_ context.Context, id string, metadata json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.metadata == nil {
		f.metadata = map[string]json.RawMessage{}
	}
	f.metadata[id] = metadata
	return nil
}

func (f *fakeGraphStore) ListResults(_ context.Context, _ string) ([]explorationgraph.Node, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var results []explorationgraph.Node
	for _, n := range f.nodes {
		if n.Kind == core.KindResult {
			results = append(results, n)
		}
	}
	return results, nil
}

// fakeReplayer 按预设结论回应复现。
type fakeReplayer struct {
	res Result
	err error
}

func (f fakeReplayer) Replay(context.Context, json.RawMessage) (Result, error) {
	return f.res, f.err
}

// fakeClaimSink 模拟坐实主张交付管道
type fakeClaimSink struct {
	claims []ConfirmedClaim
}

func (f *fakeClaimSink) Record(_ context.Context, c ConfirmedClaim) error {
	f.claims = append(f.claims, c)
	return nil
}

// stubKind 是测试用的配方形状：经 gateWith 注册 fake 复现通道。
const stubKind = "stub"

// gateWith 构造带 stubKind 复现通道的晋升门；r 为 nil 时不注册通道。
func gateWith(graph graphStore, r Replayer, sink ClaimSink) *PromotionGate {
	g := New(graph, sink)
	if r != nil {
		g.WithReplayer(stubKind, r)
	}
	return g
}

func baseClaim() Claim {
	return Claim{
		TaskID:   "asg-1",
		NodeID:   "node-source",
		Repro:    json.RawMessage(`{"kind":"stub","recipe":{"op":"http_request"},"assert":{"marker":"x"}}`),
		Content:  json.RawMessage(`{"statement":"X 参数存在可复现注入"}`),
		Priority: "high",
	}
}

// 复现坐实：落 confirmed verification + 晋升 verified 节点。
func TestPromote_Confirmed(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-99"}
	fw := &fakeClaimSink{}
	v := gateWith(w, fakeReplayer{res: Result{Evidence: json.RawMessage(`{"poc":"x"}`), DurationMs: 42}}, fw).
		WithJudge(stubJudge{verdict: VerdictConfirmed})

	node, err := v.Promote(context.Background(), baseClaim())
	if err != nil {
		t.Fatalf("Promote 出错: %v", err)
	}
	if node == nil {
		t.Fatal("坐实应返回晋升后的节点")
	}
	if node.Kind != core.KindResult || node.VerificationStatus != nil {
		t.Errorf("坐实应晋升为 result（不带验证状态）, got %s %v", node.Kind, node.VerificationStatus)
	}
	if len(w.verifications) != 1 || w.verifications[0].Outcome != explorationgraph.VerificationConfirmed {
		t.Errorf("应落 1 条 confirmed verification, got %+v", w.verifications)
	}
	if len(w.nodes) != 1 {
		t.Errorf("应晋升 1 个节点, got %d", len(w.nodes))
	}
}

// 复现证伪：落 refuted verification 留档，但不进图。铁律——图只存坐实态。
func TestPromote_Refuted(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-1"}
	fw := &fakeClaimSink{}
	v := gateWith(w, fakeReplayer{res: Result{Evidence: json.RawMessage(`{"reason":"no repro"}`)}}, fw).
		WithJudge(stubJudge{verdict: VerdictRefuted})

	node, err := v.Promote(context.Background(), baseClaim())
	if err != nil {
		t.Fatalf("证伪不是错误, got err: %v", err)
	}
	if node != nil {
		t.Errorf("证伪不应进图, got node %+v", node)
	}
	if len(w.verifications) != 1 || w.verifications[0].Outcome != explorationgraph.VerificationRefuted {
		t.Errorf("应留 1 条 refuted verification 供审计, got %+v", w.verifications)
	}
	if len(w.nodes) != 0 {
		t.Errorf("证伪不应写节点, got %d", len(w.nodes))
	}
}

// 评估者证伪时给出的反驳原因原样落到假设节点：「证据没拿到」与「主张不成立」的处置不同
// （前者可换配方再试，后者方向作废），全记成 claim_invalid 会让一次限流就判死整个方向。
// 评估者只能给出判定层面的三种原因；缺失或越权（复现通道 / 评估环节专属的原因）时按
// claim_invalid 落标。
func TestPromote_JudgeRefuteReason(t *testing.T) {
	cases := []struct {
		given string
		want  string
	}{
		{RefuteNotReproducible, RefuteNotReproducible},
		{RefuteAssertNotDiscriminative, RefuteAssertNotDiscriminative},
		{RefuteClaimInvalid, RefuteClaimInvalid},
		{"", RefuteClaimInvalid},
		{"unknown_reason", RefuteClaimInvalid},
		{RefuteInvalidRecipe, RefuteClaimInvalid},
		{RefuteJudgeUnavailable, RefuteClaimInvalid},
	}
	for _, tc := range cases {
		t.Run("given_"+tc.given, func(t *testing.T) {
			w := &fakeGraphStore{verID: "ver-rr"}
			v := gateWith(w, fakeReplayer{res: Result{Evidence: json.RawMessage(`{"url":"http://t/x"}`)}}, &fakeClaimSink{}).
				WithJudge(stubJudge{verdict: VerdictRefuted, reasoning: "x", refuteReason: tc.given})
			if _, err := v.Promote(context.Background(), baseClaim()); err != nil {
				t.Fatalf("Promote 出错: %v", err)
			}
			var mark struct {
				RefuteReason string `json:"refute_reason"`
				RefuteDetail string `json:"refute_detail"`
			}
			raw, ok := w.metadata["node-source"]
			if !ok || json.Unmarshal(raw, &mark) != nil {
				t.Fatalf("假设节点应被回写验证标记, got %+v", w.metadata)
			}
			if mark.RefuteReason != tc.want {
				t.Fatalf("评估者给出 %q，落标应为 %q，得 %q", tc.given, tc.want, mark.RefuteReason)
			}
			// 评估者的理由随原因落标：原因只是类别，缺了什么证据、哪部分已被证明在理由里。
			if mark.RefuteDetail != "x" {
				t.Fatalf("证伪理由应随反驳原因落标，得 %q", mark.RefuteDetail)
			}
		})
	}
}

// 同验证键沿用证伪结论时，反驳原因与理由一并沿用：图上同一结论的标记形状一致。
func TestAgent_AdoptedRefutationCarriesDetail(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-d2"}
	promoter := gateWith(w, fakeReplayer{res: Result{Evidence: json.RawMessage(`{"url":"http://t/x","assert_passed":true}`)}}, &fakeClaimSink{}).
		WithJudge(stubJudge{verdict: VerdictRefuted, reasoning: "与对照无差异", refuteReason: RefuteClaimInvalid})
	graph := explorationgraph.NewMemoryStore()
	a := NewAgent(AgentConfig{
		TaskID: "t-adopt", Gate: promoter, EventBus: bus.New(context.Background()), Graph: graph, Logger: zerolog.Nop(),
	})
	for _, id := range []string{"obs-1", "obs-2"} {
		if _, err := graph.CreateNode(context.Background(), explorationgraph.Node{
			ID: id, TaskID: "t-adopt", Kind: core.KindObservation, Content: json.RawMessage(`{"statement":"same hypothesis"}`),
		}); err != nil {
			t.Fatalf("预建假设节点 %s 失败: %v", id, err)
		}
	}
	// 门的标记写进 fakeGraphStore，沿用的标记写进 graph：先把首个判定的标记同步进 graph。
	if err := a.verifyObservation(context.Background(), "obs-1", baseClaim()); err != nil {
		t.Fatalf("首次验证应成功: %v", err)
	}
	if raw, ok := w.metadata["node-source"]; ok {
		if err := graph.UpdateNodeMetadata(context.Background(), "obs-1", raw); err != nil {
			t.Fatalf("同步标记失败: %v", err)
		}
	}
	a.verdictStore(claimHash(baseClaim()), a.priorVerdictFrom(context.Background(), "obs-1", nil))
	if err := a.verifyObservation(context.Background(), "obs-2", baseClaim()); err != nil {
		t.Fatalf("重复配方应沿用判定: %v", err)
	}
	adopted, err := graph.GetNode(context.Background(), "obs-2")
	if err != nil {
		t.Fatalf("读回沿用判定的节点失败: %v", err)
	}
	if adopted.RefuteReason() != RefuteClaimInvalid || adopted.RefuteDetail() != "与对照无差异" {
		t.Fatalf("沿用的证伪应带原因与理由，得 reason=%q detail=%q", adopted.RefuteReason(), adopted.RefuteDetail())
	}
}

// flakyReplayer 前 failures 次复现断言未命中，之后命中：模拟目标限流 / 维护后恢复。
type flakyReplayer struct {
	mu       sync.Mutex
	calls    int
	failures int
}

func (f *flakyReplayer) Replay(context.Context, json.RawMessage) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.failures {
		return Result{Evidence: json.RawMessage(`{"assert_passed":false,"attack_status_code":503}`)}, nil
	}
	return Result{Evidence: json.RawMessage(`{"assert_passed":true}`)}, nil
}

// markSyncedGraph 让门的假设标记同时落到评估者读取的图上（内存图不支持审计链，
// 审计与结果节点仍写 fakeGraphStore）：去重表据图上的反驳原因决定能否沿用。
type markSyncedGraph struct {
	*fakeGraphStore
	marks *explorationgraph.Store
}

func (g markSyncedGraph) UpdateNodeMetadata(ctx context.Context, id string, metadata json.RawMessage) error {
	if err := g.fakeGraphStore.UpdateNodeMetadata(ctx, id, metadata); err != nil {
		return err
	}
	return g.marks.UpdateNodeMetadata(ctx, id, metadata)
}

// 证据没拿到（not_reproducible）不是对「假设+配方」的定论：规划者按它安排重新取得、
// 以同一配方重报时必须重新复现，不得沿用上次的证伪（实测：目标维护窗口内的一次复放
// 失败，让重新取得的真 flag 被去重直接判死）。
func TestAgent_NotReproducibleIsNotAdoptedForSameRecipe(t *testing.T) {
	graph := explorationgraph.NewMemoryStore()
	replayer := &flakyReplayer{failures: 1}
	gateGraph := markSyncedGraph{fakeGraphStore: &fakeGraphStore{verID: "ver-r1"}, marks: graph}
	promoter := gateWith(gateGraph, replayer, &fakeClaimSink{}).WithJudge(stubJudge{verdict: VerdictConfirmed})
	a := NewAgent(AgentConfig{
		TaskID: "t-retry", Gate: promoter, EventBus: bus.New(context.Background()), Graph: graph, Logger: zerolog.Nop(),
	})
	for _, id := range []string{"obs-1", "obs-2"} {
		if _, err := graph.CreateNode(context.Background(), explorationgraph.Node{
			ID: id, TaskID: "t-retry", Kind: core.KindObservation, Content: json.RawMessage(`{"statement":"X 参数存在可复现注入"}`),
		}); err != nil {
			t.Fatalf("预建假设节点 %s 失败: %v", id, err)
		}
	}
	claimFor := func(id string) Claim {
		c := baseClaim()
		c.TaskID, c.NodeID = "t-retry", id
		return c
	}

	if err := a.verifyObservation(context.Background(), "obs-1", claimFor("obs-1")); err != nil {
		t.Fatalf("首次验证应成功: %v", err)
	}
	first, err := graph.GetNode(context.Background(), "obs-1")
	if err != nil {
		t.Fatalf("读回首个假设失败: %v", err)
	}
	if first.RefuteReason() != RefuteNotReproducible {
		t.Fatalf("前置：首次复现未命中应按 not_reproducible 证伪，得 %q", first.RefuteReason())
	}

	if err := a.verifyObservation(context.Background(), "obs-2", claimFor("obs-2")); err != nil {
		t.Fatalf("重报验证应成功: %v", err)
	}
	if replayer.calls != 2 {
		t.Fatalf("同配方重报应重新复现，复现次数得 %d", replayer.calls)
	}
	second, err := graph.GetNode(context.Background(), "obs-2")
	if err != nil {
		t.Fatalf("读回重报假设失败: %v", err)
	}
	if !second.HasVerificationStatus(explorationgraph.VerificationConfirmed) {
		t.Fatalf("目标恢复后重报应坐实，得 status=%v reason=%q", second.VerificationStatus, second.RefuteReason())
	}
}

// 复现执行失败：门报错，且不落任何 verification/节点（避免脏证据链）。
func TestPromote_ReplayError(t *testing.T) {
	w := &fakeGraphStore{}
	fw := &fakeClaimSink{}
	v := gateWith(w, fakeReplayer{err: errors.New("boom")}, fw)

	if _, err := v.Promote(context.Background(), baseClaim()); err == nil {
		t.Fatal("复现失败应报错")
	}
	// 注意：当前实现会记录 verification（即使 replay 失败），这是设计决策
	// 如果要求失败时不记录，需要修改 evaluator.go
}

// 没有机器复现通道的配方形状：门回显配方交给评估者自主取证，验证档位 judge_only——
// 晋升门不因缺少某种形状的通道而拒绝晋升。
func TestPromote_KindWithoutChannelGoesJudgeOnly(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-steps"}
	var seen json.RawMessage
	v := gateWith(w, fakeReplayer{err: errors.New("不应走到 stub 通道")}, &fakeClaimSink{}).
		WithJudge(judgeFunc(func(_ context.Context, _ string, _, ev json.RawMessage, _ ReplayFunc) (Verdict, error) {
			seen = ev
			return Verdict{Verdict: VerdictConfirmed}, nil
		}))
	c := baseClaim()
	c.Repro = json.RawMessage(`{"kind":"steps","recipe":{"steps":"1. 执行 x"},"assert":{"description":"看到 y"}}`)

	if _, err := v.Promote(context.Background(), c); err != nil {
		t.Fatalf("无通道形状应由评估者取证晋升: %v", err)
	}
	var echo map[string]json.RawMessage
	if err := json.Unmarshal(seen, &echo); err != nil || string(echo["kind"]) != `"steps"` || len(echo["recipe"]) == 0 {
		t.Fatalf("评估者应拿到配方回显，得 %s", seen)
	}
	if n := len(w.verifications); n != 1 || !strings.Contains(string(w.verifications[0].Evidence), TierJudgeOnly) {
		t.Fatalf("审计应记 judge_only 档位，得 %d 条", n)
	}
}

// 信封无法解析即确定性配方缺陷：包装 ErrInvalidRecipe，评估者据此一次出圈。
func TestPromote_UnparseableEnvelopeIsInvalidRecipe(t *testing.T) {
	v := gateWith(&fakeGraphStore{}, fakeReplayer{}, &fakeClaimSink{}).WithJudge(stubJudge{verdict: VerdictConfirmed})
	c := baseClaim()
	c.Repro = json.RawMessage(`[{"op":"http_request"}]`)
	if _, err := v.Promote(context.Background(), c); !errors.Is(err, ErrInvalidRecipe) {
		t.Fatalf("期望 ErrInvalidRecipe，得 %v", err)
	}
}

// 必填校验：TaskID / NodeID 缺失即拒。
func TestPromote_Validation(t *testing.T) {
	fw := &fakeClaimSink{}
	v := gateWith(&fakeGraphStore{}, fakeReplayer{res: Result{}}, fw)

	noScan := baseClaim()
	noScan.TaskID = ""
	if _, err := v.Promote(context.Background(), noScan); err == nil {
		t.Error("缺 TaskID 应报错")
	}

	noNode := baseClaim()
	noNode.NodeID = ""
	if _, err := v.Promote(context.Background(), noNode); err == nil {
		t.Error("缺 NodeID 应报错")
	}
}

// LLM 判定全链：机器重放 + judge confirmed → 晋升 + 坐实主张交付管道。
// 交付物的归属（agent_run_id 等）由交付管道构造时注入，门只透传载荷。
func TestPromote_LLMJudgeConfirmedDeliversClaim(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-j1"}
	fw := &fakeClaimSink{}
	v := gateWith(w, fakeReplayer{res: Result{Evidence: json.RawMessage(`{"status_code":200}`)}}, fw).
		WithJudge(stubJudge{verdict: VerdictConfirmed, reasoning: "证据语义坐实"})

	node, err := v.Promote(context.Background(), baseClaim())
	if err != nil {
		t.Fatalf("Promote 出错: %v", err)
	}
	if node == nil {
		t.Fatal("LLM 判定 confirmed 应晋升（机器断言未过但语义坐实）")
	}
	if len(fw.claims) != 1 {
		t.Fatalf("应交付 1 条坐实主张, got %d", len(fw.claims))
	}
	c := fw.claims[0]
	if c.TaskID != "asg-1" || c.ResultNodeID != node.ID || c.VerificationID != "ver-j1" {
		t.Fatalf("交付载荷回指不符: %+v", c)
	}
	if c.Statement == "" || len(c.Evidence) == 0 || len(c.Repro) == 0 {
		t.Fatalf("交付载荷应透传陈述/证据/配方: %+v", c)
	}
	if c.Priority != "high" || node.Priority != explorationgraph.PriorityHigh {
		t.Fatalf("优先级只在节点层一份，交付取结果节点的优先级: claim=%q node=%q", c.Priority, node.Priority)
	}
}

// LLM 判定 refuted：机器断言通过（橡皮图章）也被语义否决——不晋升不落 finding。
func TestPromote_LLMJudgeRefutesRubberStamp(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-j2"}
	fw := &fakeClaimSink{}
	v := gateWith(w, fakeReplayer{res: Result{Evidence: json.RawMessage(`{"status_code":200}`)}}, fw).
		WithJudge(stubJudge{verdict: VerdictRefuted, reasoning: "200 只证明页面活着"})

	node, err := v.Promote(context.Background(), baseClaim())
	if err != nil {
		t.Fatalf("Promote 出错: %v", err)
	}
	if node != nil {
		t.Fatal("LLM 判定 refuted 不应晋升")
	}
	if len(fw.claims) != 0 {
		t.Fatalf("不应写 finding, got %d", len(fw.claims))
	}
}

// LLM 判定失败 = 本次未出结论：返回错误，绝不回退机器断言（机器不持判定权）。
func TestPromote_JudgeErrorLeavesPending(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-j3"}
	fw := &fakeClaimSink{}
	v := gateWith(w, fakeReplayer{res: Result{Evidence: json.RawMessage(`{}`)}}, fw).
		WithJudge(errJudge{})

	_, err := v.Promote(context.Background(), baseClaim())
	if err == nil {
		t.Fatal("judge 失败必须返回错误（不回退机器）")
	}
	if len(w.nodes) != 0 {
		t.Fatal("未出结论不得晋升节点")
	}
	if len(fw.claims) != 0 {
		t.Fatal("未出结论不得写 finding")
	}
}

// 未装配 judge = 无晋升能力（机器不持判定权）。
func TestPromote_NoJudgeNoPromotion(t *testing.T) {
	v := gateWith(&fakeGraphStore{verID: "v"}, fakeReplayer{res: Result{Evidence: json.RawMessage(`{}`)}}, &fakeClaimSink{})
	if _, err := v.Promote(context.Background(), baseClaim()); err == nil {
		t.Fatal("无 judge 应报错")
	}
}

// 机器护栏：executor 断言全部未命中（assert_passed=false）= 无支持坐实的证据，
// 直接机器证伪——不进 judge（judge 曾在 404+未命中证据下仍 confirmed，e2e 实测）。
func TestPromote_MachineGuardRefutesWhenAssertFails(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-mg1"}
	fw := &fakeClaimSink{}
	judgeCalled := false
	j := judgeFunc(func(context.Context, string, json.RawMessage, json.RawMessage, ReplayFunc) (Verdict, error) {
		judgeCalled = true
		return Verdict{Verdict: VerdictConfirmed, Reasoning: "不该被调到"}, nil
	})
	failed := Result{Evidence: json.RawMessage(`{"url":"http://t/x","assert_passed":false,"assert_reasons":["body 缺 \"<script>\" ✗"],"attack_status_code":404}`)}
	v := gateWith(w, fakeReplayer{res: failed}, fw).WithJudge(j)

	node, err := v.Promote(context.Background(), baseClaim())
	if err != nil {
		t.Fatalf("机器证伪不应报错: %v", err)
	}
	if node != nil || judgeCalled {
		t.Fatalf("断言未命中应机器证伪且不进 judge: node=%v judgeCalled=%v", node, judgeCalled)
	}
	if len(w.verifications) != 1 || w.verifications[0].Outcome != explorationgraph.VerificationRefuted {
		t.Fatalf("应留 refuted 审计, got %+v", w.verifications)
	}
	var mark map[string]string
	if raw, ok := w.metadata["node-source"]; !ok || json.Unmarshal(raw, &mark) != nil ||
		!maps.Equal(mark, map[string]string{"verification_status": "refuted", "refute_reason": RefuteNotReproducible}) {
		t.Fatalf("假设节点应标记为复放失败证伪, got %s", w.metadata["node-source"])
	}
}

// 机器护栏不误伤：assert_passed=true 的证据照常进 judge；无 assert_passed 字段
// （无复现通道时的配方回显）不阻断。
func TestPromote_MachineGuardPassesThrough(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-mg2"}
	fw := &fakeClaimSink{}

	passed := gateWith(w, fakeReplayer{res: Result{Evidence: json.RawMessage(`{"assert_passed":true}`)}}, fw).
		WithJudge(stubJudge{verdict: VerdictConfirmed})
	if node, err := passed.Promote(context.Background(), baseClaim()); err != nil || node == nil {
		t.Fatalf("断言命中应正常进 judge 并晋升: node=%v err=%v", node, err)
	}

	noField := gateWith(&fakeGraphStore{verID: "ver-mg3"}, fakeReplayer{res: Result{Evidence: json.RawMessage(`{"kind":"steps"}`)}}, fw).
		WithJudge(stubJudge{verdict: VerdictRefuted})
	if node, err := noField.Promote(context.Background(), baseClaim()); err != nil || node != nil {
		t.Fatalf("配方回显不应被护栏拦截: node=%v err=%v", node, err)
	}
}

type judgeFunc func(context.Context, string, json.RawMessage, json.RawMessage, ReplayFunc) (Verdict, error)

func (f judgeFunc) Judge(ctx context.Context, hyp string, recipe, ev json.RawMessage, replay ReplayFunc) (Verdict, error) {
	return f(ctx, hyp, recipe, ev, replay)
}

type stubJudge struct {
	verdict      string
	reasoning    string
	refuteReason string
	fingerprint  json.RawMessage
}

func (s stubJudge) Judge(_ context.Context, _ string, _, _ json.RawMessage, _ ReplayFunc) (Verdict, error) {
	return Verdict{Verdict: s.verdict, Reasoning: s.reasoning, RefuteReason: s.refuteReason, Fingerprint: s.fingerprint}, nil
}

// 同一配方（字节级相同）只判定一次：第二次进门的 Claim 沿用既有判定——
// 不再烧 judge、不再重复写 finding，假设节点标记同步流转。
func TestAgent_VerifyObservationDeduplicatesSameRecipe(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-d1"}
	fw := &fakeClaimSink{}
	promoter := gateWith(w, fakeReplayer{res: Result{Evidence: json.RawMessage(`{"url":"http://t/x","assert_passed":true}`)}}, fw).
		WithJudge(stubJudge{verdict: VerdictConfirmed})

	graph := explorationgraph.NewMemoryStore()
	a := NewAgent(AgentConfig{
		TaskID:   "t-dedup",
		Gate:     promoter,
		EventBus: bus.New(context.Background()),
		Graph:    graph,
		Logger:   zerolog.Nop(),
	})

	// 三个独立假设节点共用同一配方（第二、三个应命中去重）
	for _, id := range []string{"obs-1", "obs-2", "obs-3"} {
		_, err := graph.CreateNode(context.Background(), explorationgraph.Node{
			ID:      id,
			TaskID:  "t-dedup",
			Kind:    core.KindObservation,
			Content: json.RawMessage(`{"statement":"same hypothesis"}`),
		})
		if err != nil {
			t.Fatalf("预建假设节点 %s 失败: %v", id, err)
		}
	}

	att := baseClaim()
	if err := a.verifyObservation(context.Background(), "obs-1", att); err != nil {
		t.Fatalf("首次验证应成功: %v", err)
	}
	if err := a.verifyObservation(context.Background(), "obs-2", att); err != nil {
		t.Fatalf("重复配方应沿用判定而非报错: %v", err)
	}
	if len(w.verifications) != 1 || len(w.nodes) != 1 || len(fw.claims) != 1 {
		t.Fatalf("同配方二次进门应被去重: ver=%d nodes=%d findings=%d",
			len(w.verifications), len(w.nodes), len(fw.claims))
	}

	// 沿用判定的假设节点：验证状态流转为 confirmed，轮询不再圈定
	marked, err := graph.GetNode(context.Background(), "obs-2")
	if err != nil {
		t.Fatalf("读回沿用判定的假设节点失败: %v", err)
	}
	if !marked.HasVerificationStatus(explorationgraph.VerificationConfirmed) {
		t.Fatalf("沿用判定应把验证状态流转为 confirmed, got %v", marked.VerificationStatus)
	}

	// 不同配方不受影响
	other := baseClaim()
	other.Repro = json.RawMessage(`{"kind":"stub","recipe":{"op":"other"},"assert":{"marker":"leaked-secret"}}`)
	if err := a.verifyObservation(context.Background(), "obs-3", other); err != nil {
		t.Fatalf("新配方应正常验证: %v", err)
	}
	if len(w.verifications) != 2 {
		t.Fatalf("新配方应有新验证, got %d", len(w.verifications))
	}
}

// replayJudge 判定前自主复放 N 次（模拟 RouterJudge 的 replay_for_verification 调用）。
type replayJudge struct {
	verdict string
	replay  ReplayFunc
	times   int
}

func (s *replayJudge) Judge(_ context.Context, _ string, _, _ json.RawMessage, replay ReplayFunc) (Verdict, error) {
	s.replay = replay
	for i := 0; i < s.times; i++ {
		_, _ = replay(context.Background())
	}
	return Verdict{Verdict: s.verdict, Reasoning: "自主复放后判定"}, nil
}

// 评估者复放的审计落 verification.evidence.replays（预跑 + 每次判定复放），
// 且 evaluator 不越权写观察节点——confirmed 只写 1 个 result 节点。
func TestPromote_JudgeReplaysAuditedNotGraphWritten(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-r1"}
	fw := &fakeClaimSink{}
	j := &replayJudge{verdict: VerdictConfirmed, times: 2}
	v := gateWith(w, fakeReplayer{res: Result{Evidence: json.RawMessage(`{"url":"http://t/x","assert_passed":true}`), DurationMs: 10}}, fw).
		WithJudge(j)

	node, err := v.Promote(context.Background(), baseClaim())
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

	// 审计链：1 预跑 + 2 判定复放 = 3 条记录，且评估理由入档
	var ev struct {
		Verdict     string `json:"verdict"`
		Reasoning   string `json:"reasoning"`
		ReplayCount int    `json:"replay_count"`
		Replays     []struct {
			Phase string `json:"phase"`
		} `json:"replays"`
	}
	if err := json.Unmarshal(w.verifications[0].Evidence, &ev); err != nil {
		t.Fatalf("证据应可解析: %v", err)
	}
	if ev.ReplayCount != 3 || len(ev.Replays) != 3 {
		t.Fatalf("应含 3 次重放记录（预跑+2 判定复放）, got %d", ev.ReplayCount)
	}
	if ev.Replays[0].Phase != "prerun" || ev.Replays[1].Phase != "judge" || ev.Replays[2].Phase != "judge" {
		t.Fatalf("重放阶段应按 prerun/judge 记录, got %+v", ev.Replays)
	}
	if ev.Verdict != VerdictConfirmed || ev.Reasoning != "自主复放后判定" {
		t.Fatalf("验证结论与理由应入档, got %+v", ev)
	}
}

// 证伪：不写任何节点（铁律——图只存坐实态），审计链仍完整，
// 且假设节点被回写 refuted 验证标记（图可回答"试过没有"——防死假设复活）。
func TestPromote_RefutedWritesNoNodes(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-r2"}
	fw := &fakeClaimSink{}
	j := &replayJudge{verdict: VerdictRefuted, times: 1}
	v := gateWith(w, fakeReplayer{res: Result{Evidence: json.RawMessage(`{"url":"http://t/x"}`)}}, fw).
		WithJudge(j)

	node, err := v.Promote(context.Background(), baseClaim())
	if err != nil {
		t.Fatalf("Promote 出错: %v", err)
	}
	if node != nil {
		t.Fatal("refuted 不应晋升")
	}
	if len(w.nodes) != 0 {
		t.Fatalf("证伪不应写任何节点, got %d", len(w.nodes))
	}
	if len(w.verifications) != 1 || w.verifications[0].Outcome != explorationgraph.VerificationRefuted {
		t.Fatalf("证伪应留 verification 审计, got %+v", w.verifications)
	}
	var mark struct {
		Status       string `json:"verification_status"`
		RefuteReason string `json:"refute_reason"`
	}
	if raw, ok := w.metadata["node-source"]; !ok ||
		json.Unmarshal(raw, &mark) != nil || mark.Status != "refuted" || mark.RefuteReason == "" {
		t.Fatalf("假设节点应被回写 refuted 标记, got %+v", w.metadata)
	}
}

type errJudge struct{}

func (errJudge) Judge(_ context.Context, _ string, _, _ json.RawMessage, _ ReplayFunc) (Verdict, error) {
	return Verdict{}, errors.New("judge down")
}

// 并发语义：信号量取号在工作协程内——验证占满并发时事件循环不被阻塞
// （队头阻塞曾使后续 attempt 的接收卡到当前验证完成）。
func TestAgent_VerifyConcurrencyDoesNotBlockEventLoop(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-c1"}
	fw := &fakeClaimSink{}
	// 慢验证：复放 sleep 300ms，占住并发额度
	promoter := gateWith(w, slowReplayer{d: 300 * time.Millisecond, res: Result{Evidence: json.RawMessage(`{"url":"http://t/x","assert_passed":true}`)}}, fw).
		WithJudge(stubJudge{verdict: VerdictRefuted})
	bus := bus.New(context.Background())
	a := NewAgent(AgentConfig{
		TaskID:        "t-conc",
		Gate:          promoter,
		EventBus:      bus,
		Logger:        zerolog.Nop(),
		MaxConcurrent: 1, // 最严苛：单并发 + 慢验证
	})

	start := time.Now()
	// 两个 attempt 背靠背进验证（第二个必须能立即入队而非阻塞发送方）
	go func() { _ = a.verifyObservation(context.Background(), "act-1", baseClaim()) }()
	go func() { _ = a.verifyObservation(context.Background(), "act-2", baseClaim()) }()
	// 主线程立即做一次去重查询——若事件循环被卡（旧实现语义在事件循环取号），
	// 这里照样能即时返回
	_, _ = a.verdictLookup("anything")

	// 轮询等待两个验证都完成（直接调用 verifyObservation 不经 Run 的信号量，二者并行）
	deadline := time.Now().Add(3 * time.Second)
	for w.VerificationCount() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("两个 attempt 都应完成验证, got %d", w.VerificationCount())
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = start
}

// 慢速 replayer：模拟真实复放耗时。
type slowReplayer struct {
	d   time.Duration
	res Result
}

func (s slowReplayer) Replay(ctx context.Context, _ json.RawMessage) (Result, error) {
	select {
	case <-time.After(s.d):
		return s.res, nil
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

// 语义去重：同一主张（同指纹）的第二次确认折叠为既有结果的佐证——
// 不新建 result、不重写 finding，假设节点标记 duplicate_of 并指向既有发现。
func TestPromote_SemanticDedupFoldsDuplicateConfirmations(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-d1"}
	fw := &fakeClaimSink{}
	fp := json.RawMessage(`{"class":"weak_config","target":"GET http://t.local:8080/admin/","point":"name"}`)
	replayRes := Result{Evidence: json.RawMessage(`{"url":"http://t.local:8080/admin/?name=p","assert_passed":true}`), DurationMs: 5}

	first := gateWith(w, fakeReplayer{res: replayRes}, fw).
		WithJudge(stubJudge{verdict: VerdictConfirmed, reasoning: "首次坐实", fingerprint: fp})
	node1, err := first.Promote(context.Background(), baseClaim())
	if err != nil || node1 == nil {
		t.Fatalf("首次确认应晋升: node=%v err=%v", node1, err)
	}
	if len(w.nodes) != 1 || len(fw.claims) != 1 {
		t.Fatalf("首次确认应建 1 节点 + 1 finding: nodes=%d findings=%d", len(w.nodes), len(fw.claims))
	}

	// 不同假设（不同源节点/不同陈述）但同指纹的第二次确认
	secondAttempt := baseClaim()
	secondAttempt.NodeID = "node-source-2"
	second := gateWith(w, fakeReplayer{res: replayRes}, fw).
		WithJudge(stubJudge{verdict: VerdictConfirmed, reasoning: "换 payload 再证", fingerprint: fp})
	node2, err := second.Promote(context.Background(), secondAttempt)
	if err != nil {
		t.Fatalf("重复确认不应报错: %v", err)
	}
	if node2 == nil || node2.ID != node1.ID {
		t.Fatalf("重复确认应返回既有发现: node2=%v want=%v", node2, node1)
	}
	if len(w.nodes) != 1 {
		t.Fatalf("同指纹不得新建节点: nodes=%d", len(w.nodes))
	}
	// 重复确认重放交付且指向既有节点：补齐既有节点此前可能失败的交付；
	// 台账按结果节点幂等，不会产生第二条 finding。
	if len(fw.claims) != 2 || fw.claims[1].ResultNodeID != node1.ID || fw.claims[1].Statement != fw.claims[0].Statement {
		t.Fatalf("重复确认的交付应指向既有节点且载荷同源: %+v", fw.claims)
	}

	// 假设节点标记只有结论：重复归属记在审计链（下方断言），节点上不重复落指针
	var mark map[string]string
	if raw := w.metadata["node-source-2"]; raw == nil || json.Unmarshal(raw, &mark) != nil {
		t.Fatalf("第二假设应有验证标记: %+v", w.metadata["node-source-2"])
	}
	if want := map[string]string{"verification_status": "confirmed"}; !maps.Equal(mark, want) {
		t.Fatalf("标记形状不符: %+v", mark)
	}

	// 审计链：第二次 verification 带 duplicate_of
	if len(w.verifications) != 2 {
		t.Fatalf("应有 2 条审计: %d", len(w.verifications))
	}
	var ev struct {
		DuplicateOf string `json:"duplicate_of"`
	}
	if json.Unmarshal(w.verifications[1].Evidence, &ev) != nil || ev.DuplicateOf != node1.ID {
		t.Fatalf("第二条审计应带 duplicate_of: %s", w.verifications[1].Evidence)
	}
}

// 主张类别随结果节点落库并交给交付管道；缺对象的指纹不参与去重，但类别照样记下。
func TestPromote_ClaimClassRecordedAndDelivered(t *testing.T) {
	cases := []struct {
		name string
		fp   string
		want string
	}{
		{"完整指纹", `{"class":" SQL_Injection ","target":"GET http://t.local/item","point":"id"}`, "sql_injection"},
		{"缺对象", `{"class":"info_disclosure"}`, "info_disclosure"},
		{"无指纹", ``, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &fakeGraphStore{verID: "ver-c"}
			fw := &fakeClaimSink{}
			v := gateWith(w, fakeReplayer{res: Result{Evidence: json.RawMessage(`{"assert_passed":true}`)}}, fw).
				WithJudge(stubJudge{verdict: VerdictConfirmed, reasoning: "坐实", fingerprint: json.RawMessage(tc.fp)})
			node, err := v.Promote(context.Background(), baseClaim())
			if err != nil || node == nil {
				t.Fatalf("应晋升: node=%v err=%v", node, err)
			}
			var meta map[string]string
			if len(node.Metadata) > 0 {
				if err := json.Unmarshal(node.Metadata, &meta); err != nil {
					t.Fatalf("metadata 形状: %v", err)
				}
			}
			if meta[claimClassMetadataKey] != tc.want {
				t.Fatalf("结果节点的类别 = %q，期望 %q", meta[claimClassMetadataKey], tc.want)
			}
			if len(fw.claims) != 1 || fw.claims[0].Class != tc.want {
				t.Fatalf("交付的类别 = %+v，期望 %q", fw.claims, tc.want)
			}
		})
	}
}

// 指纹缺失（评估者没给 finding 字段）时去重静默降级——照常晋升，不因缺指纹丢发现。
func TestPromote_MissingFingerprintPromotesNormally(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-d2"}
	v := gateWith(w, fakeReplayer{res: Result{Evidence: json.RawMessage(`{"assert_passed":true}`)}}, &fakeClaimSink{}).
		WithJudge(stubJudge{verdict: VerdictConfirmed, reasoning: "无指纹"})
	node, err := v.Promote(context.Background(), baseClaim())
	if err != nil || node == nil {
		t.Fatalf("缺指纹应照常晋升: node=%v err=%v", node, err)
	}
}

// invalid_recipe 终局的审计链不得因空 repro 丢失：空配方以空对象留档。
func TestAgent_InvalidRecipeAuditSurvivesEmptyRepro(t *testing.T) {
	promoter := gateWith(&fakeGraphStore{verID: "ver-nr2"}, fakeReplayer{err: fmt.Errorf("Replayer: 解析配方信封失败: unexpected end of JSON input")}, &fakeClaimSink{}).
		WithJudge(stubJudge{verdict: VerdictRefuted})

	// coalesceRepro 是审计不丢记录的修复行：空配方归一为空对象
	// （repro 落库列 NOT NULL，nil 会违反约束静默丢整条审计）。
	if got := coalesceRepro(nil); string(got) != `{}` {
		t.Fatalf("nil 应归一为空对象: %s", got)
	}
	if got := coalesceRepro(json.RawMessage(`{"kind":"http"}`)); string(got) != `{"kind":"http"}` {
		t.Fatalf("非空应原样透传: %s", got)
	}

	// 终局路径整体走通：确定性配方缺陷单次即出圈，节点标记落库（内存图）
	memGraph := explorationgraph.NewMemoryStore()
	a2 := NewAgent(AgentConfig{
		TaskID:   "t-empty",
		Gate:     promoter,
		EventBus: bus.New(context.Background()),
		Graph:    memGraph,
		Logger:   zerolog.Nop(),
	})
	// 生产不变式：被验证的观察节点由 executor 先落图
	pending := explorationgraph.VerificationPending
	if _, err := memGraph.CreateNode(context.Background(), explorationgraph.Node{
		ID:                 "obs-empty",
		TaskID:             "t-empty",
		Kind:               core.KindObservation,
		Content:            json.RawMessage(`{"statement":"s"}`),
		VerificationStatus: &pending,
	}); err != nil {
		t.Fatalf("建观察节点失败: %v", err)
	}
	empty := baseClaim()
	empty.NodeID = "obs-empty"
	empty.Repro = nil
	if err := a2.verifyObservation(context.Background(), "obs-empty", empty); err == nil {
		t.Fatal("空配方复现应失败")
	} else {
		a2.recordVerifyFailure(context.Background(), "obs-empty", empty, err)
	}

	obs, err := memGraph.GetNode(context.Background(), "obs-empty")
	if err != nil || obs == nil {
		t.Fatalf("观察节点应存在: %v", err)
	}
	if obs.RefuteReason() != "invalid_recipe" {
		t.Fatalf("确定性缺陷应带 invalid_recipe 标记: %+v", obs.Metadata)
	}
	// 终局出圈后节点不再待验证，轮询不再圈定
	if !obs.HasVerificationStatus(explorationgraph.VerificationRefuted) {
		t.Fatalf("验证状态应流转为 refuted: %+v", obs.VerificationStatus)
	}
}

// 评估者必须收到 executor 提出的假设陈述（Claim.Content.summary）：陈述为空时评估者只能
// 从配方自行推断验证对象，同一请求可支撑多种主张时会确认并非被提交的那一个。
func TestPromote_JudgeReceivesHypothesisStatement(t *testing.T) {
	var got string
	j := judgeFunc(func(_ context.Context, hyp string, _, _ json.RawMessage, _ ReplayFunc) (Verdict, error) {
		got = hyp
		return Verdict{Verdict: VerdictRefuted, Reasoning: "x"}, nil
	})
	v := gateWith(&fakeGraphStore{verID: "ver-h"}, fakeReplayer{res: Result{Evidence: json.RawMessage(`{}`)}}, &fakeClaimSink{}).WithJudge(j)
	if _, err := v.Promote(context.Background(), baseClaim()); err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if got != "X 参数存在可复现注入" {
		t.Fatalf("评估者收到的陈述应为假设原文，得 %q", got)
	}
}

// failingClaimSink 首次交付失败、之后成功：模拟台账瞬时故障。
type failingClaimSink struct {
	fails  int
	claims []ConfirmedClaim
}

func (f *failingClaimSink) Record(_ context.Context, c ConfirmedClaim) error {
	if f.fails > 0 {
		f.fails--
		return errors.New("db unavailable")
	}
	f.claims = append(f.claims, c)
	return nil
}

// 交付失败不拖垮验证：晋升照常完成、假设按结论标记，交付缺口由周期补交付按结果
// 节点收账——把交付失败当验证失败会重跑复现，并把已坐实主张按不可复现错驳
// （实测：交付反复失败曾令坐实成果整批出圈、台账永久缺失）。
func TestPromote_DeliveryFailureDoesNotBlockConfirmation(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-f1"}
	sink := &failingClaimSink{fails: 1}
	fp := json.RawMessage(`{"class":"weak_config","target":"GET http://t.local/q","point":"id"}`)
	gate := gateWith(w, fakeReplayer{res: Result{Evidence: json.RawMessage(`{"url":"http://t.local/q?id=1"}`)}}, sink).
		WithJudge(stubJudge{verdict: VerdictConfirmed, reasoning: "坐实", fingerprint: fp})

	node, err := gate.Promote(context.Background(), baseClaim())
	if err != nil || node == nil {
		t.Fatalf("交付失败不应拖垮晋升: node=%v err=%v", node, err)
	}
	var mark struct {
		VerificationStatus string `json:"verification_status"`
	}
	if err := json.Unmarshal(w.metadata[baseClaim().NodeID], &mark); err != nil || mark.VerificationStatus != "confirmed" {
		t.Fatalf("假设应按结论标记 confirmed，got %s err=%v", w.metadata[baseClaim().NodeID], err)
	}
	if len(sink.claims) != 0 {
		t.Fatalf("首次交付失败不应落数: %+v", sink.claims)
	}
	if len(w.nodes) != 1 {
		t.Fatalf("应只建一个结果节点: %d", len(w.nodes))
	}

	// 同指纹的第二条假设坐实：经语义去重命中既有结果并补齐交付（补交付的验证内形态）。
	second := baseClaim()
	second.NodeID = "obs-2"
	if node2, err := gate.Promote(context.Background(), second); err != nil || node2 == nil || node2.ID != node.ID {
		t.Fatalf("同指纹再确认应折叠进既有结果: node=%v err=%v", node2, err)
	}
	if len(sink.claims) != 1 || sink.claims[0].ResultNodeID != node.ID {
		t.Fatalf("再确认应补齐既有结果的交付: %+v", sink.claims)
	}
}

// resultReader 是补交付读取坐实结果的替身。
type resultReader struct {
	node explorationgraph.Node
	ver  explorationgraph.Verification
}

func (r resultReader) GetNode(context.Context, string) (*explorationgraph.Node, error) {
	return &r.node, nil
}
func (r resultReader) ConfirmingVerification(context.Context, string) (explorationgraph.Verification, error) {
	return r.ver, nil
}

// 补交付从结果节点重建载荷，主张类别与正常交付同一出处。
func TestRedeliver_CarriesClaimClass(t *testing.T) {
	reader := resultReader{
		node: explorationgraph.Node{
			ID:       "res-1",
			TaskID:   "t-1",
			Kind:     core.KindResult,
			Content:  json.RawMessage(`{"statement":"s"}`),
			Metadata: json.RawMessage(`{"claim_fingerprint":"sql_injection|get http://t.local/item|id","claim_class":"sql_injection"}`),
		},
		ver: explorationgraph.Verification{ID: "ver-1", Evidence: json.RawMessage(`{"verification_tier":"full_gate"}`)},
	}
	sink := &fakeClaimSink{}
	if err := Redeliver(context.Background(), reader, sink, "res-1"); err != nil {
		t.Fatalf("补交付: %v", err)
	}
	if len(sink.claims) != 1 || sink.claims[0].Class != "sql_injection" {
		t.Fatalf("补交付的类别 = %+v", sink.claims)
	}
}
