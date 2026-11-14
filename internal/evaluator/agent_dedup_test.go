package evaluator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// TestClaimHashKeysOnStatementAndRecipe 锁定验证键语义：验证对象是"假设+配方"，
// 同配方不同陈述是两次独立判定，不得沿用结论（历史 bug：纯配方哈希让"XSS"和
// "SQLi"共享同一配方时互相继承判定）。
func TestClaimHashKeysOnStatementAndRecipe(t *testing.T) {
	recipe := json.RawMessage(`{"domain":"web","recipe":{"request":{"method":"GET","url":"http://t/search?q=1"}},"assert":{"body_contains":["leaked"]}}`)

	xss := Claim{Content: json.RawMessage(`{"statement":"/search 存在反射型 XSS"}`), Repro: recipe}
	sqli := Claim{Content: json.RawMessage(`{"statement":"/search 存在 SQL 注入"}`), Repro: recipe}
	sameAsXSS := Claim{Content: json.RawMessage(`{"statement":"/search 存在反射型 XSS","severity":"high"}`), Repro: recipe}

	if claimHash(xss) == claimHash(sqli) {
		t.Fatal("同配方不同陈述不得同键：会跨假设沿用判定")
	}
	if claimHash(xss) != claimHash(sameAsXSS) {
		t.Fatal("同陈述同配方（载荷多无关字段）应同键")
	}
}

// TestClaimFromHypothesisContentShape 锁定从假设节点构造的载荷形状：
// statement/impact/suggestion 三字段（结果与假设的陈述同名）；优先级取观察节点的
// 优先级（只在节点层存一份）。
func TestClaimFromHypothesisContentShape(t *testing.T) {
	a := NewAgent(AgentConfig{Logger: zerolog.New(io.Discard)})

	obs := explorationgraph.Node{
		ID:       "obs-1",
		TaskID:   "task-1",
		Kind:     core.KindObservation,
		Priority: explorationgraph.PriorityHigh,
		Content:  json.RawMessage(`{"statement":"S 存在漏洞","impact":"i","suggestion":"s","repro":{"domain":"generic","recipe":{"steps":["curl x"]},"assert":{"description":"d"}}}`),
	}
	att, err := a.claimFromHypothesis(obs)
	if err != nil {
		t.Fatalf("claimFromHypothesis: %v", err)
	}
	var c map[string]string
	if err := json.Unmarshal(att.Content, &c); err != nil {
		t.Fatalf("unmarshal content: %v", err)
	}
	want := map[string]string{"statement": "S 存在漏洞", "impact": "i", "suggestion": "s"}
	if !maps.Equal(c, want) {
		t.Fatalf("载荷形状不符: %+v", c)
	}
	if att.Priority != "high" {
		t.Fatalf("Claim 优先级应取观察节点的优先级，得 %q", att.Priority)
	}
}

// TestRecordVerifyFailure_InvalidRecipeFailsFast 锁定 fail-fast 语义：复现通道认定的
// 配方缺陷（ErrInvalidRecipe）重放必然同样失败，一次即终局出圈；未认定为配方缺陷的
// 失败按重试阶梯计数。分类只认哨兵错误，不按错误文本猜。
func TestRecordVerifyFailure_InvalidRecipeFailsFast(t *testing.T) {
	cases := []struct {
		name        string
		cause       error
		wantRefuted bool
	}{
		{"配方缺陷一次出圈", fmt.Errorf("verifier: 复现执行失败: %w: 未知 source", ErrInvalidRecipe), true},
		// 文本里有「解析失败」「unmarshal」也不算——不是复现通道认定的配方缺陷
		{"文本相似但非哨兵不出圈", errors.New("judge: 解析失败: json: cannot unmarshal array"), false},
		{"目标不可达计一次不出圈", fmt.Errorf("%w: EOF", ErrTargetUnreachable), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memGraph := explorationgraph.NewMemoryStore()
			a := NewAgent(AgentConfig{TaskID: "t1", EventBus: bus.New(context.Background()), Graph: memGraph, Logger: zerolog.Nop()})
			pending := explorationgraph.VerificationPending
			if _, err := memGraph.CreateNode(context.Background(), explorationgraph.Node{
				ID: "obs-f", TaskID: "t1", Kind: core.KindObservation,
				Content: json.RawMessage(`{"statement":"S"}`), VerificationStatus: &pending,
			}); err != nil {
				t.Fatalf("建观察失败: %v", err)
			}
			a.recordVerifyFailure(context.Background(), "obs-f", Claim{TaskID: "t1", NodeID: "obs-f"}, tc.cause)
			node, err := memGraph.GetNode(context.Background(), "obs-f")
			if err != nil {
				t.Fatalf("读回节点失败: %v", err)
			}
			if got := node.HasVerificationStatus(explorationgraph.VerificationRefuted); got != tc.wantRefuted {
				t.Fatalf("一次失败后 refuted=%v，期望 %v（refute_reason=%q）", got, tc.wantRefuted, node.RefuteReason())
			}
			if tc.wantRefuted && node.RefuteReason() != RefuteInvalidRecipe {
				t.Fatalf("refute_reason=%q，期望 invalid_recipe", node.RefuteReason())
			}
		})
	}
}

// evaluator 管辖边界不变式：轮询只圈待验证的假设——事实
// 是 executor 写入即终态的观察，出处即信任，不消耗验证预算、永不晋升
// （result 只承载经判定坐实的主张）。
func TestPollAndVerify_OnlyPicksPending(t *testing.T) {
	w := &fakeGraphStore{verID: "ver-boundary"}
	promoter := gateWith(w, fakeReplayer{res: Result{Evidence: json.RawMessage(`{"assert_passed":true}`)}}, &fakeClaimSink{}).
		WithJudge(stubJudge{verdict: VerdictConfirmed})
	b := bus.New(context.Background())
	memGraph := explorationgraph.NewMemoryStore()
	a := NewAgent(AgentConfig{TaskID: "t-boundary", Gate: promoter, EventBus: b, Graph: memGraph, Logger: zerolog.Nop()})

	buildObs := func(id, statement string, status *explorationgraph.VerificationStatus) explorationgraph.Node {
		return explorationgraph.Node{
			ID: id, TaskID: "t-boundary", Kind: core.KindObservation,
			Content:            json.RawMessage(`{"statement":"` + statement + `","repro":{"domain":"generic","recipe":{"steps":"s"},"assert":{"description":"d"}}}`),
			VerificationStatus: status,
		}
	}
	status := func(s explorationgraph.VerificationStatus) *explorationgraph.VerificationStatus { return &s }

	// 三种观察并存：只有待验证的假设应被圈定
	for _, obs := range []explorationgraph.Node{
		buildObs("obs-hyp", "待验证假设", status(explorationgraph.VerificationPending)),
		buildObs("obs-fact", "已确认事实", nil),
		buildObs("obs-done", "已证伪", status(explorationgraph.VerificationRefuted)),
	} {
		if _, err := memGraph.CreateNode(context.Background(), obs); err != nil {
			t.Fatalf("建观察失败: %v", err)
		}
	}

	sem := make(chan struct{}, 1)
	if err := a.pollAndVerify(context.Background(), sem); err != nil {
		t.Fatalf("pollAndVerify: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for w.VerificationCount() < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("待验证假设应被验证, got %d", w.VerificationCount())
		}
		time.Sleep(10 * time.Millisecond)
	}
	// 判定只发生 1 次：fact/refuted 不进队列
	time.Sleep(100 * time.Millisecond)
	if w.VerificationCount() != 1 {
		t.Fatalf("只有待验证假设应进验证管道: %d", w.VerificationCount())
	}
}

// TestClaimFromHypothesisKeepsReproDomain 锁定轮询入口不私改配方：
// 断言只有 description 的 web 配方（机器无从判定）必须原样交复现门终局，
// 不得私自降级成 generic 绕过「空断言不可坐实」。
func TestClaimFromHypothesisKeepsReproDomain(t *testing.T) {
	a := NewAgent(AgentConfig{Logger: zerolog.New(io.Discard)})
	obs := explorationgraph.Node{
		ID:      "obs-web-desc",
		TaskID:  "task-1",
		Kind:    core.KindObservation,
		Content: json.RawMessage(`{"statement":"S","repro":{"domain":"web","recipe":{"request":{"method":"GET","url":"http://t/a","headers":{},"body":""},"baseline":{"method":"GET","url":"http://t/b","headers":{},"body":""}},"assert":{"description":"页面出现管理员数据"}}}`),
	}
	att, err := a.claimFromHypothesis(obs)
	if err != nil {
		t.Fatalf("claimFromHypothesis: %v", err)
	}
	var env struct {
		Domain string          `json:"domain"`
		Recipe json.RawMessage `json:"recipe"`
	}
	if err := json.Unmarshal(att.Repro, &env); err != nil {
		t.Fatalf("unmarshal repro: %v", err)
	}
	if env.Domain != "web" {
		t.Fatalf("轮询入口改写了配方域: domain=%q repro=%s", env.Domain, att.Repro)
	}
}

// TestPollAndVerify_SkipsInFlightBeforeParsing 锁定轮询对在途假设的处理：验证进行中的
// 假设直接跳过——不重复解析配方（归一化失败的配方曾每 5s 告警一次），也不计入
// 「启动验证任务」（计数只统计真正启动的验证）。
func TestPollAndVerify_SkipsInFlightBeforeParsing(t *testing.T) {
	var logs bytes.Buffer
	memGraph := explorationgraph.NewMemoryStore()
	a := NewAgent(AgentConfig{TaskID: "t-inflight", EventBus: bus.New(context.Background()), Graph: memGraph, Logger: zerolog.New(&logs)})

	c := explorationgraph.VerificationPending
	if _, err := memGraph.CreateNode(context.Background(), explorationgraph.Node{
		ID: "obs-inflight", TaskID: "t-inflight", Kind: core.KindObservation,
		Content:            json.RawMessage(`{"statement":"S","repro":{"domain":"generic","recipe":{"steps":["curl x"]}}}`), // 缺 assert：归一化必失败
		VerificationStatus: &c,
	}); err != nil {
		t.Fatalf("建观察失败: %v", err)
	}
	if !a.tryAcquire("obs-inflight") {
		t.Fatal("预置在途锁失败")
	}

	if err := a.pollAndVerify(context.Background(), make(chan struct{}, 1)); err != nil {
		t.Fatalf("pollAndVerify: %v", err)
	}
	for _, unwanted := range []string{"repro 归一化失败", "启动验证任务"} {
		if strings.Contains(logs.String(), unwanted) {
			t.Errorf("在途假设不应产生 %q 日志:\n%s", unwanted, logs.String())
		}
	}
}
