package common

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/vulnfinding"
	"github.com/V3teran/liusha/internal/graph"
)

// 编译期接口断言：保证 engagement.Store / vulnfinding.Store / graph.Store
// 自动满足本包定义的窄接口。任意签名漂移都会在 go build/test 阶段立刻失败。
var (
	_ MemoryStore  = (*engagement.Store)(nil)
	_ FindingStore = (*vulnfinding.Store)(nil)
	_ GraphStore   = (*graph.Store)(nil)
)

// fakeMem 是 MemoryStore 的内存实现，按追加顺序保留 notes。
type fakeMem struct {
	state []byte
	notes [][]byte
}

func (f *fakeMem) ReadState(_ context.Context, _ string) ([]byte, error) { return f.state, nil }
func (f *fakeMem) ReadStateScoped(_ context.Context, _ string, _ engagement.ReadOpts) ([]byte, error) {
	return f.state, nil
}
func (f *fakeMem) AppendNote(_ context.Context, _ string, e []byte) error {
	f.notes = append(f.notes, append([]byte(nil), e...))
	return nil
}

// fakeFinding 是 FindingStore 的内存实现，记录最后一次 Save 的入参。
type fakeFinding struct {
	saved vulnfinding.VulnFinding
	id    string
}

func (f *fakeFinding) Save(_ context.Context, in vulnfinding.VulnFinding) (vulnfinding.VulnFinding, bool, error) {
	f.saved = in
	if f.id == "" {
		f.id = "finding-id-stub"
	}
	saved := in
	saved.ID = f.id
	return saved, true, nil
}

// fakeGraph 是 GraphStore 的内存实现，按 dedup_key 分配伪 ID。
type fakeGraph struct {
	nodes []graph.NodeParams
	edges []graph.EdgeParams
}

func (g *fakeGraph) UpsertNode(_ context.Context, p graph.NodeParams) (graph.Node, error) {
	g.nodes = append(g.nodes, p)
	return graph.Node{
		ID:           "node-" + p.DedupKey,
		EngagementID: p.EngagementID,
		Kind:         p.Kind,
		DedupKey:     p.DedupKey,
		Payload:      p.Payload,
	}, nil
}
func (g *fakeGraph) UpsertEdge(_ context.Context, p graph.EdgeParams) (graph.Edge, error) {
	g.edges = append(g.edges, p)
	return graph.Edge{
		ID:           "edge-stub",
		EngagementID: p.EngagementID,
		FromID:       p.FromID,
		ToID:         p.ToID,
		Kind:         p.Kind,
		Payload:      p.Payload,
	}, nil
}

// ---------- Done ----------

func TestDone_ReturnsDone(t *testing.T) {
	r, err := Done{}.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !r.Done {
		t.Fatalf("Done 未置位")
	}
	if string(r.Output) != "{}" {
		t.Fatalf("nil args 应回吐空对象，got %s", string(r.Output))
	}
}

func TestDone_WithReason(t *testing.T) {
	args := json.RawMessage(`{"reason":"任务完成","summary":"已找到 1 个 SQLi"}`)
	r, err := Done{}.Execute(context.Background(), args)
	if err != nil || !r.Done {
		t.Fatalf("unexpected: done=%v err=%v", r.Done, err)
	}
	if string(r.Output) != string(args) {
		t.Fatalf("Output 应原样回吐，got %s", string(r.Output))
	}
}

// ---------- ReadState ----------

func TestReadState_ReturnsState(t *testing.T) {
	want := []byte(`{"facts":{"evidence":[]},"ideas":{"hypotheses":[]},"hints":{"hints":[]}}`)
	rd := &ReadState{Store: &fakeMem{state: want}, EngagementID: "e"}
	got, err := rd.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if string(got.Output) != string(want) {
		t.Fatalf("Output mismatch:\n want %s\n got  %s", string(want), string(got.Output))
	}
}

// ---------- TakeNote ----------

func TestTakeNote_AppendsObservation(t *testing.T) {
	m := &fakeMem{}
	wr := &TakeNote{Store: m, EngagementID: "e", TaskID: "task-x"}
	_, err := wr.Execute(context.Background(),
		json.RawMessage(`{"kind":"observation","content":"endpoint X returned 401"}`))
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(m.notes) != 1 {
		t.Fatalf("应追加 1 条 note，got %d", len(m.notes))
	}
	if !strings.Contains(string(m.notes[0]), "observation") {
		t.Fatalf("entry 缺少 kind=observation: %s", string(m.notes[0]))
	}
	if !strings.Contains(string(m.notes[0]), "task-x") {
		t.Fatalf("entry 缺少 task_id 标记: %s", string(m.notes[0]))
	}
}

func TestTakeNote_AppendsHypothesisWithStatus(t *testing.T) {
	m := &fakeMem{}
	wr := &TakeNote{Store: m, EngagementID: "e", TaskID: "task-x"}
	_, err := wr.Execute(context.Background(),
		json.RawMessage(`{"kind":"hypothesis","content":"GET /admin","status":"testing"}`))
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(m.notes) != 1 {
		t.Fatalf("应追加 1 条 note，got %d", len(m.notes))
	}
	if !strings.Contains(string(m.notes[0]), `"status":"testing"`) {
		t.Fatalf("entry 缺少 status=testing: %s", string(m.notes[0]))
	}
}

func TestTakeNote_RejectsInvalidKind(t *testing.T) {
	m := &fakeMem{}
	wr := &TakeNote{Store: m, EngagementID: "e"}
	_, err := wr.Execute(context.Background(),
		json.RawMessage(`{"kind":"junk","content":"x"}`))
	if err == nil {
		t.Fatal("非法 kind 应报错")
	}
	if len(m.notes) != 0 {
		t.Fatalf("校验失败仍写入: %v", m.notes)
	}
}

func TestTakeNote_RejectsEmptyContent(t *testing.T) {
	wr := &TakeNote{Store: &fakeMem{}, EngagementID: "e"}
	_, err := wr.Execute(context.Background(),
		json.RawMessage(`{"kind":"observation","content":""}`))
	if err == nil {
		t.Fatal("空 content 应报错")
	}
}

func TestTakeNote_RejectsStatusOnNonHypothesis(t *testing.T) {
	wr := &TakeNote{Store: &fakeMem{}, EngagementID: "e"}
	_, err := wr.Execute(context.Background(),
		json.RawMessage(`{"kind":"observation","content":"x","status":"verified"}`))
	if err == nil {
		t.Fatal("status 仅 hypothesis 时可填，应报错")
	}
}

func TestTakeNote_RejectsInvalidStatus(t *testing.T) {
	wr := &TakeNote{Store: &fakeMem{}, EngagementID: "e"}
	_, err := wr.Execute(context.Background(),
		json.RawMessage(`{"kind":"hypothesis","content":"x","status":"foo"}`))
	if err == nil {
		t.Fatal("非法 status 应报错")
	}
}

// ---------- WriteFinding ----------

func TestWriteFinding_CallsStoreSave(t *testing.T) {
	st := &fakeFinding{id: "fid-1"}
	wr := &WriteFinding{Store: st, EngagementID: "eng-1", TaskID: "task-1"}
	args := json.RawMessage(`{
		"kind":"sqli",
		"severity":"high",
		"title":"login form",
		"dedup_key":"sqli|/login|email",
		"target":{"url":"/login"},
		"evidence":{"req":"x"},
		"tool":"sqlmap",
		"confidence":"verified"
	}`)
	r, err := wr.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if st.saved.EngagementID != "eng-1" {
		t.Fatalf("EngagementID 未透传: %q", st.saved.EngagementID)
	}
	if st.saved.TaskID == nil || *st.saved.TaskID != "task-1" {
		t.Fatalf("TaskID 未透传: %+v", st.saved.TaskID)
	}
	if st.saved.DedupKey != "sqli|/login|email" || st.saved.Severity != vulnfinding.SeverityHigh {
		t.Fatalf("字段未透传: %+v", st.saved)
	}
	var out struct {
		ID       string `json:"id"`
		DedupKey string `json:"dedup_key"`
	}
	if err := json.Unmarshal(r.Output, &out); err != nil || out.ID != "fid-1" {
		t.Fatalf("Output 不正确: %s err=%v", string(r.Output), err)
	}
}

func TestWriteFinding_RejectsMissingDedupKey(t *testing.T) {
	wr := &WriteFinding{Store: &fakeFinding{}, EngagementID: "e"}
	_, err := wr.Execute(context.Background(),
		json.RawMessage(`{"kind":"x","severity":"low","title":"t","dedup_key":""}`))
	if err == nil {
		t.Fatal("dedup_key 必填，应报错")
	}
}

func TestWriteFinding_PropagatesStoreError(t *testing.T) {
	wr := &WriteFinding{Store: erroringFinding{}, EngagementID: "e", TaskID: ""}
	_, err := wr.Execute(context.Background(),
		json.RawMessage(`{"kind":"x","severity":"low","title":"t","dedup_key":"k"}`))
	if err == nil {
		t.Fatal("Store.Save 失败应回传错误")
	}
}

type erroringFinding struct{}

func (erroringFinding) Save(_ context.Context, _ vulnfinding.VulnFinding) (vulnfinding.VulnFinding, bool, error) {
	return vulnfinding.VulnFinding{}, false, errors.New("boom")
}

// ---------- WriteGraph ----------

func TestWriteGraph_NodeAndEdges(t *testing.T) {
	g := &fakeGraph{}
	wr := &WriteGraph{Store: g, EngagementID: "eng-1"}
	args := json.RawMessage(`{
		"nodes":[
			{"kind":"endpoint","dedup_key":"GET /a","payload":{}},
			{"kind":"endpoint","dedup_key":"GET /b","payload":{}}
		],
		"edges":[
			{"from":"GET /a","to":"GET /b","kind":"links_to","payload":{}}
		]
	}`)
	r, err := wr.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(g.nodes) != 2 {
		t.Fatalf("应 upsert 2 个 node，got %d", len(g.nodes))
	}
	if len(g.edges) != 1 {
		t.Fatalf("应 upsert 1 条 edge，got %d", len(g.edges))
	}
	if g.edges[0].FromID != "node-GET /a" || g.edges[0].ToID != "node-GET /b" {
		t.Fatalf("edge 端点未按 dedup_key 解析: %+v", g.edges[0])
	}
	var out struct {
		Nodes int `json:"nodes"`
		Edges int `json:"edges"`
	}
	if err := json.Unmarshal(r.Output, &out); err != nil {
		t.Fatalf("Output 解析失败: %v", err)
	}
	if out.Nodes != 2 || out.Edges != 1 {
		t.Fatalf("Output 计数错误: %+v", out)
	}
}

func TestWriteGraph_EdgeWithExistingNodeID(t *testing.T) {
	g := &fakeGraph{}
	wr := &WriteGraph{Store: g, EngagementID: "e"}
	args := json.RawMessage(`{
		"nodes":[],
		"edges":[{"from":"uuid-1","to":"uuid-2","kind":"k","payload":{}}]
	}`)
	if _, err := wr.Execute(context.Background(), args); err != nil {
		t.Fatalf("err=%v", err)
	}
	if g.edges[0].FromID != "uuid-1" || g.edges[0].ToID != "uuid-2" {
		t.Fatalf("未走历史 UUID 路径: %+v", g.edges[0])
	}
}

func TestWriteGraph_RejectsIncompleteNode(t *testing.T) {
	wr := &WriteGraph{Store: &fakeGraph{}, EngagementID: "e"}
	_, err := wr.Execute(context.Background(),
		json.RawMessage(`{"nodes":[{"kind":"endpoint","dedup_key":""}]}`))
	if err == nil {
		t.Fatal("空 dedup_key 应报错")
	}
}
