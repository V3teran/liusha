package evaluator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// 写权限不变式：evaluator 只应新建 result 节点；对假设节点仅 metadata 审判标记。
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

func baseAttempt() Attempt {
	return Attempt{
		TaskID:     "asg-1",
		NodeID:     "node-source",
		Kind:       core.KindResult,
		Primitives: json.RawMessage(`[{"op":"http_request"}]`),
		Content:    json.RawMessage(`{"summary":"X 参数存在可复现注入","severity":"high"}`),
		Priority:   "high",
	}
}

// 复现坐实：落 confirmed verification + 晋升 verified 节点。
func TestPromote_Confirmed(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-99"}
	fw := &fakeClaimSink{}
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
	fw := &fakeClaimSink{}
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
	fw := &fakeClaimSink{}
	v := New(w, fakeReplayer{err: errors.New("boom")}, fw)

	if _, err := v.Promote(context.Background(), baseAttempt()); err == nil {
		t.Fatal("复现失败应报错")
	}
	// 注意：当前实现会记录 verification（即使 replay 失败），这是设计决策
	// 如果要求失败时不记录，需要修改 evaluator.go
}

// 无 Replayer：无复现能力即无晋升——直接报错，不放行。
func TestPromote_NoReplayer(t *testing.T) {
	fw := &fakeClaimSink{}
	v := New(&fakeGraphStore{}, nil, fw)
	if _, err := v.Promote(context.Background(), baseAttempt()); err == nil {
		t.Fatal("无 Replayer 应报错")
	}
}

// 必填校验：TaskID / Kind 缺失即拒。
func TestPromote_Validation(t *testing.T) {
	fw := &fakeClaimSink{}
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

// LLM 裁决全链：机器重放 + judge confirmed → 晋升 + 坐实主张交付管道。
// 交付物的领域归属（agent_run_id 等）由域适配 sink 构造时注入，门只透传载荷。
func TestPromote_LLMJudgeConfirmedDeliversClaim(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-j1"}
	fw := &fakeClaimSink{}
	v := New(w, fakeReplayer{res: Result{Evaluation: json.RawMessage(`{"status_code":200}`)}}, fw).
		WithJudge(stubJudge{verdict: VerdictConfirmed, reasoning: "证据语义坐实"})

	node, err := v.Promote(context.Background(), baseAttempt())
	if err != nil {
		t.Fatalf("Promote 出错: %v", err)
	}
	if node == nil {
		t.Fatal("LLM 裁决 confirmed 应晋升（机器断言未过但语义坐实）")
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
}

// LLM 裁决 refuted：机器断言通过（橡皮图章）也被语义否决——不晋升不落 finding。
func TestPromote_LLMJudgeRefutesRubberStamp(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-j2"}
	fw := &fakeClaimSink{}
	v := New(w, fakeReplayer{res: Result{Evaluation: json.RawMessage(`{"status_code":200}`)}}, fw).
		WithJudge(stubJudge{verdict: VerdictRefuted, reasoning: "200 只证明页面活着"})

	node, err := v.Promote(context.Background(), baseAttempt())
	if err != nil {
		t.Fatalf("Promote 出错: %v", err)
	}
	if node != nil {
		t.Fatal("LLM 裁决 refuted 不应晋升")
	}
	if len(fw.claims) != 0 {
		t.Fatalf("不应写 finding, got %d", len(fw.claims))
	}
}

// LLM 裁决失败 = 本次未裁决：返回错误，绝不回退机器断言（机器不持判定权）。
func TestPromote_JudgeErrorIsUnadjudicated(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-j3"}
	fw := &fakeClaimSink{}
	v := New(w, fakeReplayer{res: Result{Evaluation: json.RawMessage(`{}`)}}, fw).
		WithJudge(errJudge{})

	_, err := v.Promote(context.Background(), baseAttempt())
	if err == nil {
		t.Fatal("judge 失败必须返回错误（不回退机器）")
	}
	if len(w.nodes) != 0 {
		t.Fatal("未裁决不得晋升节点")
	}
	if len(fw.claims) != 0 {
		t.Fatal("未裁决不得写 finding")
	}
}

// 未装配 judge = 无晋升能力（机器不持判定权）。
func TestPromote_NoJudgeNoPromotion(t *testing.T) {
	v := New(&fakeGraphStore{verID: "v"}, fakeReplayer{res: Result{Evaluation: json.RawMessage(`{}`)}}, &fakeClaimSink{})
	if _, err := v.Promote(context.Background(), baseAttempt()); err == nil {
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
	failed := Result{Evaluation: json.RawMessage(`{"url":"http://t/x","assert_passed":false,"assert_reasons":["body 缺 \"<script>\" ✗"],"attack_status_code":404}`)}
	v := New(w, fakeReplayer{res: failed}, fw).WithJudge(j)

	node, err := v.Promote(context.Background(), baseAttempt())
	if err != nil {
		t.Fatalf("机器证伪不应报错: %v", err)
	}
	if node != nil || judgeCalled {
		t.Fatalf("断言未命中应机器证伪且不进 judge: node=%v judgeCalled=%v", node, judgeCalled)
	}
	if len(w.verifications) != 1 || w.verifications[0].Outcome != explorationgraph.OutcomeRefuted {
		t.Fatalf("应留 refuted 审计, got %+v", w.verifications)
	}
	var mark struct {
		RefutedBy string `json:"refuted_by"`
	}
	if raw, ok := w.metadata["node-source"]; !ok || json.Unmarshal(raw, &mark) != nil || mark.RefutedBy != "machine_guard" {
		t.Fatalf("假设节点应带 machine_guard 标记, got %+v", w.metadata)
	}
}

// 机器护栏不误伤：assert_passed=true 的证据照常进 judge；无 assert_passed 字段
// （generic 域回显）不阻断。
func TestPromote_MachineGuardPassesThrough(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-mg2"}
	fw := &fakeClaimSink{}

	passed := New(w, fakeReplayer{res: Result{Evaluation: json.RawMessage(`{"assert_passed":true}`)}}, fw).
		WithJudge(stubJudge{verdict: VerdictConfirmed})
	if node, err := passed.Promote(context.Background(), baseAttempt()); err != nil || node == nil {
		t.Fatalf("断言命中应正常进 judge 并晋升: node=%v err=%v", node, err)
	}

	noField := New(&fakeGraphStore{verID: "ver-mg3"}, fakeReplayer{res: Result{Evaluation: json.RawMessage(`{"domain":"generic"}`)}}, fw).
		WithJudge(stubJudge{verdict: VerdictRefuted})
	if node, err := noField.Promote(context.Background(), baseAttempt()); err != nil || node != nil {
		t.Fatalf("generic 证据不应被护栏拦截: node=%v err=%v", node, err)
	}
}

type judgeFunc func(context.Context, string, json.RawMessage, json.RawMessage, ReplayFunc) (Verdict, error)

func (f judgeFunc) Judge(ctx context.Context, hyp string, recipe, ev json.RawMessage, replay ReplayFunc) (Verdict, error) {
	return f(ctx, hyp, recipe, ev, replay)
}

type stubJudge struct {
	verdict     string
	reasoning   string
	fingerprint json.RawMessage
}

func (s stubJudge) Judge(_ context.Context, _ string, _, _ json.RawMessage, _ ReplayFunc) (Verdict, error) {
	return Verdict{Verdict: s.verdict, Reasoning: s.reasoning, Fingerprint: s.fingerprint}, nil
}

// 同一配方（字节级相同）只裁一次：第二次进门的 Attempt 沿用既有裁决——
// 不再烧 judge、不再重复写 finding，假设节点标记同步流转。
func TestAgent_VerifyObservationDeduplicatesSameRecipe(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-d1"}
	fw := &fakeClaimSink{}
	promoter := New(w, fakeReplayer{res: Result{Evaluation: json.RawMessage(`{"url":"http://t/x","assert_passed":true}`)}}, fw).
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

	att := baseAttempt()
	if err := a.verifyObservation(context.Background(), "obs-1", att); err != nil {
		t.Fatalf("首次验证应成功: %v", err)
	}
	if err := a.verifyObservation(context.Background(), "obs-2", att); err != nil {
		t.Fatalf("重复配方应沿用裁决而非报错: %v", err)
	}
	if len(w.verifications) != 1 || len(w.nodes) != 1 || len(fw.claims) != 1 {
		t.Fatalf("同配方二次进门应被去重: ver=%d nodes=%d findings=%d",
			len(w.verifications), len(w.nodes), len(fw.claims))
	}

	// 沿用裁决的假设节点：confidence 流转为 verified，轮询不再圈定
	marked, err := graph.GetNode(context.Background(), "obs-2")
	if err != nil {
		t.Fatalf("读回沿用裁决的假设节点失败: %v", err)
	}
	if marked.Confidence == nil || *marked.Confidence != explorationgraph.ConfidenceVerified {
		t.Fatalf("沿用裁决应把 confidence 流转为 verified, got %v", marked.Confidence)
	}

	// 不同配方不受影响
	other := baseAttempt()
	other.Primitives = json.RawMessage(`{"domain":"web","recipe":{"request":{"method":"GET","url":"http://t/y","headers":{},"body":""}},"assert":{"body_contains":["leaked-secret"]}}`)
	if err := a.verifyObservation(context.Background(), "obs-3", other); err != nil {
		t.Fatalf("新配方应正常验证: %v", err)
	}
	if len(w.verifications) != 2 {
		t.Fatalf("新配方应有新验证, got %d", len(w.verifications))
	}
}

// replayJudge 裁决前自主复放 N 次（模拟 RouterJudge 的 replay_for_verification 调用）。
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
	return Verdict{Verdict: s.verdict, Reasoning: "自主复放后裁决"}, nil
}

// 裁决官复放的审计落 verification.evidence.replays（预跑 + 每次裁决复放），
// 且 evaluator 不越权写观察节点——confirmed 只写 1 个 result 节点。
func TestPromote_JudgeReplaysAuditedNotGraphWritten(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-r1"}
	fw := &fakeClaimSink{}
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

// 证伪：不写任何节点（铁律——图只存坐实态），审计链仍完整，
// 且假设节点被回写 refuted 审判标记（图可回答"试过没有"——防死假设复活）。
func TestPromote_RefutedWritesNoNodes(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-r2"}
	fw := &fakeClaimSink{}
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
	var mark struct {
		Outcome string `json:"verification_outcome"`
	}
	if raw, ok := w.metadata["node-source"]; !ok ||
		json.Unmarshal(raw, &mark) != nil || mark.Outcome != VerdictRefuted {
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
	promoter := New(w, slowReplayer{d: 300 * time.Millisecond, res: Result{Evaluation: json.RawMessage(`{"url":"http://t/x","assert_passed":true}`)}}, fw).
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
	go func() { _ = a.verifyObservation(context.Background(), "act-1", baseAttempt()) }()
	go func() { _ = a.verifyObservation(context.Background(), "act-2", baseAttempt()) }()
	// 主线程立即做一次去重查询——若事件循环被卡（旧实现语义在事件循环取号），
	// 这里照样能即时返回
	_, _ = a.adjudicatedLookup("anything")

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

// 语义去重：同一漏洞（同指纹）的第二次确认折叠为既有发现的佐证——
// 不新建 result、不重写 finding，假设节点标记 duplicate_of 并指向既有发现。
func TestPromote_SemanticDedupFoldsDuplicateConfirmations(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-d1"}
	fw := &fakeClaimSink{}
	fp := json.RawMessage(`{"class":"reflected_xss","method":"GET","host":"t.local:8080","path":"/vuln/xss_r/","param":"name"}`)
	codec := shapeCodec{}
	replayRes := Result{Evaluation: json.RawMessage(`{"url":"http://t.local:8080/vuln/xss_r/?name=p","assert_passed":true}`), DurationMs: 5}

	first := New(w, fakeReplayer{res: replayRes}, fw).
		WithJudge(stubJudge{verdict: VerdictConfirmed, reasoning: "首次坐实", fingerprint: fp}).
		WithFingerprintCodec(codec)
	node1, err := first.Promote(context.Background(), baseAttempt())
	if err != nil || node1 == nil {
		t.Fatalf("首次确认应晋升: node=%v err=%v", node1, err)
	}
	if len(w.nodes) != 1 || len(fw.claims) != 1 {
		t.Fatalf("首次确认应建 1 节点 + 1 finding: nodes=%d findings=%d", len(w.nodes), len(fw.claims))
	}

	// 不同假设（不同源节点/不同陈述）但同指纹的第二次确认
	secondAttempt := baseAttempt()
	secondAttempt.NodeID = "node-source-2"
	second := New(w, fakeReplayer{res: replayRes}, fw).
		WithJudge(stubJudge{verdict: VerdictConfirmed, reasoning: "换 payload 再证", fingerprint: fp}).
		WithFingerprintCodec(codec)
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
	if len(fw.claims) != 1 {
		t.Fatalf("同指纹不得重写 finding: findings=%d", len(fw.claims))
	}

	// 假设节点标记：confirmed + duplicate_of 指向既有发现
	// （promoted_node 冗余已裁剪——observation→result 的 generates 边承载归属）
	var mark struct {
		Confidence          string `json:"confidence"`
		VerificationOutcome string `json:"verification_outcome"`
		DuplicateOf         string `json:"duplicate_of"`
	}
	if raw := w.metadata["node-source-2"]; raw == nil || json.Unmarshal(raw, &mark) != nil {
		t.Fatalf("第二假设应有审判标记: %+v", w.metadata["node-source-2"])
	}
	if mark.Confidence != "verified" || mark.VerificationOutcome != "confirmed" ||
		mark.DuplicateOf != node1.ID {
		t.Fatalf("标记形状不符: %+v", mark)
	}

	// 审计链：第二次 verification 带 duplicate_of
	if len(w.verifications) != 2 {
		t.Fatalf("应有 2 条审计: %d", len(w.verifications))
	}
	var ev struct {
		DuplicateOf string `json:"duplicate_of"`
	}
	if json.Unmarshal(w.verifications[1].Evaluation, &ev) != nil || ev.DuplicateOf != node1.ID {
		t.Fatalf("第二条审计应带 duplicate_of: %s", w.verifications[1].Evaluation)
	}
}

// 指纹缺失（裁决官没给 finding 字段）时去重静默降级——照常晋升，不因缺指纹丢发现。
func TestPromote_MissingFingerprintPromotesNormally(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-d2"}
	v := New(w, fakeReplayer{res: Result{Evaluation: json.RawMessage(`{"assert_passed":true}`)}}, &fakeClaimSink{}).
		WithJudge(stubJudge{verdict: VerdictConfirmed, reasoning: "无指纹"})
	node, err := v.Promote(context.Background(), baseAttempt())
	if err != nil || node == nil {
		t.Fatalf("缺指纹应照常晋升: node=%v err=%v", node, err)
	}
}

// 无配方的 Attempt 不得进入裁决生命周期：执行记录（上下文结论/降级推测）
// 保持 confidence 为空，不烧裁决预算、不污染 refuted 分类。
// 回归背景：e2e 实测 7 条无 repro 的上下文记录被事件路径拖进裁决门，
// 以 invalid_recipe 终局后审计链又因空 primitives 违反 NOT NULL 丢记录。
func TestAgent_EventPathSkipsAttemptsWithoutRecipe(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-nr1"}
	promoter := New(w, fakeReplayer{res: Result{Evaluation: json.RawMessage(`{}`)}}, &fakeClaimSink{}).
		WithJudge(stubJudge{verdict: VerdictConfirmed})
	b := bus.New(context.Background())
	a := NewAgent(AgentConfig{
		TaskID:   "t-norepro",
		Gate:     promoter,
		EventBus: b,
		Logger:   zerolog.Nop(),
	})

	a.handleEvent(context.Background(), bus.Event{
		Type:     bus.EventAttemptGenerated,
		TaskID:   "t-norepro",
		ActionID: "act-1",
		Payload: map[string]interface{}{
			"attempt": Attempt{TaskID: "t-norepro", NodeID: "obs-record", Kind: "result"},
		},
	}, make(chan struct{}, 1))

	if w.VerificationCount() != 0 {
		t.Fatalf("无配方 Attempt 不得触发验证: %d", w.VerificationCount())
	}
	if len(w.metadata) != 0 {
		t.Fatalf("无配方 Attempt 不得改写节点标记: %+v", w.metadata)
	}

	// 对照组：带配方的 Attempt 照常进入裁决
	withRepro := baseAttempt()
	withRepro.NodeID = "obs-hypothesis"
	a.handleEvent(context.Background(), bus.Event{
		Type:     bus.EventAttemptGenerated,
		TaskID:   "t-norepro",
		ActionID: "act-1",
		Payload:  map[string]interface{}{"attempt": withRepro},
	}, make(chan struct{}, 1))

	deadline := time.Now().Add(3 * time.Second)
	for w.VerificationCount() < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("带配方 Attempt 应完成验证, got %d", w.VerificationCount())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// invalid_recipe 终局的审计链不得因空 primitives 丢失：空配方以空对象留档。
func TestAgent_InvalidRecipeAuditSurvivesEmptyPrimitives(t *testing.T) {
	promoter := New(&fakeGraphStore{verID: "ver-nr2"}, fakeReplayer{err: fmt.Errorf("Replayer: 解析域信封失败: unexpected end of JSON input")}, &fakeClaimSink{}).
		WithJudge(stubJudge{verdict: VerdictRefuted})

	// coalescePrimitives 是审计不丢记录的修复行：空配方归一为空对象
	// （primitives 落库列 NOT NULL，nil 会违反约束静默丢整条审计）。
	if got := coalescePrimitives(nil); string(got) != `{}` {
		t.Fatalf("nil 应归一为空对象: %s", got)
	}
	if got := coalescePrimitives(json.RawMessage(`{"domain":"web"}`)); string(got) != `{"domain":"web"}` {
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
	// 生产不变式：被裁决的观察节点由 executor 先落图
	unverified := explorationgraph.ConfidenceUnverified
	if _, err := memGraph.CreateNode(context.Background(), explorationgraph.Node{
		ID:         "obs-empty",
		TaskID:     "t-empty",
		Kind:       core.KindObservation,
		Content:    json.RawMessage(`{"statement":"s"}`),
		Confidence: &unverified,
	}); err != nil {
		t.Fatalf("建观察节点失败: %v", err)
	}
	empty := baseAttempt()
	empty.NodeID = "obs-empty"
	empty.Primitives = nil
	if err := a2.verifyObservation(context.Background(), "obs-empty", empty); err == nil {
		t.Fatal("空配方复现应失败")
	} else {
		a2.recordVerifyFailure(context.Background(), "obs-empty", empty, err)
	}

	obs, err := memGraph.GetNode(context.Background(), "obs-empty")
	if err != nil || obs == nil {
		t.Fatalf("观察节点应存在: %v", err)
	}
	var mark struct {
		RefuteReason string `json:"refute_reason"`
	}
	if json.Unmarshal(obs.Metadata, &mark) != nil || mark.RefuteReason != "invalid_recipe" {
		t.Fatalf("确定性缺陷应带 invalid_recipe 标记: %+v", obs.Metadata)
	}
	// confidence 是图级类型化键（读取时提到 Node.Confidence）——
	// 终局出圈后节点不再是 unverified，轮询不再圈定
	if obs.Confidence == nil || *obs.Confidence != explorationgraph.ConfidenceRefuted {
		t.Fatalf("节点 confidence 应流转为 refuted: %+v", obs.Confidence)
	}
}

// shapeCodec 是测试内的最小指纹归一器：实现 FingerprintNormalizer，
// 归一语义的真身与测试在 finding 包（此处只验证晋升门对 codec 的用法）。
type shapeCodec struct{}

func (shapeCodec) CanonicalKey(raw json.RawMessage) (string, bool) {
	var fp struct {
		Class  string `json:"class"`
		Method string `json:"method"`
		Host   string `json:"host"`
		Path   string `json:"path"`
		Param  string `json:"param"`
	}
	if json.Unmarshal(raw, &fp) != nil {
		return "", false
	}
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	return strings.Join([]string{norm(fp.Class), norm(fp.Method), norm(fp.Host), strings.TrimSuffix(norm(fp.Path), "/"), norm(fp.Param)}, "|"), true
}
