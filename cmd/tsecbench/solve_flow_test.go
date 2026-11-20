package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// res 构造坐实 flag 的 result 节点（flag 经复现门晋升后的形态，正文是主张描述）。
func res(text string) graphNode {
	b, _ := json.Marshal(map[string]string{"statement": text})
	return graphNode{Kind: "result", Content: b}
}

// obs 构造未坐实的观察节点（诱饵、待验证假设都在这层，不应被提取）。
func obs(text string) graphNode {
	b, _ := json.Marshal(map[string]string{"output": text})
	return graphNode{Kind: "observation", Content: b}
}

// TestSolve_FlagFromResultCompletesAndCleansUp 验证主路径：坐实 result 正文里的 flag 被提交、
// 本题通关，且收尾时终止 liusha 任务并 close 靶场释放名额。
func TestSolve_FlagFromResultCompletesAndCleansUp(t *testing.T) {
	f := newFakeStack(t)
	f.flags["web_01"] = []string{"flag{abc}"}
	f.nodesFn = func(int) []graphNode {
		return []graphNode{res("捕获 flag：flag{abc}")}
	}

	res, err := f.newSolver().solve(t.Context(), challenge{UniqueCode: "web_01", FlagCount: 1})
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if !res.Completed || res.CorrectFlags != 1 || res.CumulativeScore != 10 {
		t.Fatalf("res = %+v", res)
	}
	starts, closes, _, controls, goals := f.snapshot()
	if !slices.Equal(starts, []string{"web_01"}) || !slices.Equal(closes, []string{"web_01"}) {
		t.Fatalf("starts=%v closes=%v", starts, closes)
	}
	if len(controls) != 1 || !strings.Contains(controls[0], `"terminate"`) {
		t.Fatalf("应终止 liusha 任务，controls=%v", controls)
	}
	if len(goals) != 1 || !strings.Contains(goals[0], "10.0.1.2:8080") {
		t.Fatalf("goal 应带 start 返回的靶场地址，goals=%v", goals)
	}
}

// TestSolve_SubmitsOnlyConfirmedStatement 验证只提交主张陈述里的 flag：复现门坐实的是陈述，
// 结果正文里的影响、建议是执行者的附言（可能提到见过的诱饵），不是被确认的内容。
func TestSolve_SubmitsOnlyConfirmedStatement(t *testing.T) {
	f := newFakeStack(t)
	f.flags["web_01"] = []string{"flag{real}"}
	f.nodesFn = func(int) []graphNode {
		b, _ := json.Marshal(map[string]string{
			"statement":  "捕获 flag{real}",
			"impact":     "首页另有 flag{decoy_page}，已判为诱饵",
			"suggestion": "忽略 flag{not_this}",
		})
		return []graphNode{{Kind: "result", Content: b}}
	}

	res, err := f.newSolver().solve(t.Context(), challenge{UniqueCode: "web_01", FlagCount: 1})
	if err != nil || !res.Completed {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if _, _, submits, _, _ := f.snapshot(); !slices.Equal(submits, []string{"flag{real}"}) {
		t.Fatalf("只应提交陈述里的 flag，submits=%v", submits)
	}
}

// TestSolve_StartRetriesTransientErrors 验证启动靶场遇到可重试错误（503/500）会重试：
// 平台把它定义为网络波动，一次失败就跳题会白白丢掉整道题。
func TestSolve_StartRetriesTransientErrors(t *testing.T) {
	f := newFakeStack(t)
	f.flags["web_01"] = []string{"flag{abc}"}
	f.startErr = func(call int) (int, string) {
		if call <= 2 {
			return http.StatusServiceUnavailable, codeResourceUnavailable
		}
		return 0, ""
	}
	f.nodesFn = func(int) []graphNode { return []graphNode{res("flag{abc}")} }

	res, err := f.newSolver().solve(t.Context(), challenge{UniqueCode: "web_01", FlagCount: 1})
	if err != nil || !res.Completed {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
	if starts, _, _, _, _ := f.snapshot(); len(starts) != 3 {
		t.Fatalf("应重试到成功，starts=%v", starts)
	}
}

// TestSolve_StartGivesUpWithoutTask 验证启动始终失败时有限次重试后放弃：不建 liusha 任务，
// 靶场照常 close 释放名额。
func TestSolve_StartGivesUpWithoutTask(t *testing.T) {
	f := newFakeStack(t)
	f.startErr = func(int) (int, string) { return http.StatusServiceUnavailable, codeResourceUnavailable }

	if _, err := f.newSolver().solve(t.Context(), challenge{UniqueCode: "web_01", FlagCount: 1}); err == nil {
		t.Fatal("启动始终失败应报错")
	}
	starts, closes, _, _, goals := f.snapshot()
	if len(starts) != startAttempts || len(goals) != 0 || !slices.Equal(closes, []string{"web_01"}) {
		t.Fatalf("starts=%d（应为 %d） goals=%v closes=%v", len(starts), startAttempts, goals, closes)
	}
}

// TestSolve_NoContainerAddrSkipsWithoutTask 验证启动没给地址就不建任务：授权范围只能是平台
// 给出的地址，没有地址的目标会让智能体自行在 VPN 网段里找靶场。
func TestSolve_NoContainerAddrSkipsWithoutTask(t *testing.T) {
	f := newFakeStack(t)
	f.containers = nil

	if _, err := f.newSolver().solve(t.Context(), challenge{UniqueCode: "web_01", FlagCount: 1}); err == nil {
		t.Fatal("没有靶场地址应报错")
	}
	if _, closes, _, _, goals := f.snapshot(); len(goals) != 0 || !slices.Equal(closes, []string{"web_01"}) {
		t.Fatalf("不应建任务且应 close：goals=%v closes=%v", goals, closes)
	}
}

// TestSolve_UnconfirmedObservationsIgnored 验证只认坐实：观察节点（待验证假设、诱饵、
// 侦察记录）里的 flag 串不参与提取——未过复现门的东西不提交。
func TestSolve_UnconfirmedObservationsIgnored(t *testing.T) {
	f := newFakeStack(t)
	f.flags["web_01"] = []string{"flag{real}"}
	f.nodesFn = func(int) []graphNode {
		return []graphNode{
			obs("诱饵页写着 flag{fake_not_this}，疑似假"),
			obs("待验证假设：flag{real}"),
		}
	}
	f.usageFn = func(int) int { return 42 } // 活动停摆：让空闲收手快速触发
	sv := f.newSolver()
	sv.idleWindow = 10 * time.Millisecond
	res, err := sv.solve(t.Context(), challenge{UniqueCode: "web_01", FlagCount: 1})
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if res.Completed {
		t.Fatalf("未坐实的观察不应产生提交，res = %+v", res)
	}
	if _, _, submits, _, _ := f.snapshot(); len(submits) != 0 {
		t.Fatalf("不应有任何提交，got %v", submits)
	}
}

// TestSolve_MultiFlagCollectsFromResults 验证多 flag 题：两个 flag 分别出现在不同的
// result 节点，错误候选不计入，全部正确后通关；action 节点的文本不参与提取。
func TestSolve_MultiFlagCollectsFromResults(t *testing.T) {
	f := newFakeStack(t)
	f.flags["web_02"] = []string{"flag{one}", "flag{two}"}
	f.nodesFn = func(int) []graphNode {
		action, _ := json.Marshal(map[string]string{"plan": "试试 flag{guess}"})
		return []graphNode{
			res("确认 flag{one}"),
			{Kind: "action", Content: action},
			res("响应体 flag{wrong} 与 flag{two}"),
		}
	}

	res, err := f.newSolver().solve(t.Context(), challenge{UniqueCode: "web_02", FlagCount: 2})
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if !res.Completed || res.CorrectFlags != 2 {
		t.Fatalf("res = %+v", res)
	}
	_, _, submits, _, _ := f.snapshot()
	if slices.Contains(submits, "flag{guess}") {
		t.Fatalf("action 节点文本不应提交：%v", submits)
	}
	if submits[0] != "flag{one}" {
		t.Fatalf("靠前的观察应先提交，submits=%v", submits)
	}
	if n := countOf(submits, "flag{wrong}"); n != 1 {
		t.Fatalf("错误候选应只提交一次，实际 %d 次：%v", n, submits)
	}
}

func countOf(s []string, v string) int {
	n := 0
	for _, x := range s {
		if x == v {
			n++
		}
	}
	return n
}

// TestHarvest_FinalRoundAfterTaskEnds 验证任务结束前最后时刻写下的 flag 不会漏：
// 第一轮提取时还没有 flag，查状态已是 completed，收手前的补充提取拿到并提交。
func TestHarvest_FinalRoundAfterTaskEnds(t *testing.T) {
	f := newFakeStack(t)
	f.flags["web_03"] = []string{"flag{late}"}
	f.nodesFn = func(call int) []graphNode {
		if call == 1 {
			return nil
		}
		return []graphNode{res("flag{late}")}
	}
	f.statusFn = func(int) string { return "completed" }

	res, err := f.newSolver().solve(t.Context(), challenge{UniqueCode: "web_03", FlagCount: 1})
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	if !res.Completed {
		t.Fatalf("任务结束后的补充提取应拿到 flag，res=%+v", res)
	}
}

// TestHarvest_TaskEndedWithoutFlag 验证任务结束且没有 flag 时干净收手（不报错、未通关）。
func TestHarvest_TaskEndedWithoutFlag(t *testing.T) {
	f := newFakeStack(t)
	f.statusFn = func(int) string { return "aborted" }

	res, err := f.newSolver().solve(t.Context(), challenge{UniqueCode: "web_04", FlagCount: 1})
	if err != nil || res.Completed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	_, closes, _, _, _ := f.snapshot()
	if len(closes) != 1 {
		t.Fatalf("收手也要 close 靶场，closes=%v", closes)
	}
}

// TestHarvest_StopsWhenIdle 验证 LLM/工具调用数长时间不变时提前换题。
func TestHarvest_StopsWhenIdle(t *testing.T) {
	f := newFakeStack(t)
	f.usageFn = func(int) int { return 7 }
	s := f.newSolver()
	s.idleWindow = 20 * time.Millisecond
	s.taskDeadline = 0 // 只靠停摆判定收手

	res, err := s.solve(t.Context(), challenge{UniqueCode: "web_05", FlagCount: 1})
	if err != nil || res.Completed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// TestHarvest_StopsAtDeadline 验证单题轮询上限到点即换题。
func TestHarvest_StopsAtDeadline(t *testing.T) {
	f := newFakeStack(t)
	s := f.newSolver()
	s.taskDeadline = 20 * time.Millisecond

	res, err := s.solve(t.Context(), challenge{UniqueCode: "web_06", FlagCount: 1})
	if err != nil || res.Completed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// TestHarvest_ContextCanceled 验证全局取消时求解返回 ctx 错误且仍 close 靶场。
func TestHarvest_ContextCanceled(t *testing.T) {
	f := newFakeStack(t)
	s := f.newSolver()
	s.taskDeadline = 0
	s.pollInterval = time.Hour

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	_, err := s.solve(ctx, challenge{UniqueCode: "web_07", FlagCount: 1})
	if err == nil {
		t.Fatal("取消后应返回错误")
	}
	_, closes, _, _, _ := f.snapshot()
	if len(closes) != 1 {
		t.Fatalf("取消也要 close 靶场，closes=%v", closes)
	}
}

// TestTrySubmit_ErrorClassification 验证提交错误的分流：token 失效 / 任务结束为致命；
// duplicate 记为已提交；可重试与 challenge_not_found 不致命、不记为已提交。
func TestTrySubmit_ErrorClassification(t *testing.T) {
	cases := []struct {
		name          string
		status        int
		code          string
		wantFatal     bool
		wantSubmitted bool
	}{
		{"token 失效", http.StatusNotFound, codeTaskNotFound, true, false},
		{"任务结束", http.StatusConflict, codeInvalidState, true, false},
		{"重复提交", http.StatusConflict, codeDuplicate, false, true},
		{"资源未就绪", http.StatusServiceUnavailable, codeResourceUnavailable, false, false},
		{"内部错误", http.StatusInternalServerError, codeInternalError, false, false},
		{"题目不存在", http.StatusNotFound, codeChallengeNotFound, false, false},
		{"未知业务码", http.StatusBadRequest, "weird", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeStack(t)
			f.submitErr = func(string) (int, string) { return tc.status, tc.code }
			s := f.newSolver()
			submitted := map[string]bool{}
			var res challengeResult
			log := zerolog.Nop()

			done, fatal := s.trySubmit(t.Context(), &log, "web_01", "flag{x}", submitted, &res)
			if done {
				t.Fatal("出错时不应判通关")
			}
			if (fatal != nil) != tc.wantFatal {
				t.Fatalf("fatal=%v, wantFatal=%v", fatal, tc.wantFatal)
			}
			if submitted["flag{x}"] != tc.wantSubmitted {
				t.Fatalf("submitted=%v, want %v", submitted["flag{x}"], tc.wantSubmitted)
			}
		})
	}
}

// TestSolve_FatalSubmitErrorPropagates 验证提交遇 token 失效时 solve 返回可识别的致命错误，
// 供 schedule 取消全局。
func TestSolve_FatalSubmitErrorPropagates(t *testing.T) {
	f := newFakeStack(t)
	f.nodesFn = func(int) []graphNode { return []graphNode{res("flag{a}")} }
	f.submitErr = func(string) (int, string) { return http.StatusNotFound, codeTaskNotFound }

	_, err := f.newSolver().solve(t.Context(), challenge{UniqueCode: "web_08", FlagCount: 1})
	var be *benchError
	if !errors.As(err, &be) || !be.tokenInvalid() {
		t.Fatalf("期望 token 失效错误，得到 %v", err)
	}
}

// TestSolve_RetriableSubmitRetriedNextRound 验证可重试错误的 flag 下一轮会再次提交并通关。
func TestSolve_RetriableSubmitRetriedNextRound(t *testing.T) {
	f := newFakeStack(t)
	f.flags["web_09"] = []string{"flag{r}"}
	f.nodesFn = func(int) []graphNode { return []graphNode{res("flag{r}")} }
	calls := 0
	f.submitErr = func(string) (int, string) {
		calls++
		if calls == 1 {
			return http.StatusServiceUnavailable, codeResourceUnavailable
		}
		return 0, ""
	}

	res, err := f.newSolver().solve(t.Context(), challenge{UniqueCode: "web_09", FlagCount: 1})
	if err != nil || !res.Completed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// TestSchedule_StopsDispatchOnFatal 验证一道题报致命错误后不再派发后续题目。
func TestSchedule_StopsDispatchOnFatal(t *testing.T) {
	f := newFakeStack(t)
	f.nodesFn = func(int) []graphNode { return []graphNode{res("flag{a}")} }
	f.submitErr = func(string) (int, string) { return http.StatusConflict, codeInvalidState }
	s := f.newSolver()
	cfg := config{concurrency: 1, pollInterval: time.Millisecond, taskDeadline: 5 * time.Second}
	pending := []challenge{{UniqueCode: "a", FlagCount: 1}, {UniqueCode: "b", FlagCount: 1}, {UniqueCode: "c", FlagCount: 1}}

	results := schedule(t.Context(), cfg, s.bench, s.liusha, s.extractor, zerolog.Nop(), pending)
	starts, _, _, _, _ := f.snapshot()
	if len(starts) != 1 {
		t.Fatalf("致命错误后不应再派发，starts=%v", starts)
	}
	if len(results) != 1 {
		t.Fatalf("results=%+v", results)
	}
}

// TestSchedule_SolvesAll 验证正常情况下全部题目都被求解并汇总。
func TestSchedule_SolvesAll(t *testing.T) {
	f := newFakeStack(t)
	for _, c := range []string{"a", "b", "c", "d"} {
		f.flags[c] = []string{"flag{ok}"}
	}
	f.nodesFn = func(int) []graphNode { return []graphNode{res("flag{ok}")} }
	s := f.newSolver()
	cfg := config{concurrency: 3, pollInterval: time.Millisecond, taskDeadline: 5 * time.Second}
	pending := []challenge{{UniqueCode: "a"}, {UniqueCode: "b"}, {UniqueCode: "c"}, {UniqueCode: "d"}}

	results := schedule(t.Context(), cfg, s.bench, s.liusha, s.extractor, zerolog.Nop(), pending)
	if len(results) != 4 {
		t.Fatalf("results=%+v", results)
	}
	for _, r := range results {
		if !r.Completed {
			t.Fatalf("%s 未通关：%+v", r.UniqueCode, r)
		}
	}
	printSummary(zerolog.Nop(), 4, results) // 冒烟：汇总不崩
}
