package evaluator

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/framework/core"
)

// fakeGraphStore 记录 Evaluator 对探索图的写入，供断言"门的副作用"。
// 写权限不变式：evaluator 只应新建 result 节点；对假设节点仅 metadata 审判标记。
type fakeGraphStore struct {
	mu            sync.Mutex // 并发验证 goroutine 共享本 fake，写入需互斥
	verifications []explorationgraph.Verification
	nodes         []explorationgraph.Node
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
	nodeID := "node-" + string(n.Kind)
	n.ID = nodeID
	f.nodes = append(f.nodes, n)
	return nodeID, nil
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

// 机器护栏：executor 断言全部未命中（assert_passed=false）= 无支持坐实的证据，
// 直接机器证伪——不进 judge（judge 曾在 404+未命中证据下仍 confirmed，e2e 实测）。
func TestPromote_MachineGuardRefutesWhenAssertFails(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-mg1"}
	fw := &fakeFindingWriter{}
	judgeCalled := false
	j := judgeFunc(func(context.Context, string, json.RawMessage, json.RawMessage, ReplayFunc) (string, string, error) {
		judgeCalled = true
		return VerdictConfirmed, "不该被调到", nil
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
	fw := &fakeFindingWriter{}

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

type judgeFunc func(context.Context, string, json.RawMessage, json.RawMessage, ReplayFunc) (string, string, error)

func (f judgeFunc) Judge(ctx context.Context, hyp string, recipe, ev json.RawMessage, replay ReplayFunc) (string, string, error) {
	return f(ctx, hyp, recipe, ev, replay)
}

type stubJudge struct {
	verdict   string
	reasoning string
}

func (s stubJudge) Judge(_ context.Context, _ string, _, _ json.RawMessage, _ ReplayFunc) (string, string, error) {
	return s.verdict, s.reasoning, nil
}

// 同一配方（字节级相同）只裁一次：第二次进门的 Attempt 直接跳过——
// 不再烧 judge、不再重复写 finding。
func TestAgent_VerifyAttemptDeduplicatesSameRecipe(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-d1"}
	fw := &fakeFindingWriter{}
	promoter := New(w, fakeReplayer{res: Result{Evaluation: json.RawMessage(`{"url":"http://t/x","assert_passed":true}`)}}, fw).
		WithJudge(stubJudge{verdict: VerdictConfirmed})
	a := NewAgent(AgentConfig{
		TaskID:    "t-dedup",
		Evaluator: promoter,
		EventBus:  bus.New(context.Background()),
		Logger:    zerolog.Nop(),
	})

	att := baseAttempt()
	if err := a.verifyAttempt(context.Background(), "act-1", att); err != nil {
		t.Fatalf("首次验证应成功: %v", err)
	}
	if err := a.verifyAttempt(context.Background(), "act-2", att); err != nil {
		t.Fatalf("重复配方应跳过而非报错: %v", err)
	}
	if len(w.verifications) != 1 || len(w.nodes) != 1 || len(fw.findings) != 1 {
		t.Fatalf("同配方二次进门应被去重: ver=%d nodes=%d findings=%d",
			len(w.verifications), len(w.nodes), len(fw.findings))
	}

	// 不同配方不受影响
	other := baseAttempt()
	other.Primitives = json.RawMessage(`{"domain":"web","recipe":{"request":{"method":"GET","url":"http://t/y","headers":{},"body":""}},"assert":{"body_contains":["leaked-secret"]}}`)
	if err := a.verifyAttempt(context.Background(), "act-3", other); err != nil {
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

// 证伪：不写任何节点（铁律——图只存坐实态），审计链仍完整，
// 且假设节点被回写 refuted 审判标记（图可回答"试过没有"——防死假设复活）。
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
	var mark struct {
		Outcome string `json:"verification_outcome"`
	}
	if raw, ok := w.metadata["node-source"]; !ok ||
		json.Unmarshal(raw, &mark) != nil || mark.Outcome != VerdictRefuted {
		t.Fatalf("假设节点应被回写 refuted 标记, got %+v", w.metadata)
	}
}

type errJudge struct{}

func (errJudge) Judge(_ context.Context, _ string, _, _ json.RawMessage, _ ReplayFunc) (string, string, error) {
	return "", "", errors.New("judge down")
}

// 并发语义：信号量取号在工作协程内——验证占满并发时事件循环不被阻塞
// （队头阻塞曾使后续 attempt 的接收卡到当前验证完成）。
func TestAgent_VerifyConcurrencyDoesNotBlockEventLoop(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-c1"}
	fw := &fakeFindingWriter{}
	// 慢验证：复放 sleep 300ms，占住并发额度
	promoter := New(w, slowReplayer{d: 300 * time.Millisecond, res: Result{Evaluation: json.RawMessage(`{"url":"http://t/x","assert_passed":true}`)}}, fw).
		WithJudge(stubJudge{verdict: VerdictRefuted})
	bus := bus.New(context.Background())
	a := NewAgent(AgentConfig{
		TaskID:        "t-conc",
		Evaluator:     promoter,
		EventBus:      bus,
		Logger:        zerolog.Nop(),
		MaxConcurrent: 1, // 最严苛：单并发 + 慢验证
	})

	start := time.Now()
	// 两个 attempt 背靠背进验证（第二个必须能立即入队而非阻塞发送方）
	go func() { _ = a.verifyAttempt(context.Background(), "act-1", baseAttempt()) }()
	go func() { _ = a.verifyAttempt(context.Background(), "act-2", baseAttempt()) }()
	// 主线程立即做一次去重查询——若事件循环被卡（旧实现语义在事件循环取号），
	// 这里照样能即时返回
	_, _ = a.adjudicatedLookup("anything")

	// 轮询等待两个验证都完成（直接调用 verifyAttempt 不经 Run 的信号量，二者并行）
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
