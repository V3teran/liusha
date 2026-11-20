package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// fakeStack 用一个 httptest 服务端同时扮演 TSecBench 答题 API 与 liusha API，
// 记录双方收到的调用，供求解流程测试断言。
type fakeStack struct {
	t   *testing.T
	srv *httptest.Server

	mu sync.Mutex
	// TSecBench 侧
	flags      map[string][]string // unique_code → 正确 flag 列表
	correct    map[string]map[string]bool
	starts     []string
	closes     []string
	submits    []string
	submitErr  func(flag string) (int, string) // 非 nil 且返回非 0 状态时以该业务错误响应
	startErr   func(call int) (int, string)    // 非 nil 且返回非 0 状态时以该业务错误响应（call 从 1 计）
	startHook  func(call int)                  // 非 nil 时每次 start 前调用（call 从 1 计），用于改变就绪状态
	containers []string
	// liusha 侧
	goals      []string
	controls   []string
	nodesFn    func(call int) []graphNode
	statusFn   func(call int) string
	usageFn    func(call int) int
	nodeCall   int
	statusCall int
	usageCall  int
}

func newFakeStack(t *testing.T) *fakeStack {
	t.Helper()
	f := &fakeStack{
		t:          t,
		flags:      map[string][]string{},
		correct:    map[string]map[string]bool{},
		containers: []string{"10.0.1.2:8080"},
		nodesFn:    func(int) []graphNode { return nil },
		statusFn:   func(int) string { return "active" },
		usageFn:    func(call int) int { return call }, // 默认持续有活动
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /openapi/v1/challenges/start", f.handleStart)
	mux.HandleFunc("POST /openapi/v1/challenges/submit", f.handleSubmit)
	mux.HandleFunc("POST /openapi/v1/challenges/close", f.handleClose)
	mux.HandleFunc("POST /api/auth/login", f.handleLogin)
	mux.HandleFunc("POST /api/tasks", f.handleCreateTask)
	mux.HandleFunc("GET /api/tasks/{id}", f.handleStatus)
	mux.HandleFunc("GET /api/tasks/{id}/usage", f.handleUsage)
	mux.HandleFunc("GET /api/tasks/{id}/exploration/graph", f.handleGraph)
	mux.HandleFunc("POST /api/tasks/{id}/control", f.handleControl)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeBenchErr(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"code": code, "message": code, "detail": map[string]any{}})
}

func (f *fakeStack) handleStart(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	code := r.URL.Query().Get("unique_code")
	f.starts = append(f.starts, code)
	if f.startHook != nil {
		f.startHook(len(f.starts))
	}
	if f.startErr != nil {
		if status, ecode := f.startErr(len(f.starts)); status != 0 {
			writeBenchErr(w, status, ecode)
			return
		}
	}
	writeJSON(w, http.StatusOK, startResp{UniqueCode: code, ContainerAddr: f.containers})
}

func (f *fakeStack) handleClose(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	code := r.URL.Query().Get("unique_code")
	f.closes = append(f.closes, code)
	writeJSON(w, http.StatusOK, closeResp{UniqueCode: code, Closed: true})
}

func (f *fakeStack) handleSubmit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UniqueCode string `json:"unique_code"`
		Flag       string `json:"flag"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBenchErr(w, http.StatusUnprocessableEntity, "bad_body")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submits = append(f.submits, body.Flag)
	if f.submitErr != nil {
		if status, code := f.submitErr(body.Flag); status != 0 {
			writeBenchErr(w, status, code)
			return
		}
	}
	got := f.correct[body.UniqueCode]
	if got == nil {
		got = map[string]bool{}
		f.correct[body.UniqueCode] = got
	}
	if got[body.Flag] {
		writeBenchErr(w, http.StatusConflict, codeDuplicate)
		return
	}
	want := f.flags[body.UniqueCode]
	ok := false
	for _, fl := range want {
		if fl == body.Flag {
			ok = true
		}
	}
	if ok {
		got[body.Flag] = true
	}
	writeJSON(w, http.StatusOK, submitResp{
		Correct: ok, Awarded: map[bool]int{true: 10}[ok], CumulativeScore: 10 * len(got),
		CorrectFlagCount: len(got), TotalFlagCount: len(want),
	})
}

func (f *fakeStack) handleLogin(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: liushaSessionCookie, Value: "sess"})
	writeJSON(w, http.StatusOK, map[string]any{})
}

// requireSession 校验 liusha 请求带上了登录下发的会话 Cookie。
func (f *fakeStack) requireSession(w http.ResponseWriter, r *http.Request) bool {
	if ck, err := r.Cookie(liushaSessionCookie); err != nil || ck.Value != "sess" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未登录"})
		return false
	}
	return true
}

func (f *fakeStack) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	if !f.requireSession(w, r) {
		return
	}
	var body struct {
		Briefs []string `json:"briefs"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	// 与真实端点同一契约（httpapi.normalizeBriefs）：brief 按行拆、每行一个任务。
	// fake 若不拆，多行 goal 被拆成多个任务的缺陷就测不出来。
	var lines []string
	for _, b := range body.Briefs {
		for _, line := range strings.Split(b, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, line)
			}
		}
	}
	tasks := make([]map[string]string, 0, len(lines))
	f.mu.Lock()
	for i, line := range lines {
		f.goals = append(f.goals, line)
		tasks = append(tasks, map[string]string{"task_id": fmt.Sprintf("t-%d", i+1)})
	}
	f.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"tasks": tasks})
}

func (f *fakeStack) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !f.requireSession(w, r) {
		return
	}
	f.mu.Lock()
	f.statusCall++
	st := f.statusFn(f.statusCall)
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, taskStatusResp{Status: st})
}

func (f *fakeStack) handleUsage(w http.ResponseWriter, r *http.Request) {
	if !f.requireSession(w, r) {
		return
	}
	f.mu.Lock()
	f.usageCall++
	n := f.usageFn(f.usageCall)
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, usageResp{LLMInvocations: n})
}

func (f *fakeStack) handleGraph(w http.ResponseWriter, r *http.Request) {
	if !f.requireSession(w, r) {
		return
	}
	f.mu.Lock()
	f.nodeCall++
	nodes := f.nodesFn(f.nodeCall)
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, graphResp{Nodes: nodes})
}

func (f *fakeStack) handleControl(w http.ResponseWriter, r *http.Request) {
	if !f.requireSession(w, r) {
		return
	}
	raw, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.controls = append(f.controls, r.PathValue("id")+":"+strings.TrimSpace(string(raw)))
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{})
}

// newSolver 构造指向假服务端的求解器；轮询间隔压到毫秒级让测试快速收敛。
func (f *fakeStack) newSolver() *solver {
	f.t.Helper()
	lc, err := loginLiusha(f.t.Context(), f.srv.URL, "admin", "pw", nil)
	if err != nil {
		f.t.Fatalf("登录假 liusha: %v", err)
	}
	ex, err := newFlagExtractor("")
	if err != nil {
		f.t.Fatalf("构造提取器: %v", err)
	}
	return &solver{
		bench:        newBenchClient(f.srv.URL, "tok", nil),
		liusha:       lc,
		extractor:    ex,
		logger:       zerolog.Nop(),
		pollInterval: time.Millisecond,
		taskDeadline: 5 * time.Second,
	}
}

func (f *fakeStack) snapshot() (starts, closes, submits, controls, goals []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := func(s []string) []string { return append([]string(nil), s...) }
	return cp(f.starts), cp(f.closes), cp(f.submits), cp(f.controls), cp(f.goals)
}
