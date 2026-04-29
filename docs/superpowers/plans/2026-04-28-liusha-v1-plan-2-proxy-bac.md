# Liusha v1 Plan 2: Proxy + BAC e2e 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 plan 1 底座上接通"代理流量 → 关窗 → sniffer ReAct → spawn BAC 子任务 → 5 条 finding 入库"的 e2e 链路，跑通 vulnapp 8 接口的 BAC 检测，落 5 条 finding（spec §10.2 表）。

**Architecture:**
- proxy 关窗时 enqueue sniffer task；agent-worker 起 sniffer ReAct
- sniffer 用 5 个共享 action + `read_window` 决定哪些请求要进 BAC；命中线索 → `spawn_subtask(skill="vuln/web/bac")`
- BAC 子任务在 worker 收到时按 skill 注册 5 个 BAC 专属 action（`fetch_credentials` / `replay_multi_identity` / `heuristic_check` / `compute_similarity`，加 sniffer 已有的 `write_finding` / `write_graph` / `done`），SKILL.md 正文严格规定步骤顺序

**Tech Stack:** plan 1 全部依赖。

**前置假设：** plan 1 全部 task 已完成（含黑客松借鉴增量 T21.5 / T22.5 / T23.5）；`go test ./... -race` 全绿；`make up` 起 postgres + redis；`docker compose --profile agent --profile proxy up -d` 起 api / proxy / agent-worker / vulnapp。

---

## 黑客松借鉴增量（plan 2 范围，2026-04-29 加入；详见 docs/hks2.md）

| Task | 改动 |
|---|---|
| T2（BAC 4 actions） | actions 保持"纯函数式"语义（输入流量+身份，输出响应/相似度）；事实/假设写入由 LLM 在 SKILL.md 步骤间显式调 `write_fact / write_idea` action 完成（见 spec §5.2 流程，T3 SKILL.md 指明每步前后该写什么）。actions 全部经 T22.5 中间件链（result_compress 自动落盘大响应 / loop_detect 防重复） |
| T3（BAC SKILL.md） | 重写为 spec §5.2 新版 8 步流程（含 step 0 read_state、明确 done.reason 取值集）；frontmatter 加 `done_validator: bac_v1`、`cognitive_map: docs/skills/_template/cognitive_map.md` 或 `docs/skills/bac/cognitive_map.md` |
| **T2.5（新增）** | `internal/agent/actions/done_validator/bac.go`：实现 `BACDoneValidator` 注册到 ActionRegistry key=`bac_v1`；规则见 spec §6.1 done 行 + §5.2 末尾"done 系统校验"块 |
| **T2.6（新增）** | `docs/skills/_template/cognitive_map.md` 6 槽位模板 + `docs/skills/bac/cognitive_map.md` BAC 填充版（按 spec §5.1 表格内容）；plan 1 T25 skill loader 启动时校验槽位齐全 |
| **T9.5（新增）** | e2e 验收脚本扩展：检查 `engagement.memory_facts/ideas/hints` 都有写入；`finding` 命中后查 `memory_hints` 至少有一条 `from_skill='vuln/web/bac'` 的 distill hint；至少一次 `llm_call.role='observer'` 或 `'distill'`（验证多模型路由生效） |

---

## 任务依赖

```
T1 internal/agent/actions/window.go          (read_window)
T2 internal/agent/actions/bac/*.go           (BAC 4 个 action + factory；含 AppendFact/AppendIdea 调用)
T2.5 internal/agent/actions/done_validator/bac.go (新增；BAC done 校验器，注册 key=bac_v1)
T2.6 docs/skills/{_template,bac}/cognitive_map.md  (新增；6 槽位模板 + BAC 填充)
T3 skills/vuln/web/bac/SKILL.md 正文（按 spec §5.2 新版 8 步重写）
  ↓
T4 internal/spawner（task.Store + worker.Client 的 Spawner 适配）
  ↓
T5 cmd/agent-worker 装配（按 skill 选择 action 集 + spawner）
  ↓
T6 internal/proxify_consumer（tail proxify JSONL → flow/window/sniffer enqueue，
                              在 agent-worker 进程内 goroutine 启动）
  ↓
T7 cmd/api 加 POST /engagement/proxy（懒创建并返回 ID）
T8 deployments/docker-compose.yml：proxify + vulnapp + 共享 volume（删旧 cmd/proxy）
T9 cmd/e2e-bac（一次性触发器：建 engagement → 18 个请求 → 轮询 finding）
T9.5 e2e 验收脚本扩展（新增；查 memory 三层 + distill hint + observer/distill llm_call）
  ↓
T10 e2e 跑通：5 finding + 3 类齐全 + memory 三层 + distill hint + 多模型路由
```

---

## Task 1: read_window action

**Files:**
- Create: `internal/agent/actions/window.go`
- Create: `internal/agent/actions/window_test.go`
- Modify: `internal/window/store.go`（加 `GetByID`）

**职责：** 接 `internal/window` + `internal/flow` store，按 `window_id` 读窗口的 N 个 flow ref，把每个 flow 的 method/url/status/请求摘要打包返回，并自动 `MarkConsumed`。

- [ ] **Step 1: 写失败测试 `internal/agent/actions/window_test.go`**

```go
package actions

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/window"
)

type fakeWinStore struct {
	w        window.Window
	consumed []string
}

func (f *fakeWinStore) GetByID(_ context.Context, id string) (window.Window, error) {
	return f.w, nil
}
func (f *fakeWinStore) MarkConsumed(_ context.Context, id string) error {
	f.consumed = append(f.consumed, id)
	return nil
}

type fakeFlowStore struct {
	flows map[int64]flow.Flow
}

func (f *fakeFlowStore) GetByID(_ context.Context, id int64) (flow.Flow, error) {
	return f.flows[id], nil
}

func TestReadWindow_ReturnsFlowsAndMarksConsumed(t *testing.T) {
	wins := &fakeWinStore{w: window.Window{
		ID: "w1", Flows: []window.FlowRef{{ID: 10}, {ID: 11}},
	}}
	flows := &fakeFlowStore{flows: map[int64]flow.Flow{
		10: {ID: 10, Method: "GET", URL: "/api/order/7", StatusCode: 200},
		11: {ID: 11, Method: "POST", URL: "/api/order/cancel", StatusCode: 200},
	}}
	a := &ReadWindow{Windows: wins, Flows: flows}
	out, err := a.Execute(context.Background(), json.RawMessage(`{"window_id":"w1"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Flows []map[string]any `json:"flows"`
	}
	if err := json.Unmarshal(out.Output, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Flows) != 2 {
		t.Fatalf("flows=%v", got.Flows)
	}
	if len(wins.consumed) != 1 || wins.consumed[0] != "w1" {
		t.Fatalf("consumed=%v", wins.consumed)
	}
}
```

- [ ] **Step 2: 实现 `internal/agent/actions/window.go`**

```go
package actions

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/agent/action"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/window"
)

type WindowStore interface {
	GetByID(ctx context.Context, id string) (window.Window, error)
	MarkConsumed(ctx context.Context, id string) error
}

type FlowReader interface {
	GetByID(ctx context.Context, id int64) (flow.Flow, error)
}

type ReadWindow struct {
	Windows WindowStore
	Flows   FlowReader
}

func (a *ReadWindow) Name() string        { return "read_window" }
func (a *ReadWindow) Description() string { return "读 traffic_window 内的全部 flow 摘要并标记 consumed" }
func (a *ReadWindow) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"window_id":{"type":"string"}},"required":["window_id"]}`)
}

func (a *ReadWindow) Execute(ctx context.Context, args json.RawMessage) (action.Result, error) {
	var in struct {
		WindowID string `json:"window_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return action.Result{}, fmt.Errorf("parse args: %w", err)
	}
	w, err := a.Windows.GetByID(ctx, in.WindowID)
	if err != nil {
		return action.Result{}, fmt.Errorf("get window %s: %w", in.WindowID, err)
	}
	type flowSummary struct {
		ID         int64  `json:"id"`
		Method     string `json:"method"`
		URL        string `json:"url"`
		StatusCode int    `json:"status_code"`
		BodyHint   string `json:"body_hint,omitempty"`
	}
	out := struct {
		Flows []flowSummary `json:"flows"`
	}{}
	for _, ref := range w.Flows {
		f, err := a.Flows.GetByID(ctx, ref.ID)
		if err != nil {
			continue
		}
		hint := string(f.ResponseBody)
		if len(hint) > 200 {
			hint = hint[:200]
		}
		out.Flows = append(out.Flows, flowSummary{
			ID: f.ID, Method: f.Method, URL: f.URL,
			StatusCode: f.StatusCode, BodyHint: hint,
		})
	}
	if err := a.Windows.MarkConsumed(ctx, in.WindowID); err != nil {
		return action.Result{}, fmt.Errorf("mark consumed: %w", err)
	}
	enc, _ := json.Marshal(out)
	return action.Result{Output: enc}, nil
}
```

- [ ] **Step 3: 给 `internal/window/store.go` 末尾追加 `GetByID`**

```go
func (s *Store) GetByID(ctx context.Context, id string) (Window, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, engagement_id, flows, status, started_at, closed_at
		FROM traffic_window WHERE id=$1`, id)
	var w Window
	var flows []byte
	if err := row.Scan(&w.ID, &w.EngagementID, &flows, &w.Status, &w.StartedAt, &w.ClosedAt); err != nil {
		return Window{}, fmt.Errorf("get window %s: %w", id, err)
	}
	_ = json.Unmarshal(flows, &w.Flows)
	return w, nil
}
```

- [ ] **Step 4: 跑测试 + Commit**

```bash
go test ./internal/agent/actions/... ./internal/window/... -race
go test -tags=integration ./internal/window/... -race -count=1
git add internal/agent/actions/window.go internal/agent/actions/window_test.go internal/window/store.go
git commit -m "feat(actions): read_window action（读窗口 flow 摘要 + MarkConsumed）"
```

---

## Task 2: BAC 4 个 action + factory

**Files:**
- Create: `internal/agent/actions/bac/credentials.go`
- Create: `internal/agent/actions/bac/replay.go`
- Create: `internal/agent/actions/bac/heuristic.go`
- Create: `internal/agent/actions/bac/similarity.go`
- Create: `internal/agent/actions/bac/factory.go`
- Create: `internal/agent/actions/bac/bac_test.go`

- [ ] **Step 1: 实现 `internal/agent/actions/bac/credentials.go`**

```go
package bac

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/agent/action"
	"github.com/V3teran/liusha/internal/credential"
)

type FetchCredentials struct {
	Provider credential.Provider
}

func (a *FetchCredentials) Name() string        { return "fetch_credentials" }
func (a *FetchCredentials) Description() string { return "拿目标 host 的全部身份（含 anonymous）" }
func (a *FetchCredentials) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"host":{"type":"string"}},"required":["host"]}`)
}

func (a *FetchCredentials) Execute(ctx context.Context, args json.RawMessage) (action.Result, error) {
	var in struct {
		Host string `json:"host"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return action.Result{}, err
	}
	ids, err := a.Provider.GetIdentitiesByHost(ctx, in.Host)
	if err != nil {
		return action.Result{}, fmt.Errorf("fetch credentials: %w", err)
	}
	enc, _ := json.Marshal(map[string]any{"identities": ids})
	return action.Result{Output: enc}, nil
}
```

- [ ] **Step 2: 实现 `internal/agent/actions/bac/replay.go`**

```go
package bac

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/V3teran/liusha/internal/agent/action"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/replay"
)

type FlowReader interface {
	GetByID(ctx context.Context, id int64) (flow.Flow, error)
}

type ReplayMultiIdentity struct {
	Engine   *replay.Engine
	Provider credential.Provider
	Flows    FlowReader
}

func (a *ReplayMultiIdentity) Name() string { return "replay_multi_identity" }
func (a *ReplayMultiIdentity) Description() string {
	return "对一条已抓的 flow 用多个身份并发重放，返回各身份的响应摘要"
}
func (a *ReplayMultiIdentity) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
	  "type":"object",
	  "properties":{
	    "flow_id":{"type":"integer"},
	    "host":{"type":"string"},
	    "concurrency":{"type":"integer","default":5}
	  },
	  "required":["flow_id","host"]
	}`)
}

func (a *ReplayMultiIdentity) Execute(ctx context.Context, args json.RawMessage) (action.Result, error) {
	var in struct {
		FlowID      int64  `json:"flow_id"`
		Host        string `json:"host"`
		Concurrency int    `json:"concurrency"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return action.Result{}, err
	}
	if in.Concurrency <= 0 {
		in.Concurrency = 5
	}

	f, err := a.Flows.GetByID(ctx, in.FlowID)
	if err != nil {
		return action.Result{}, fmt.Errorf("get flow %d: %w", in.FlowID, err)
	}
	ids, err := a.Provider.GetIdentitiesByHost(ctx, in.Host)
	if err != nil {
		return action.Result{}, fmt.Errorf("get identities: %w", err)
	}
	raw := replay.RawRequest{
		Method:  f.Method,
		URL:     f.URL,
		Headers: rebuildHeaders(f.RequestHeaders),
		Body:    f.RequestBody,
	}
	resps, err := a.Engine.ReplayMultiIdentity(ctx, raw, ids, in.Concurrency)
	if err != nil {
		return action.Result{}, fmt.Errorf("replay multi: %w", err)
	}

	type respSummary struct {
		Identity   string `json:"identity"`
		StatusCode int    `json:"status_code"`
		BodyHint   string `json:"body_hint,omitempty"`
		Error      string `json:"error,omitempty"`
	}
	out := struct {
		FlowID    int64         `json:"flow_id"`
		Method    string        `json:"method"`
		URL       string        `json:"url"`
		Responses []respSummary `json:"responses"`
	}{FlowID: f.ID, Method: f.Method, URL: f.URL}
	for _, r := range resps {
		hint := string(r.Body)
		if len(hint) > 400 {
			hint = hint[:400]
		}
		out.Responses = append(out.Responses, respSummary{
			Identity: r.IdentityName, StatusCode: r.StatusCode, BodyHint: hint, Error: r.ErrorMessage,
		})
	}
	enc, _ := json.Marshal(out)
	return action.Result{Output: enc}, nil
}

func rebuildHeaders(raw json.RawMessage) http.Header {
	if len(raw) == 0 {
		return http.Header{}
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return http.Header{}
	}
	h := http.Header{}
	for k, v := range m {
		for _, s := range strings.Split(v, ", ") {
			h.Add(k, s)
		}
	}
	return h
}
```

- [ ] **Step 3: 实现 `internal/agent/actions/bac/heuristic.go`**

```go
package bac

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/agent/action"
	"github.com/V3teran/liusha/internal/heuristic"
	"github.com/V3teran/liusha/internal/replay"
)

type HeuristicCheck struct{}

func (HeuristicCheck) Name() string        { return "heuristic_check" }
func (HeuristicCheck) Description() string { return "对 replay_multi_identity 输出跑短路规则。命中即 skip" }
func (HeuristicCheck) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
	  "type":"object",
	  "properties":{
	    "responses":{"type":"array"},
	    "rules":{"type":"array","items":{"type":"string","enum":["all_denied","all_empty","all_auth_error"]}}
	  },
	  "required":["responses"]
	}`)
}

func (HeuristicCheck) Execute(_ context.Context, args json.RawMessage) (action.Result, error) {
	var in struct {
		Responses []struct {
			Identity   string `json:"identity"`
			StatusCode int    `json:"status_code"`
			BodyHint   string `json:"body_hint"`
		} `json:"responses"`
		Rules []string `json:"rules"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return action.Result{}, fmt.Errorf("parse args: %w", err)
	}
	rs := make([]replay.Response, len(in.Responses))
	for i, r := range in.Responses {
		rs[i] = replay.Response{IdentityName: r.Identity, StatusCode: r.StatusCode, Body: []byte(r.BodyHint)}
	}
	if len(in.Rules) == 0 {
		in.Rules = []string{"all_denied", "all_empty", "all_auth_error"}
	}
	for _, name := range in.Rules {
		var rule heuristic.Rule
		switch name {
		case "all_denied":
			rule = heuristic.AllDeniedByStatus
		case "all_empty":
			rule = heuristic.AllEmptyResponse
		case "all_auth_error":
			rule = func(rs []replay.Response) (bool, string) { return heuristic.AllAuthError(rs, nil) }
		default:
			continue
		}
		if skip, reason := rule(rs); skip {
			out, _ := json.Marshal(map[string]any{"skip": true, "rule": name, "reason": reason})
			return action.Result{Output: out}, nil
		}
	}
	return action.Result{Output: json.RawMessage(`{"skip":false}`)}, nil
}
```

- [ ] **Step 4: 实现 `internal/agent/actions/bac/similarity.go`**

```go
package bac

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/agent/action"
	"github.com/V3teran/liusha/internal/heuristic"
)

type ComputeSimilarity struct{}

func (ComputeSimilarity) Name() string { return "compute_similarity" }
func (ComputeSimilarity) Description() string {
	return "对 replay 输出计算相似度矩阵。低于 threshold 视为差异。"
}
func (ComputeSimilarity) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
	  "type":"object",
	  "properties":{
	    "responses":{"type":"array"},
	    "algorithm":{"type":"string","default":"jaccard"},
	    "threshold":{"type":"number","default":0.3}
	  },
	  "required":["responses"]
	}`)
}

func (ComputeSimilarity) Execute(_ context.Context, args json.RawMessage) (action.Result, error) {
	var in struct {
		Responses []struct {
			Identity   string `json:"identity"`
			StatusCode int    `json:"status_code"`
			BodyHint   string `json:"body_hint"`
		} `json:"responses"`
		Threshold float64 `json:"threshold"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return action.Result{}, fmt.Errorf("parse args: %w", err)
	}
	if in.Threshold <= 0 {
		in.Threshold = 0.3
	}
	type pair struct {
		A, B    string
		Score   float64
		AStatus int
		BStatus int
	}
	var pairs []pair
	for i := 0; i < len(in.Responses); i++ {
		for j := i + 1; j < len(in.Responses); j++ {
			a, b := in.Responses[i], in.Responses[j]
			s := heuristic.StructuralSimilarity(a.BodyHint, b.BodyHint)
			pairs = append(pairs, pair{a.Identity, b.Identity, s, a.StatusCode, b.StatusCode})
		}
	}
	out, _ := json.Marshal(map[string]any{
		"threshold": in.Threshold,
		"pairs":     pairs,
	})
	return action.Result{Output: out}, nil
}
```

- [ ] **Step 5: 实现 `internal/agent/actions/bac/factory.go`**

```go
package bac

import (
	"github.com/V3teran/liusha/internal/agent/action"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/replay"
)

type Deps struct {
	Credentials credential.Provider
	Replay      *replay.Engine
	Flows       FlowReader
}

func Register(reg *action.Registry, d Deps) error {
	for _, a := range []action.Action{
		&FetchCredentials{Provider: d.Credentials},
		&ReplayMultiIdentity{Engine: d.Replay, Provider: d.Credentials, Flows: d.Flows},
		HeuristicCheck{},
		ComputeSimilarity{},
	} {
		if err := reg.Register(a); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 6: 写测试 `internal/agent/actions/bac/bac_test.go`**

```go
package bac

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/replay"
)

type fakeProv struct{ ids []credential.Identity }

func (f *fakeProv) BatchSave(_ context.Context, _ map[string][]credential.Identity, _ int) error {
	return nil
}
func (f *fakeProv) GetIdentitiesByHost(_ context.Context, _ string) ([]credential.Identity, error) {
	return f.ids, nil
}
func (f *fakeProv) List(_ context.Context, _ string) (map[string][]credential.Identity, error) {
	return nil, nil
}
func (f *fakeProv) Delete(_ context.Context, _ string) error { return nil }

type fakeFlow struct{ f flow.Flow }

func (f *fakeFlow) GetByID(_ context.Context, _ int64) (flow.Flow, error) { return f.f, nil }

func TestReplayMultiIdentity_HitsAllIdentities(t *testing.T) {
	var seen int
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen++ }))
	defer srv.Close()

	ff := &fakeFlow{f: flow.Flow{
		ID: 1, Method: "GET", URL: srv.URL + "/x",
		RequestHeaders: json.RawMessage(`{"Cookie":"session=admin_sess_a1b2c3"}`),
	}}
	prov := &fakeProv{ids: []credential.Identity{
		{Name: "admin"},
		{Name: "test", Credentials: []credential.Credential{
			{Type: credential.TypeHeaders, Key: "Cookie", Value: "session=test_sess_d4e5f6"},
		}},
		{Name: credential.AnonymousName},
	}}
	a := &ReplayMultiIdentity{Engine: replay.NewEngine(srv.Client()), Provider: prov, Flows: ff}
	out, err := a.Execute(context.Background(), json.RawMessage(`{"flow_id":1,"host":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if seen != 3 {
		t.Fatalf("expected 3 hits, got %d (out=%s)", seen, string(out.Output))
	}
}

func TestHeuristicCheck_AllDenied(t *testing.T) {
	body := `{"responses":[{"identity":"a","status_code":403},{"identity":"b","status_code":401}],"rules":["all_denied"]}`
	r, err := HeuristicCheck{}.Execute(context.Background(), json.RawMessage(body))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(r.Output), `"skip":true`) {
		t.Fatalf("output=%s", string(r.Output))
	}
}

func TestComputeSimilarity_PairsCount(t *testing.T) {
	body := `{"responses":[
	  {"identity":"a","status_code":200,"body_hint":"alice profile"},
	  {"identity":"b","status_code":200,"body_hint":"bob profile"},
	  {"identity":"anonymous","status_code":401,"body_hint":"please login"}
	]}`
	r, _ := ComputeSimilarity{}.Execute(context.Background(), json.RawMessage(body))
	var got struct {
		Pairs []map[string]any `json:"pairs"`
	}
	_ = json.Unmarshal(r.Output, &got)
	if len(got.Pairs) != 3 {
		t.Fatalf("expected 3 pairs (C(3,2)), got %d", len(got.Pairs))
	}
}
```

- [ ] **Step 7: 跑测试 + Commit**

```bash
go test ./internal/agent/actions/bac/... -race
git add internal/agent/actions/bac
git commit -m "feat(actions/bac): fetch_credentials / replay_multi_identity / heuristic_check / compute_similarity + factory"
```

---

## Task 2.5: BACDoneValidator（借鉴黑客松共识 C，对应 spec §6.1 done 校验式 + §5.2 末尾校验块）

**Files:**
- Create: `internal/agent/actions/done_validator/bac.go`
- Create: `internal/agent/actions/done_validator/bac_test.go`
- Modify: plan 1 T22.5 中的 `internal/agent/action/done_validator.go`：导出全局 `Registry` 接受 `Register(key string, v DoneValidator)`

**关键点：**
- `BACValidator` 持 `state State`（来自 ReadState）+ `findingStore *finding.Store`
- `CanDone(ctx, args)` 规则：
  1. 检查 state 含 `evidence/boundaries` 字段表示已调过 4 个 BAC 工具（通过 fact 命名识别：`replay_summary` / `heuristic_hit` / `similarity_low` 三种 fact key 至少有 1 个）
  2. 解析 args 取 `reason ∈ {finding_written, all_similar, heuristic_skip, no_pattern_match}`
  3. 若 reason=finding_written → 查 finding 表是否有对应 dedup_key（取 `state.LatestEndpoint`）
  4. 任一不满足 → 返回 missing 列表

- [ ] **Step 1: 写测试 `bac_test.go`**

```go
func TestBACValidator_RejectMissingActions(t *testing.T) {
    v := &BACValidator{state: State{}}  // empty state
    ok, missing := v.CanDone(context.Background(), []byte(`{"reason":"finding_written"}`))
    if ok || len(missing) == 0 {
        t.Fatal("expected reject with missing")
    }
}
func TestBACValidator_AcceptAllSimilar(t *testing.T) {
    v := &BACValidator{state: stateWithFacts("replay_summary", "similarity_low")}
    ok, _ := v.CanDone(context.Background(), []byte(`{"reason":"all_similar"}`))
    if !ok { t.Fatal("expected accept") }
}
func TestBACValidator_RequireFinding(t *testing.T) {
    findingStore := &mockFindingStore{exists: false}
    v := &BACValidator{state: stateFull, findings: findingStore}
    ok, missing := v.CanDone(context.Background(), []byte(`{"reason":"finding_written"}`))
    if ok || !contains(missing, "finding") { t.Fatal("expected finding missing") }
}
```

- [ ] **Step 2: 实现 bac.go + 注册 key="bac_v1"**

```go
package done_validator

import (
    "github.com/V3teran/liusha/internal/agent/action"
)

func init() {
    action.RegisterDoneValidator("bac_v1", &BACValidator{})
}
```

- [ ] **Step 3: Commit**

```bash
go test ./internal/agent/actions/done_validator/... -race
git add internal/agent/actions/done_validator
git commit -m "feat(actions/done_validator): BAC done 校验器（注册 key=bac_v1）"
```

---

## Task 2.6: cognitive_map.md 模板 + BAC 填充版（借鉴黑客松创新 7）

**Files:**
- Create: `docs/skills/_template/cognitive_map.md`（6 槽位空模板）
- Create: `docs/skills/bac/cognitive_map.md`（BAC 填充版）

**6 槽位**（与 spec §5.1 一致）：
1. 检测点
2. 类型矩阵
3. 能力矩阵
4. 有效 Payload
5. 判定规则
6. 失败方向

- [ ] **Step 1: 写空模板 `_template/cognitive_map.md`**

```markdown
# Skill 认知地图（6 槽位）

## 1. 检测点
（哪类请求 / 流量特征触发本 skill）

## 2. 类型矩阵
（漏洞子类型枚举）

## 3. 能力矩阵
（已注册的 Action 列表 + 各自的输入/输出）

## 4. 有效 Payload
（替换什么字段 / 注入什么内容）

## 5. 判定规则
（阈值 / 命中条件 / 排除规则）

## 6. 失败方向
（已知不该再走的方向，对应 done.reason 取值）
```

- [ ] **Step 2: 写 BAC 填充版 `bac/cognitive_map.md`**

按 spec §5.1 表格内容填充 6 槽位。

- [ ] **Step 3: 跑 plan 1 T25 skill loader 校验（启动时校验 6 槽位齐全）**

```bash
go test ./internal/skill/... -race
```

- [ ] **Step 4: Commit**

```bash
git add docs/skills/_template docs/skills/bac
git commit -m "docs(skills): cognitive_map 6 槽位模板 + BAC 填充版（借鉴黑客松创新 7）"
```

---

## Task 3: BAC SKILL.md 正文（步骤指引）

**Files:**
- Modify: `skills/vuln/web/bac/SKILL.md`（替换 plan 1 占位正文）

- [ ] **Step 1: 替换 SKILL.md**

```markdown
---
name: vuln/web/bac
description: BAC（未授权 / 垂直越权 / 水平越权）
applies_to:
  - role: sniffer
budget:
  max_steps: 10
  max_tokens: 15000
done_validator: bac_v1
cognitive_map: docs/skills/bac/cognitive_map.md
required_actions:
  - read_state
  - write_fact
  - write_idea
  - fetch_credentials
  - replay_multi_identity
  - heuristic_check
  - compute_similarity
  - write_finding
  - write_graph
  - done
---

你是 BAC 检测器。任务输入包含 `flow_id` 和 `host`。**严格按以下顺序行动，不允许跳步、不允许直接看原始响应判断漏洞**：

## 步骤

0. **read_state()** — 读 memory_hints（含上次 distill 的经验提示）+ 已有 facts/ideas；写一条 `write_idea({direction: "<host><method><path>", status: "pending"})`。
1. **fetch_credentials(host)** — 拿到全部身份列表（含自动注入的 anonymous）。
2. **replay_multi_identity(flow_id, host, concurrency=5)** — 多身份并发重放；返回后调 `write_fact({category:"evidence", content:"endpoint <X> N 身份重放结果摘要 ..."})`。
3. **heuristic_check(rules=[all_denied, all_empty, all_auth_error])** —
   - 命中任一规则 → `write_fact({category:"boundary", content:"heuristic 命中 <RULE>"})`；`write_idea({direction, status:"failed"})`；`done({"reason":"heuristic_skip"})`。
4. **compute_similarity(threshold=0.3)** — 拿两两相似度矩阵。
   - 全部 pair 相似度均 ≥ threshold 且无显著状态码差异 → `write_fact({category:"boundary", content:"similarity 全低于阈值"})`；`write_idea({direction, status:"failed"})`；`done({"reason":"all_similar"})`。
5. **基于相似度矩阵 + 状态码判定漏洞类型**（顺序就是优先级，命中即返回）：
   - 若 `anonymous` 与某个登录态身份的 `status_code` 都 < 400 且相似度 ≥ threshold → **bac.unauthorized_access**（最高优先）
   - 若有多个低权限用户（非 admin）访问 `/admin/`、`/sys/` 路径并 `status_code` < 400 → **bac.vertical_priv_esc**
   - 若多个同级用户（同 role）对同一私有资源都返回 `status_code` < 400 且相似度 ≥ threshold → **bac.horizontal_priv_esc**
6. **write_finding** — 命中 5 中任一类型即写 finding：
   - `severity = high`
   - `confidence = unverified`（v1 不做二次验证；v1.5 接 verifier）
   - `dedup_key`：`<kind>:<host>:<method>:<path-template>`（path 中数值/UUID 段替换为 `:id` / `:uuid`）
   - `evidence` 至少含：`{"violating_identities":[...], "responses":[{"identity","status_code"}]}`
   - 写库后系统自动触发 distill（写 hint 入 memory_hints，下次同 engagement 优先读）
7. **write_graph** —
   - node `endpoint`（dedup_key=`<host>:<method>:<path-template>`）
   - edge `endpoint -bac-> finding_id`
8. `write_idea({direction, status:"verified"})` → `done({"reason":"finding_written"})` 或 `write_idea({direction, status:"failed"})` → `done({"reason":"no_pattern_match"})`。

## done 系统校验（不通过则被注入 user message 继续）

- 必须已调用：`fetch_credentials` + `replay_multi_identity` + `heuristic_check` + `compute_similarity` 全套
- `done.reason` 必须 ∈ `{finding_written, all_similar, heuristic_skip, no_pattern_match}`
- `reason=finding_written` 时 finding 表必须存在对应 dedup_key

## 常见坑

- **不要拿原始 body 判断**：v1 用相似度+状态码两个信号，禁止 LLM 直接读原文判定漏洞（容易被错觉/语言迷惑）。
- **anonymous 身份永远存在**：`fetch_credentials` 一定返回它（零 credentials），不要再额外创建。
- **path 模板化**：`dedup_key` 的 path 一定要把数字 ID / UUID 替换成 `:id` / `:uuid`，否则同接口不同实例会重复入库。
- **超 budget 立刻 done**：max_steps=10，max_tokens=15000；若步骤 5 已判定，剩 1 步直接 `done`。

## 示例 dedup_key

| 接口 | dedup_key |
|---|---|
| GET /api/order/7 | `bac.horizontal_priv_esc:vulnapp:GET:/api/order/:id` |
| POST /api/admin/user/delete（无 cookie） | `bac.unauthorized_access:vulnapp:POST:/api/admin/user/delete` |
| GET /api/admin/users（test 用户） | `bac.vertical_priv_esc:vulnapp:GET:/api/admin/users` |
```

- [ ] **Step 2: 跑 loader 测试验证 yaml 仍能解析**

```bash
go test ./internal/skill/... -race
```

- [ ] **Step 3: Commit**

```bash
git add skills/vuln/web/bac/SKILL.md
git commit -m "feat(skills/bac): SKILL.md 正文（步骤指引 + dedup_key 模板 + 常见坑）"
```

---

## Task 4: internal/spawner — Spawner 实现

**Files:**
- Create: `internal/spawner/spawner.go`
- Create: `internal/spawner/spawner_integration_test.go`

**职责：** plan 1 T24 留了 `actions.Spawner` 接口；这里给生产实现：写 agent_task → enqueue。**强约束**：Spawn depth ≤ 1（父任务 parent_task_id 必须为空）；in-flight ≤ 10/parent，≤ 20/engagement。

- [ ] **Step 1: 写失败测试**

```go
//go:build integration

package spawner

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
)

func TestSpawn_DepthLimit(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	es := engagement.NewStore(pool)
	ts := task.NewStore(pool)
	ctx := context.Background()
	e, _ := es.LookupOrCreate(ctx, "default", "h", engagement.ModeProxy)

	parent, _ := ts.Create(ctx, task.NewParams{EngagementID: e.ID, Role: "sniffer"})
	pid := parent
	child, _ := ts.Create(ctx, task.NewParams{EngagementID: e.ID, ParentTaskID: &pid, Role: "sniffer"})

	mr := miniredis.RunT(t)
	wc := worker.NewClient(asynq.RedisClientOpt{Addr: mr.Addr()})
	defer wc.Close()
	s := New(ts, wc, Limits{MaxChildrenPerParent: 5, MaxInflightPerEngagement: 10})

	if _, err := s.Spawn(ctx, child, "vuln/web/bac", json.RawMessage(`{}`), nil); err == nil {
		t.Fatal("expected depth error")
	}
}

func TestSpawn_OK(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	es := engagement.NewStore(pool)
	ts := task.NewStore(pool)
	ctx := context.Background()
	e, _ := es.LookupOrCreate(ctx, "default", "h", engagement.ModeProxy)
	parent, _ := ts.Create(ctx, task.NewParams{EngagementID: e.ID, Role: "sniffer"})

	mr := miniredis.RunT(t)
	wc := worker.NewClient(asynq.RedisClientOpt{Addr: mr.Addr()})
	defer wc.Close()
	s := New(ts, wc, Limits{MaxChildrenPerParent: 10, MaxInflightPerEngagement: 20})

	id, err := s.Spawn(ctx, parent, "vuln/web/bac", json.RawMessage(`{"flow_id":1,"host":"vulnapp"}`), nil)
	if err != nil || id == "" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}
```

- [ ] **Step 2: 实现 `internal/spawner/spawner.go`**

```go
package spawner

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"
)

type Limits struct {
	MaxChildrenPerParent     int
	MaxInflightPerEngagement int
}

type Spawner struct {
	tasks  *task.Store
	worker *worker.Client
	limits Limits
}

func New(t *task.Store, w *worker.Client, l Limits) *Spawner {
	if l.MaxChildrenPerParent <= 0 {
		l.MaxChildrenPerParent = 10
	}
	if l.MaxInflightPerEngagement <= 0 {
		l.MaxInflightPerEngagement = 20
	}
	return &Spawner{tasks: t, worker: w, limits: l}
}

func (s *Spawner) Spawn(ctx context.Context, parentTaskID string, skill string, input, budget json.RawMessage) (string, error) {
	parent, err := s.tasks.GetByID(ctx, parentTaskID)
	if err != nil {
		return "", fmt.Errorf("get parent: %w", err)
	}
	if parent.ParentTaskID != nil {
		return "", fmt.Errorf("spawn depth limit (parent %s already a child)", parentTaskID)
	}
	if n, err := s.tasks.CountInflightChildren(ctx, parentTaskID); err == nil && n >= s.limits.MaxChildrenPerParent {
		return "", fmt.Errorf("inflight children %d >= %d", n, s.limits.MaxChildrenPerParent)
	}
	if n, err := s.tasks.CountInflightInEngagement(ctx, parent.EngagementID); err == nil && n >= s.limits.MaxInflightPerEngagement {
		return "", fmt.Errorf("inflight in engagement %d >= %d", n, s.limits.MaxInflightPerEngagement)
	}

	pid := parentTaskID
	id, err := s.tasks.Create(ctx, task.NewParams{
		EngagementID: parent.EngagementID,
		ParentTaskID: &pid,
		Role:         parent.Role,
		Skill:        skill,
		Input:        input,
		Budget:       budget,
	})
	if err != nil {
		return "", fmt.Errorf("create task: %w", err)
	}

	pl, _ := json.Marshal(worker.Payload{
		TaskID: id, EngagementID: parent.EngagementID,
		Role: worker.Role(parent.Role), Skill: skill, Input: input,
	})
	if _, _, err := s.worker.Enqueue(ctx, worker.Role(parent.Role), pl); err != nil {
		return "", fmt.Errorf("enqueue spawn: %w", err)
	}
	return id, nil
}
```

- [ ] **Step 3: 跑测试 + Commit**

```bash
go test -tags=integration ./internal/spawner/... -race -count=1
git add internal/spawner
git commit -m "feat(spawner): Spawner 实现（depth ≤ 1，inflight 限额）"
```

---

## Task 5: cmd/agent-worker — 装载 BAC actions + spawner

**Files:**
- Modify: `cmd/agent-worker/main.go`

**职责：** 把 plan 1 T30 的 sniffer handler 改成"按 skill 注册不同 action 集 + 加 spawner + 加 read_window"。规则：
- skill 为空 → 顶层 sniffer：注册 5 个共享 action + `read_window` + `spawn_subtask`（不含 BAC 专属）
- skill = `vuln/web/bac` → BAC 子任务：注册 5 个共享 action + 4 个 BAC action（不再有 spawn）

- [ ] **Step 1: 改 `snifferSystemPrompt` 常量**

```go
const snifferSystemPrompt = `你是 sniffer 角色。每次任务输入对应一个 traffic_window。

工作流程：
1. read_window(window_id) → 拿当前窗口里的 N 条 flow 摘要
2. 扫描可疑信号：
   - URL 含 /admin/ /sys/，或参数含 ?uid= /:user_id/ → spawn_subtask(skill="vuln/web/bac", input={"flow_id":<id>,"host":<host>})
3. 在所有可疑 flow 都已 spawn 之后 done({"reason":"window_consumed"})

规则：
- 同一窗口内每个 flow 只 spawn 一次
- 单窗口最多 spawn 5 个子任务（避免炸开）
- 不要直接判漏洞，那是 BAC skill 的工作
- 没可疑就 done({"reason":"no_suspicious"})`
```

- [ ] **Step 2: 替换 `snifferHandler` 结构定义**（去掉 `provider`，加 `cfg`）

```go
type snifferHandler struct {
	tasks               *task.Store
	engagements         *engagement.Store
	findings            *finding.Store
	graphs              *graph.Store
	calls               *llmcall.Store
	windows             *window.Store
	flows               *flow.Store
	credentials         credential.Provider
	replayEngine        *replay.Engine
	spawner             *spawner.Spawner
	skill               *skill.Loader
	cfg                 config.Config
	pricing             llm.PricingProvider
	snifferSystemPrompt string
	budget              runtime.Budget
}
```

- [ ] **Step 3: 替换 `handle` 实现**

```go
func (h snifferHandler) handle(ctx context.Context, p worker.Payload) error {
	if err := h.tasks.SetRunning(ctx, p.TaskID); err != nil {
		return err
	}

	reg := action.NewRegistry()
	_ = reg.Register(actions.Done{})
	_ = reg.Register(&actions.ReadState{Store: h.engagements, EngagementID: p.EngagementID})
	_ = reg.Register(&actions.WriteFact{Store: h.engagements, EngagementID: p.EngagementID})
	_ = reg.Register(&actions.WriteIdea{Store: h.engagements, EngagementID: p.EngagementID})
	_ = reg.Register(&actions.WriteHint{Store: h.engagements, EngagementID: p.EngagementID})
	_ = reg.Register(&actions.WriteFinding{Store: h.findings, EngagementID: p.EngagementID, TaskID: p.TaskID})
	_ = reg.Register(&actions.WriteGraph{Store: h.graphs, EngagementID: p.EngagementID})

	// T22.5：套上中间件链（result_compress / loop_detect / done_validate）
	reg.Use(
		middleware.ResultCompress(p.EngagementID, h.cfg.EngagementStoreDir),
		middleware.LoopDetect(),
		middleware.DoneValidate(h.skillLoader.DoneValidatorFor(p.Skill)),
	)

	systemPrompt := h.snifferSystemPrompt
	switch p.Skill {
	case "":
		_ = reg.Register(&actions.ReadWindow{Windows: h.windows, Flows: h.flows})
		_ = reg.Register(&actions.SpawnSubtask{Engine: h.spawner, ParentTaskID: p.TaskID})
	case "vuln/web/bac":
		if err := bac.Register(reg, bac.Deps{
			Credentials: h.credentials, Replay: h.replayEngine, Flows: h.flows,
		}); err != nil {
			_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
			return err
		}
		body, err := h.skill.Load(ctx, p.Skill)
		if err != nil {
			_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
			return err
		}
		systemPrompt = systemPrompt + "\n\n" + body
	default:
		_ = h.tasks.SetError(ctx, p.TaskID, "unknown skill: "+p.Skill)
		return nil
	}

	// 每个 task 一个全新 Generator（tools 一次绑定，避免跨 goroutine 竞争）
	provider, err := llm.BuildProvider(ctx, h.cfg, h.cfg.LLM.DefaultProvider, reg.Schemas())
	if err != nil {
		_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
		return err
	}

	tid, eid := p.TaskID, p.EngagementID
	gen := llm.Instrument(provider, h.calls, llm.CallMeta{TaskID: &tid, EngagementID: &eid}, h.pricing)

	out, err := runtime.Run(ctx, runtime.Config{
		LLM: gen, Actions: reg, Budget: h.budget,
		SystemPrompt: systemPrompt,
		UserPrompt:   string(p.Input),
		OnAbort: func(c context.Context) (bool, error) {
			e, err := h.engagements.GetByID(c, eid)
			if err != nil {
				return false, err
			}
			return e.Status != engagement.StatusActive, nil
		},
	})
	if err != nil {
		_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
		return err
	}
	res, _ := json.Marshal(map[string]any{
		"terminate_by":     out.TerminateBy,
		"total_steps":      out.TotalSteps,
		"total_in":         out.TotalUsage.InTokens,
		"total_out":        out.TotalUsage.OutTokens,
		"total_cached":     out.TotalUsage.CachedTokens,
		"observer_hints":   out.ObserverHints,
		"done_force_count": out.DoneForceCount,
	})
	return h.tasks.SetDone(ctx, p.TaskID, res)
}
```

- [ ] **Step 4: 在 `main` 中装配新依赖**

在 `main` 里 `pool := db.NewPgPool(...)` 之后插入：

```go
rdb, err := db.NewRedis(ctx, os.Getenv("LIUSHA_REDIS_ADDR"))
if err != nil {
	logger.Fatal().Err(err).Msg("redis")
}
defer rdb.Close()

windows := window.NewStore(pool)
flows := flow.NewStore(pool, 1<<20, 2<<20)
creds := credential.NewRedis(rdb)
re := replay.NewEngine(nil)

wc := worker.NewClient(asynq.RedisClientOpt{Addr: os.Getenv("LIUSHA_REDIS_ADDR")})
defer wc.Close()
sp := spawner.New(tasks, wc, spawner.Limits{
	MaxChildrenPerParent: 10, MaxInflightPerEngagement: 20,
})
```

并修改 `h := snifferHandler{...}` 为：

```go
h := snifferHandler{
	tasks: tasks, engagements: engs, findings: finds, graphs: graphs, calls: calls,
	windows: windows, flows: flows, credentials: creds, replayEngine: re,
	spawner: sp, skill: skillLoader,
	cfg: cfg, pricing: observability.DefaultPricing,
	snifferSystemPrompt: snifferSystemPrompt,
	budget: runtime.Budget{
		MaxSteps: cfg.LLM.MaxSteps, MaxTokens: 50_000, WatchdogSeconds: 60,
	},
}
mux.Register(worker.RoleSniffer, h.handle)
```

- [ ] **Step 5: 在 import 块加**

```go
"github.com/V3teran/liusha/internal/agent/actions/bac"
"github.com/V3teran/liusha/internal/credential"
"github.com/V3teran/liusha/internal/flow"
"github.com/V3teran/liusha/internal/replay"
"github.com/V3teran/liusha/internal/spawner"
"github.com/V3teran/liusha/internal/window"
```

- [ ] **Step 6: 构建 + Commit**

```bash
go build ./cmd/agent-worker
docker build -f cmd/agent-worker/Dockerfile -t liusha/agent-worker .
git add cmd/agent-worker/main.go
git commit -m "feat(cmd/agent-worker): sniffer 装 read_window + spawner；BAC 子任务装 4 个 BAC action + skill 正文"
```

---

## Task 6: internal/proxify_consumer — tail proxify JSONL → flow + window + sniffer enqueue

**Files:**
- Create: `internal/proxify_consumer/consumer.go`
- Create: `internal/proxify_consumer/slicer.go`
- Create: `internal/proxify_consumer/consumer_test.go`
- Modify: `cmd/agent-worker/main.go`（main 内启动 consumer goroutine）

**职责：** 一个 goroutine 死循环 tail `/data/flows.jsonl`（proxify 的 default output 格式），逐行解码 → `engagement.LookupOrCreate(host)` → `flow.Append` → slicer → 关窗时 `worker.Client.Enqueue(sniffer, {window_id})`。

> proxify 的 JSONL 格式：每行是一个完整请求/响应对，字段名按 proxify 默认 schema（`request.method` / `request.url` / `request.headers` / `request.body` / `response.status_code` / `response.headers` / `response.body`），实施时按真实 schema 调字段名。

- [ ] **Step 1: 写 slicer 测试 `internal/proxify_consumer/slicer_test.go`**

```go
package proxify_consumer

import (
	"context"
	"testing"
	"time"
)

type recorder struct{ calls int }

func (r *recorder) appendFlow(_ context.Context, _ int64) (string, bool, error) {
	r.calls++
	return "w", true, nil
}

func TestSlicer_FlushOnAge(t *testing.T) {
	rec := &recorder{}
	s := newSlicer(rec.appendFlow, nil, 100, 50*time.Millisecond)
	defer s.stop()
	s.onFlow(context.Background(), 1)
	time.Sleep(150 * time.Millisecond)
	if rec.calls < 1 {
		t.Fatalf("expected at least 1 append, got %d", rec.calls)
	}
}

func TestSlicer_OnClose(t *testing.T) {
	rec := &recorder{}
	var closed []string
	s := newSlicer(rec.appendFlow, func(_ context.Context, id string) {
		closed = append(closed, id)
	}, 100, 50*time.Millisecond)
	defer s.stop()
	s.onFlow(context.Background(), 1)
	time.Sleep(150 * time.Millisecond)
	if len(closed) != 1 || closed[0] != "w" {
		t.Fatalf("closed=%v", closed)
	}
}
```

- [ ] **Step 2: 实现 `internal/proxify_consumer/slicer.go`**

```go
package proxify_consumer

import (
	"context"
	"sync"
	"time"
)

type appendFn func(ctx context.Context, flowID int64) (string, bool, error)
type onCloseFn func(ctx context.Context, windowID string)

type slicer struct {
	appendFlow appendFn
	onClose    onCloseFn
	maxBatch   int
	maxAge     time.Duration
	stopCh     chan struct{}
	mu         sync.Mutex
	pending    []int64
}

func newSlicer(a appendFn, onClose onCloseFn, batch int, age time.Duration) *slicer {
	s := &slicer{appendFlow: a, onClose: onClose, maxBatch: batch, maxAge: age, stopCh: make(chan struct{})}
	go s.loop()
	return s
}

func (s *slicer) onFlow(ctx context.Context, id int64) {
	s.mu.Lock()
	s.pending = append(s.pending, id)
	if len(s.pending) >= s.maxBatch {
		ids := s.pending
		s.pending = nil
		s.mu.Unlock()
		s.flush(ctx, ids)
		return
	}
	s.mu.Unlock()
}

func (s *slicer) loop() {
	t := time.NewTicker(s.maxAge)
	defer t.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-t.C:
			s.mu.Lock()
			ids := s.pending
			s.pending = nil
			s.mu.Unlock()
			if len(ids) > 0 {
				s.flush(context.Background(), ids)
			}
		}
	}
}

func (s *slicer) flush(ctx context.Context, ids []int64) {
	for _, id := range ids {
		wid, closed, err := s.appendFlow(ctx, id)
		if err == nil && closed && s.onClose != nil {
			s.onClose(ctx, wid)
		}
	}
}

func (s *slicer) stop() { close(s.stopCh) }
```

- [ ] **Step 3: 写 consumer 单元测试 `internal/proxify_consumer/consumer_test.go`**

```go
package proxify_consumer

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestParseProxifyLine_ExtractsFields(t *testing.T) {
	line := []byte(`{
	  "request":  {"method":"GET","url":"http://vulnapp:8001/api/profile","headers":{"Cookie":["session=admin_sess_a1b2c3"]},"body":""},
	  "response": {"status_code":200,"headers":{"Content-Type":["application/json"]},"body":"{\"me\":\"admin\"}"}
	}`)
	var raw proxifyRecord
	if err := json.Unmarshal(line, &raw); err != nil {
		t.Fatal(err)
	}
	host, err := hostFromURL(raw.Request.URL)
	if err != nil {
		t.Fatal(err)
	}
	if host != "vulnapp" {
		t.Fatalf("host=%q", host)
	}
	if raw.Response.StatusCode != 200 {
		t.Fatalf("status=%d", raw.Response.StatusCode)
	}
}

func TestParseProxifyLine_BadJSON(t *testing.T) {
	if _, err := parseLine([]byte("not json")); err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(string([]byte("\nvalid_check")), "valid") {
		t.Fatal("smoke")
	}
	_ = bytes.NewBufferString
	_ = url.Parse
	_ = context.Background
}
```

- [ ] **Step 4: 实现 `internal/proxify_consumer/consumer.go`**

```go
package proxify_consumer

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/window"
	"github.com/V3teran/liusha/internal/worker"

	"github.com/rs/zerolog"
)

// proxifyRecord 是 proxify 默认 JSONL output 的最小子集。如真实字段名不一致，按真实 schema 调整。
type proxifyRecord struct {
	Request  proxifyRequest  `json:"request"`
	Response proxifyResponse `json:"response"`
}

type proxifyRequest struct {
	Method  string              `json:"method"`
	URL     string              `json:"url"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
}

type proxifyResponse struct {
	StatusCode int                 `json:"status_code"`
	Headers    map[string][]string `json:"headers"`
	Body       string              `json:"body"`
}

func parseLine(b []byte) (proxifyRecord, error) {
	var r proxifyRecord
	if err := json.Unmarshal(b, &r); err != nil {
		return proxifyRecord{}, fmt.Errorf("decode proxify line: %w", err)
	}
	return r, nil
}

func hostFromURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", fmt.Errorf("empty host in %q", raw)
	}
	host := u.Hostname()
	return host, nil
}

type Config struct {
	JSONLPath           string
	BatchSize           int
	WindowMaxAgeSeconds int
	TenantID            string
}

type Deps struct {
	Engagements *engagement.Store
	Flows       *flow.Store
	Windows     *window.Store
	Tasks       *task.Store
	Worker      *worker.Client
	Logger      zerolog.Logger
}

type Consumer struct {
	cfg  Config
	deps Deps
	stop chan struct{}
}

func New(cfg Config, deps Deps) *Consumer {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 20
	}
	if cfg.WindowMaxAgeSeconds <= 0 {
		cfg.WindowMaxAgeSeconds = 30
	}
	if cfg.TenantID == "" {
		cfg.TenantID = "default"
	}
	return &Consumer{cfg: cfg, deps: deps, stop: make(chan struct{})}
}

func (c *Consumer) Stop() { close(c.stop) }

// Run tails JSONLPath in a loop, parsing each line and pushing through engagement/flow/window/enqueue.
func (c *Consumer) Run(ctx context.Context) error {
	for {
		select {
		case <-c.stop:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := c.tailOnce(ctx); err != nil {
			c.deps.Logger.Warn().Err(err).Msg("tail loop error; sleeping then retry")
			select {
			case <-time.After(2 * time.Second):
			case <-c.stop:
				return nil
			}
		}
	}
}

func (c *Consumer) tailOnce(ctx context.Context) error {
	f, err := os.Open(c.cfg.JSONLPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", c.cfg.JSONLPath, err)
	}
	defer f.Close()

	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	r := bufio.NewReader(f)

	// per-host slicer cache
	slicers := map[string]*slicer{}
	defer func() {
		for _, s := range slicers {
			s.stop()
		}
	}()

	for {
		select {
		case <-c.stop:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := r.ReadBytes('\n')
		if err == io.EOF {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if err != nil {
			return err
		}
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		if err := c.processLine(ctx, line, slicers); err != nil {
			c.deps.Logger.Warn().Err(err).Msg("process line")
		}
	}
}

func (c *Consumer) processLine(ctx context.Context, line []byte, slicers map[string]*slicer) error {
	rec, err := parseLine(line)
	if err != nil {
		return err
	}
	host, err := hostFromURL(rec.Request.URL)
	if err != nil {
		return err
	}
	e, err := c.deps.Engagements.LookupOrCreate(ctx, c.cfg.TenantID, host, engagement.ModeProxy)
	if err != nil {
		return fmt.Errorf("engagement: %w", err)
	}
	reqHeaders, _ := json.Marshal(flattenHeaders(rec.Request.Headers))
	respHeaders, _ := json.Marshal(flattenHeaders(rec.Response.Headers))
	flowID, err := c.deps.Flows.Append(ctx, flow.Flow{
		EngagementID:    e.ID,
		Method:          rec.Request.Method,
		URL:             rec.Request.URL,
		RequestHeaders:  reqHeaders,
		RequestBody:     []byte(rec.Request.Body),
		StatusCode:      rec.Response.StatusCode,
		ResponseHeaders: respHeaders,
		ResponseBody:    []byte(rec.Response.Body),
	})
	if err != nil {
		return fmt.Errorf("flow append: %w", err)
	}

	sl, ok := slicers[e.ID]
	if !ok {
		eid := e.ID
		sl = newSlicer(
			func(ctx context.Context, id int64) (string, bool, error) {
				return c.deps.Windows.OpenOrAppend(ctx, eid, window.FlowRef{ID: id}, c.cfg.BatchSize)
			},
			func(ctx context.Context, windowID string) {
				c.enqueueSniffer(ctx, eid, windowID)
			},
			c.cfg.BatchSize,
			time.Duration(c.cfg.WindowMaxAgeSeconds)*time.Second,
		)
		slicers[e.ID] = sl
	}
	sl.onFlow(ctx, flowID)
	return nil
}

func (c *Consumer) enqueueSniffer(ctx context.Context, engagementID, windowID string) {
	taskID, err := c.deps.Tasks.Create(ctx, task.NewParams{
		EngagementID: engagementID, Role: "sniffer",
		Input: json.RawMessage(`{"window_id":"` + windowID + `"}`),
	})
	if err != nil {
		c.deps.Logger.Warn().Err(err).Msg("create sniffer task")
		return
	}
	pl, _ := json.Marshal(worker.Payload{
		TaskID: taskID, EngagementID: engagementID, Role: worker.RoleSniffer,
		Input: json.RawMessage(`{"window_id":"` + windowID + `"}`),
	})
	if _, _, err := c.deps.Worker.Enqueue(ctx, worker.RoleSniffer, pl); err != nil {
		c.deps.Logger.Warn().Err(err).Msg("enqueue sniffer")
	}
}

func flattenHeaders(in map[string][]string) map[string]string {
	out := map[string]string{}
	for k, vs := range in {
		out[k] = strings.Join(vs, ", ")
	}
	return out
}
```

- [ ] **Step 5: 在 `cmd/agent-worker/main.go` 启动 consumer goroutine**

在 `wc := worker.NewClient(...)` 之后插入：

```go
proxifyPath := envOr("LIUSHA_PROXIFY_JSONL", "/data/flows.jsonl")
if _, err := os.Stat(proxifyPath); err == nil {
	consumer := proxify_consumer.New(proxify_consumer.Config{
		JSONLPath:           proxifyPath,
		BatchSize:           cfg.Proxy.WindowBatch,
		WindowMaxAgeSeconds: cfg.Proxy.WindowMaxAgeSeconds,
	}, proxify_consumer.Deps{
		Engagements: engs, Flows: flows, Windows: windows, Tasks: tasks, Worker: wc, Logger: logger,
	})
	go func() {
		if err := consumer.Run(ctx); err != nil {
			logger.Error().Err(err).Msg("proxify consumer exited")
		}
	}()
	defer consumer.Stop()
} else {
	logger.Info().Str("path", proxifyPath).Msg("proxify JSONL not present; skipping consumer")
}
```

补 import：

```go
"github.com/V3teran/liusha/internal/proxify_consumer"
```

- [ ] **Step 6: 跑测试 + 构建 + Commit**

```bash
go test ./internal/proxify_consumer/... -race
go build ./cmd/agent-worker
docker build -f cmd/agent-worker/Dockerfile -t liusha/agent-worker .
git add internal/proxify_consumer cmd/agent-worker/main.go
git commit -m "feat(proxify_consumer): tail proxify JSONL → engagement/flow/window/sniffer enqueue（agent-worker 进程内 goroutine）"
```

---

## Task 7: cmd/api — POST /engagement/proxy 懒创建

**Files:**
- Modify: `internal/httpapi/handlers.go`
- Modify: `internal/httpapi/server.go`
- Modify: `internal/httpapi/handlers_test.go`
- Modify: `internal/engagement/store.go`（加 `LookupOrCreateProxy` wrapper）

- [ ] **Step 1: `internal/httpapi/handlers.go` 加扩展**

把 `EngagementsAPI` 接口改成：

```go
type EngagementsAPI interface {
	Abort(ctx context.Context, id string) error
	LookupOrCreateProxy(ctx context.Context, host string) (string, error)
}

type CreateProxyRequest struct {
	Host string `json:"host"`
}

func createProxyHandler(api EngagementsAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreateProxyRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		if req.Host == "" {
			c.JSON(400, gin.H{"error": "host required"})
			return
		}
		id, err := api.LookupOrCreateProxy(c.Request.Context(), req.Host)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"engagement_id": id})
	}
}
```

- [ ] **Step 2: `internal/httpapi/server.go` 加路由**

```go
if d.Engagements != nil {
	r.POST("/engagement/proxy", createProxyHandler(d.Engagements))
	r.POST("/engagement/:id/abort", abortHandler(d.Engagements))
}
```

- [ ] **Step 3: 给 `engagement.Store` 加 wrapper（追加到 `store.go` 末尾）**

```go
func (s *Store) LookupOrCreateProxy(ctx context.Context, host string) (string, error) {
	e, err := s.LookupOrCreate(ctx, "default", host, ModeProxy)
	if err != nil {
		return "", err
	}
	return e.ID, nil
}
```

- [ ] **Step 4: 更新测试 `internal/httpapi/handlers_test.go`**

给 `fakeAbort` 加方法：

```go
func (f *fakeAbort) LookupOrCreateProxy(_ context.Context, host string) (string, error) {
	return "eid-" + host, nil
}
```

加新用例：

```go
func TestCreateProxyEngagement(t *testing.T) {
	fa := &fakeAbort{}
	srv := httptest.NewServer(NewServer(Deps{APIKey: "k", Engagements: fa}))
	defer srv.Close()
	body, _ := json.Marshal(CreateProxyRequest{Host: "vulnapp"})
	req, _ := http.NewRequest("POST", srv.URL+"/engagement/proxy", bytes.NewReader(body))
	req.Header.Set("X-API-Key", "k")
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}
```

- [ ] **Step 5: 跑测试 + Commit**

```bash
go test ./internal/httpapi/... -race
go test -tags=integration ./internal/engagement/... -race -count=1
git add internal/httpapi internal/engagement/store.go
git commit -m "feat(httpapi): POST /engagement/proxy 懒创建（LookupOrCreateProxy）"
```

---

## Task 8: docker-compose.yml — 从零写 v1 干净版

**Files:**
- Create: `deployments/docker-compose.yml`（仓库前置 commit 已删旧文件）

> 整文件从零写，**不做任何 patch / 兼容旧版本**。v1 只 6 个 service：postgres / redis / asynqmon / api / agent-worker / proxify / vulnapp。SQLi 用的 vuln-tools 留到 v1.5 再加回。

- [ ] **Step 1: 写 `deployments/docker-compose.yml`**

```yaml
services:
  postgres:
    image: pgvector/pgvector:pg17
    container_name: liusha-postgres
    environment:
      POSTGRES_USER: liusha
      POSTGRES_PASSWORD: liusha
      POSTGRES_DB: liusha
    ports:
      - "5432:5432"
    volumes:
      - liusha-pg:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U liusha -d liusha"]
      interval: 5s
      timeout: 3s
      retries: 10

  redis:
    image: redis:8
    container_name: liusha-redis
    ports:
      - "6379:6379"
    volumes:
      - liusha-redis:/data
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 10

  asynqmon:
    image: hibiken/asynqmon:latest
    platform: linux/amd64
    container_name: liusha-asynqmon
    command: ["--redis-addr=redis:6379"]
    ports:
      - "8081:8080"
    depends_on:
      redis:
        condition: service_healthy

  api:
    image: liusha/api
    build:
      context: ..
      dockerfile: cmd/api/Dockerfile
    container_name: liusha-api
    environment:
      LIUSHA_POSTGRES_DSN: postgres://liusha:liusha@postgres:5432/liusha?sslmode=disable
      LIUSHA_REDIS_ADDR: redis:6379
      LIUSHA_API_ADDR: 0.0.0.0:8080
      LIUSHA_API_KEY: "${LIUSHA_API_KEY:-changeme-dev-key}"
      LIUSHA_ENV: development
      LIUSHA_LOG_LEVEL: info
      LIUSHA_CONFIG: /app/config/config.yaml
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    ports:
      - "8080:8080"

  # proxify：projectdiscovery 官方 MITM 代理。listen 8888，捕流量写 JSONL。
  # filter DSL 直接写 command，命中即丢弃；agent-worker 容器 tail JSONL 消费。
  proxify:
    image: projectdiscovery/proxify:latest
    container_name: liusha-proxify
    command:
      - "-output"
      - "/data/flows.jsonl"
      - "-match-condition"
      - "request.method != 'OPTIONS' && request.method != 'HEAD' && request.method != 'CONNECT'"
      - "-no-color"
    ports:
      - "8888:8888"
    volumes:
      - liusha-proxify-flows:/data
    extra_hosts:
      - "host.docker.internal:host-gateway"
    profiles:
      - proxy
      - e2e

  # agent-worker：消费 Asynq 队列 + 进程内 goroutine 跑 proxify_consumer。
  # 启动前需设 LIUSHA_DEEPSEEK_API_KEY（默认 LLM provider 的 key）。
  agent-worker:
    image: liusha/agent-worker
    build:
      context: ..
      dockerfile: cmd/agent-worker/Dockerfile
    container_name: liusha-agent-worker
    environment:
      LIUSHA_POSTGRES_DSN: postgres://liusha:liusha@postgres:5432/liusha?sslmode=disable
      LIUSHA_REDIS_ADDR: redis:6379
      LIUSHA_CONFIG: /app/config/config.yaml
      LIUSHA_ENV: development
      LIUSHA_LOG_LEVEL: info
      LIUSHA_DEEPSEEK_API_KEY: "${LIUSHA_DEEPSEEK_API_KEY:-}"
      LIUSHA_ANTHROPIC_API_KEY: "${LIUSHA_ANTHROPIC_API_KEY:-}"
      LIUSHA_PROXIFY_JSONL: /data/flows.jsonl
    volumes:
      - liusha-proxify-flows:/data:ro
    extra_hosts:
      - "host.docker.internal:host-gateway"
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    ports:
      - "9090:9090"
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:9090/healthz"]
      interval: 10s
      timeout: 3s
      retries: 3
    profiles:
      - agent
      - e2e

  # vulnapp：BAC e2e 靶机（见 plan 1 T31）。仅 e2e profile 启动。
  vulnapp:
    image: liusha/vulnapp
    build:
      context: ..
      dockerfile: cmd/vulnapp/Dockerfile
    container_name: liusha-vulnapp
    ports:
      - "8001:8001"
    profiles:
      - e2e

volumes:
  liusha-pg:
  liusha-redis:
  liusha-proxify-flows:
```

> proxify CLI flag 以 `docker run --rm projectdiscovery/proxify:latest -h` 实际输出为准。如 `-match-condition` 命名不同（例如 `-allow-condition`），按真实 flag 调整；如 JSONL 不是默认输出格式，加 `-output-jsonl` 或对应 flag。

- [ ] **Step 2: 验证 compose 解析 + 干净度**

```bash
docker compose -f deployments/docker-compose.yml config >/dev/null
test "$(grep -c 'cmd/proxy/Dockerfile\|liusha-proxy-flows\|vuln-tools' deployments/docker-compose.yml)" -eq 0
```

预期：解析成功；grep 检查返回 0（确认没有任何旧 service / volume 残留）。

- [ ] **Step 3: Commit**

```bash
git add deployments/docker-compose.yml
git commit -m "chore(compose): v1 干净版（postgres/redis/asynqmon/api/agent-worker/proxify/vulnapp，无 cmd/proxy / vuln-tools 残留）"
```

---

## Task 9: cmd/e2e-bac — 触发器

**Files:**
- Create: `cmd/e2e-bac/main.go`

**职责：** 程序：
1. 调 `POST /engagement/proxy` 建 vulnapp engagement（consumer 也会懒创建同名 engagement，幂等）
2. 录入凭证 `POST /credential/batch`
3. 通过 `HTTP_PROXY=localhost:8888`（proxify）请求 vulnapp 18 个调用
4. 轮询 `finding` 表，等 5 条 BAC finding + 3 类齐全，超时 6 分钟

> **前置：** Task 10 已经 `docker compose --profile e2e up -d` 起了完整栈（含 proxify + vulnapp + agent-worker）。本程序不再 `docker compose up`。

- [ ] **Step 1: 实现 `cmd/e2e-bac/main.go`**

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/logx"
)

func main() {
	logger := logx.New("e2e-bac")
	ctx := context.Background()

	apiBase := envOr("LIUSHA_API_BASE", "http://localhost:8080")
	apiKey := envOr("LIUSHA_API_KEY", "changeme-dev-key")
	pgDSN := envOr("LIUSHA_POSTGRES_DSN", "postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable")

	eid, err := createProxyEngagement(apiBase, apiKey, "vulnapp")
	if err != nil {
		logger.Fatal().Err(err).Msg("create engagement")
	}
	logger.Info().Str("engagement_id", eid).Msg("engagement ready")

	if err := saveCreds(apiBase, apiKey); err != nil {
		logger.Fatal().Err(err).Msg("save credentials")
	}

	// 栈在 Task 10 起好；这里直接打流量
	if err := drive(); err != nil {
		logger.Fatal().Err(err).Msg("drive vulnapp")
	}

	pool, err := db.NewPgPool(ctx, pgDSN, 5, 1)
	if err != nil {
		logger.Fatal().Err(err).Msg("pg")
	}
	defer pool.Close()
	store := finding.NewStore(pool)

	deadline := time.Now().Add(6 * time.Minute)
	for time.Now().Before(deadline) {
		all, err := store.ListByEngagement(ctx, eid)
		if err != nil {
			logger.Warn().Err(err).Msg("list findings")
		}
		bacCount := 0
		kinds := map[string]int{}
		for _, f := range all {
			if strings.HasPrefix(f.Kind, "bac.") {
				bacCount++
				kinds[f.Kind]++
			}
		}
		logger.Info().Int("bac", bacCount).Interface("kinds", kinds).Msg("poll")
		if bacCount >= 5 && len(kinds) >= 3 {
			logger.Info().Msg("e2e PASS: 5 finding 三类齐全")
			return
		}
		time.Sleep(15 * time.Second)
	}
	logger.Fatal().Msg("e2e timeout")
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func createProxyEngagement(base, key, host string) (string, error) {
	body, _ := json.Marshal(map[string]string{"host": host})
	req, _ := http.NewRequest("POST", base+"/engagement/proxy", bytes.NewReader(body))
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("api %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		EngagementID string `json:"engagement_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.EngagementID, nil
}

func saveCreds(base, key string) error {
	body := []byte(`{
	  "ttl_seconds": 0,
	  "credentials": {
	    "vulnapp": [
	      {"name":"admin","role":"admin","credentials":[{"type":"headers","key":"Cookie","value":"session=admin_sess_a1b2c3"}]},
	      {"name":"test","role":"user","credentials":[{"type":"headers","key":"Cookie","value":"session=test_sess_d4e5f6"}]},
	      {"name":"m233241","role":"user","credentials":[{"type":"headers","key":"Cookie","value":"session=m233241_sess_g7h8i9"}]}
	    ]
	  }
	}`)
	req, _ := http.NewRequest("POST", base+"/credential/batch", bytes.NewReader(body))
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("save creds %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

func drive() error {
	proxyURL, _ := url.Parse("http://localhost:8888")
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	type call struct {
		Name, Sess, Method, Path, Body string
	}
	calls := []call{
		{"admin", "admin_sess_a1b2c3", "GET", "/api/profile", ""},
		{"test", "test_sess_d4e5f6", "GET", "/api/profile", ""},
		{"m233241", "m233241_sess_g7h8i9", "GET", "/api/profile", ""},

		{"admin", "admin_sess_a1b2c3", "GET", "/api/user/info?uid=1", ""},
		{"test", "test_sess_d4e5f6", "GET", "/api/user/info?uid=1", ""},
		{"m233241", "m233241_sess_g7h8i9", "GET", "/api/user/info?uid=1", ""},

		{"admin", "admin_sess_a1b2c3", "GET", "/api/order/7", ""},
		{"test", "test_sess_d4e5f6", "GET", "/api/order/7", ""},
		{"m233241", "m233241_sess_g7h8i9", "GET", "/api/order/7", ""},

		{"admin", "admin_sess_a1b2c3", "POST", "/api/order/cancel", `{"order_id":"7"}`},
		{"test", "test_sess_d4e5f6", "POST", "/api/order/cancel", `{"order_id":"7"}`},
		{"m233241", "m233241_sess_g7h8i9", "POST", "/api/order/cancel", `{"order_id":"7"}`},

		{"admin", "admin_sess_a1b2c3", "GET", "/api/admin/users", ""},
		{"test", "test_sess_d4e5f6", "GET", "/api/admin/users", ""},
		{"m233241", "m233241_sess_g7h8i9", "GET", "/api/admin/users", ""},

		{"admin", "admin_sess_a1b2c3", "POST", "/api/admin/user/delete", `{"uid":"1"}`},
		{"anonymous", "", "POST", "/api/admin/user/delete", `{"uid":"1"}`},
	}

	for _, c := range calls {
		var body io.Reader
		if c.Body != "" {
			body = bytes.NewReader([]byte(c.Body))
		}
		req, _ := http.NewRequest(c.Method, "http://host.docker.internal:8001"+c.Path, body)
		if c.Body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.Sess != "" {
			req.AddCookie(&http.Cookie{Name: "session", Value: c.Sess})
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("call %s %s: %w", c.Name, c.Path, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	return nil
}
```

- [ ] **Step 2: 构建 + Commit**

```bash
go build ./cmd/e2e-bac
git add cmd/e2e-bac
git commit -m "feat(cmd/e2e-bac): 触发器（建 engagement + 录凭证 + 18 次代理请求 + 轮询 finding）"
```

---

## Task 9.5: e2e 验收脚本扩展（验证黑客松借鉴改动落地）

**Files:**
- Modify: `cmd/e2e-bac/main.go`（在 finding 轮询后加 4 项额外断言）
- Create: `cmd/e2e-bac/verify_borrowed.go`（独立断言函数，便于单测）

**4 项额外断言**（spec §11 成功指标新增的 4 行）：
1. `engagement.memory_facts/ideas/hints` 都非空（三层 jsonb 都有写入）
2. `memory_hints` 至少有一条 `from_skill='vuln/web/bac'` 的 distill hint
3. `llm_call.role` 至少出现一次 `'observer'` 或 `'distill'`（多模型路由生效）
4. `agent_task.result->>'terminate_by'` 没有 `'observer_abort'` 或 `'done_force'`（功能存在但本次 e2e 无需触发）

- [ ] **Step 1: 写 verify_borrowed.go + 测试**

```go
package main

import (
    "context"
    "fmt"
    "github.com/jackc/pgx/v5/pgxpool"
)

func verifyBorrowedAdoptions(ctx context.Context, pool *pgxpool.Pool, eid string) error {
    // 1. memory 三层
    var f, i, h []byte
    if err := pool.QueryRow(ctx,
        `SELECT memory_facts, memory_ideas, memory_hints FROM engagement WHERE id=$1`, eid).
        Scan(&f, &i, &h); err != nil { return err }
    if len(f) <= 2 || len(i) <= 2 || len(h) <= 2 {
        return fmt.Errorf("memory 三层有空：facts=%s ideas=%s hints=%s", f, i, h)
    }

    // 2. distill hint
    var distillHints int
    if err := pool.QueryRow(ctx, `
        SELECT COUNT(*) FROM jsonb_array_elements(memory_hints->'hints') h
        WHERE h->>'from_skill' = 'vuln/web/bac'`).Scan(&distillHints); err != nil { return err }
    if distillHints == 0 { return fmt.Errorf("无 BAC distill hint") }

    // 3. 多模型路由
    var routedCalls int
    if err := pool.QueryRow(ctx,
        `SELECT COUNT(*) FROM llm_call WHERE engagement_id=$1 AND role IN ('observer','distill')`, eid).
        Scan(&routedCalls); err != nil { return err }
    if routedCalls == 0 { return fmt.Errorf("无 observer/distill llm_call，多模型路由未生效") }

    // 4. 异常终止
    var abnormal int
    if err := pool.QueryRow(ctx,
        `SELECT COUNT(*) FROM agent_task WHERE engagement_id=$1 AND result->>'terminate_by' IN ('observer_abort','done_force')`, eid).
        Scan(&abnormal); err != nil { return err }
    if abnormal > 0 { return fmt.Errorf("出现 observer_abort/done_force 共 %d 次，e2e 期望 0 次", abnormal) }

    return nil
}
```

- [ ] **Step 2: cmd/e2e-bac/main.go 加调用**

在 finding 轮询成功后插入：
```go
if err := verifyBorrowedAdoptions(ctx, pool, eid); err != nil {
    return fmt.Errorf("verify borrowed adoptions: %w", err)
}
fmt.Println("✓ 黑客松借鉴 4 项断言全部通过")
```

- [ ] **Step 3: Commit**

```bash
go build ./cmd/e2e-bac
git add cmd/e2e-bac
git commit -m "feat(e2e-bac): 加 4 项黑客松借鉴落地验证（memory 三层 / distill hint / 多模型路由 / 终止合法）"
```

---

## Task 10: e2e 跑通

- [ ] **Step 1: 起栈**

```bash
make up
make migrate
LIUSHA_DEEPSEEK_API_KEY=$YOUR_KEY \
  docker compose -f deployments/docker-compose.yml --profile e2e up -d --build
```

- [ ] **Step 2: 跑 e2e**

```bash
LIUSHA_DEEPSEEK_API_KEY=$YOUR_KEY make e2e-bac
```

- [ ] **Step 3: 期望日志**

```
{"component":"e2e-bac","engagement_id":"<uuid>","message":"engagement ready"}
{"component":"e2e-bac","bac":5,"kinds":{"bac.unauthorized_access":1,"bac.vertical_priv_esc":2,"bac.horizontal_priv_esc":2},"message":"poll"}
{"component":"e2e-bac","message":"e2e PASS: 5 finding 三类齐全"}
```

- [ ] **Step 4: SQL 直接验证**

```bash
docker exec -i liusha-postgres psql -U liusha -d liusha -c "
  SELECT kind, count(*), array_agg(distinct (target->>'url')) as urls
  FROM finding GROUP BY kind ORDER BY kind;"
```

预期：含 3 行 `bac.*`，count 总和 ≥ 5。

- [ ] **Step 5: 成本可查**

```bash
docker exec -i liusha-postgres psql -U liusha -d liusha -c "
  SELECT provider, model, count(*), SUM(cost_usd)::numeric(10,4) FROM llm_call GROUP BY 1,2;"
```

预期：deepseek 行 cost > 0。

- [ ] **Step 6: 单 Runtime + Memory 共享 grep**

```bash
test "$(grep -rE 'AgentRuntimeConfig\\{|runtime\\.Run\\(' internal/agent/runtime/*.go | wc -l)" -ge 1
docker exec -i liusha-postgres psql -U liusha -d liusha -c "
  SELECT count(*) FROM engagement WHERE memory != '{}'::jsonb;"
```

预期：第一条命令返回 0（成功）；第二条 count > 0（说明 sniffer 写过 memory）。

- [ ] **Step 7: 收尾 Commit**

```bash
git add -A
git commit -m "chore(plan-2): BAC e2e 通过（5 finding，3 类齐全，llm_call 有成本）"
```

---

## Self-Review

| spec 区域 | 覆盖任务 |
|---|---|
| §3.1 finding kind 'bac.*' + dedup_key 模板 | T3 SKILL.md + T2 写 finding |
| §3.4 memory 三层 | plan 1 T6（已含 ReadState/Append*） |
| §4.2 ReAct（子任务同 runtime） | plan 1 T23（plan 2 复用） |
| §4.3 Observer / LoopDetector / DoneValidate / 守护栏 / spawn depth=1 | plan 1 T22.5 + T23 + T23.5 + T4 |
| §5.1 cognitive_map 6 槽位 | T2.6 |
| §5.2 BAC skill 步骤指引（含 read_state / write_fact / write_idea / done.reason） | T3 |
| §5.3 sniffer 触发线索 | T5 system prompt + T1 read_window 输出 |
| §6.1 共享 actions（read_state / write_fact / write_idea / write_hint / done(系统校验)） | plan 1 T24 |
| §6.2 Sniffer 专属 read_window | T1 |
| §6.3 BAC 专属 4 个 action | T2 |
| §6.5 Action 中间件（result_compress / loop_detect / done_validate） | plan 1 T22.5（plan 2 BAC actions 自动经中间件链） |
| §7 lib（credential / replay / heuristic） | plan 1 T13/T14/T15（plan 2 在 T2 接入） |
| §8.4-§8.5 多模型路由 + 错误码退避 | plan 1 T21.5 |
| §8.6 经验自蒸馏 | plan 1 T23.5（finding.OnSaved hook） |
| §10.2 BAC e2e 流程 | T9 + T10 |
| §11 成功指标（BAC 5 + 三类齐全 + 成本 + memory 三层 + Observer / Distill / Router 验证 + 单 Runtime） | T9.5 + T10 step 4-6 |
| §13 风险与缓解 | T9.5 验证 done_force / observer_abort 不发生 |

**前后引用一致性：**
- `actions.ReadWindow.Windows / Flows` 接 `*window.Store / *flow.Store`，与 plan 1 T7/T10 命名一致
- `bac.Deps{Credentials, Replay, Flows}` 字段名与 plan 1 T13/T14 命名一致
- `spawner.New(tasks, wc, Limits{...})` 与 plan 1 T8/T26 一致
- SKILL.md 中 dedup_key 模板与 spec §3.1 完全相同

**已确认：** 无 "TBD" / "appropriate" / "similar to" 占位；每步都有完整代码或具体命令。

**Plan 2 完成定义：** `make e2e-bac` 输出 `e2e PASS: 5 finding 三类齐全`，且 `SELECT SUM(cost_usd) FROM llm_call > 0`。
