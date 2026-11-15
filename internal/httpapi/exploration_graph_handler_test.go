package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/task"
)

const graphTaskID = "5f0c6a8e-2d1b-4c3a-9e7f-8b6d4a2c1e90"

// fakeTaskReader 按 ID 返回任务行；不在表里的按 store 的形态返回包裹 pgx.ErrNoRows 的错误。
type fakeTaskReader map[string]task.Task

func (f fakeTaskReader) GetByID(_ context.Context, id string) (task.Task, error) {
	t, ok := f[id]
	if !ok {
		return task.Task{}, fmt.Errorf("get task %s: %w", id, pgx.ErrNoRows)
	}
	return t, nil
}

// fakeExplorationGraph 按任务记录被查询的 ID，返回固定的图与验证记录。
type fakeExplorationGraph struct {
	nodes         []explorationgraph.Node
	edges         []explorationgraph.Edge
	verifications []explorationgraph.Verification
	gotTask       string
	gotLimit      int
}

func (m *fakeExplorationGraph) ListNodesByTask(_ context.Context, taskID string, _ string) ([]explorationgraph.Node, error) {
	m.gotTask = taskID
	return m.nodes, nil
}

func (m *fakeExplorationGraph) ListEdgesByTask(_ context.Context, _ string) ([]explorationgraph.Edge, error) {
	return m.edges, nil
}

func (m *fakeExplorationGraph) ListVerificationsByTask(_ context.Context, taskID string, limit int) ([]explorationgraph.Verification, error) {
	m.gotTask, m.gotLimit = taskID, limit
	return m.verifications, nil
}

func newGraphServer(graph *fakeExplorationGraph) http.Handler {
	return NewServer(Deps{
		Auth:             newFakeAuthStore(),
		ExplorationGraph: graph,
		TaskReader:       fakeTaskReader{graphTaskID: {ID: graphTaskID}},
	})
}

func getAuthed(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(testSessionCookie())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// 探索图属于任务：按任务 ID 直接取，不经对话转一道。
func TestGetTaskGraph(t *testing.T) {
	graph := &fakeExplorationGraph{
		nodes: []explorationgraph.Node{{ID: "n1", Kind: core.KindObjective}, {ID: "n2", Kind: core.KindAction}},
		edges: []explorationgraph.Edge{{SrcID: "n1", DstID: "n2"}},
	}
	w := getAuthed(t, newGraphServer(graph), "/api/tasks/"+graphTaskID+"/exploration/graph")
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 %d：%s", w.Code, w.Body.String())
	}
	var resp GraphResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Nodes) != 2 || len(resp.Edges) != 1 {
		t.Errorf("应返回 2 个节点 1 条边，得 %d / %d", len(resp.Nodes), len(resp.Edges))
	}
	if graph.gotTask != graphTaskID {
		t.Errorf("应按任务取图，得 %q", graph.gotTask)
	}
}

// 空图（任务刚建、尚未规划）序列化为空数组而不是 null。
func TestGetTaskGraph_EmptyIsArrays(t *testing.T) {
	w := getAuthed(t, newGraphServer(&fakeExplorationGraph{}), "/api/tasks/"+graphTaskID+"/exploration/graph")
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 %d", w.Code)
	}
	if got := w.Body.String(); got != `{"nodes":[],"edges":[]}` {
		t.Errorf("空图应为空数组，得 %s", got)
	}
}

func TestGetTaskGraph_UnknownTaskIs404(t *testing.T) {
	w := getAuthed(t, newGraphServer(&fakeExplorationGraph{}), "/api/tasks/0b9c2f4e-1a2b-4c3d-8e9f-0a1b2c3d4e5f/exploration/graph")
	if w.Code != http.StatusNotFound {
		t.Fatalf("任务不存在应 404，得 %d", w.Code)
	}
}

func TestGetTaskGraph_InvalidIDIs400(t *testing.T) {
	w := getAuthed(t, newGraphServer(&fakeExplorationGraph{}), "/api/tasks/not-a-uuid/exploration/graph")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法 UUID 应 400，得 %d", w.Code)
	}
}

func TestGetTaskGraph_Unauthorized(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+graphTaskID+"/exploration/graph", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "wrong-token"}) // #nosec G124 // 测试伪造凭证
	w := httptest.NewRecorder()
	newGraphServer(&fakeExplorationGraph{}).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录应 401，得 %d", w.Code)
	}
}

func TestGetTaskVerifications(t *testing.T) {
	graph := &fakeExplorationGraph{verifications: []explorationgraph.Verification{{ID: "v7", NodeID: "n2"}}}
	h := newGraphServer(graph)

	w := getAuthed(t, h, "/api/tasks/"+graphTaskID+"/exploration/verifications?limit=50")
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 %d：%s", w.Code, w.Body.String())
	}
	var resp struct {
		Verifications []explorationgraph.Verification `json:"verifications"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Verifications) != 1 || graph.gotTask != graphTaskID || graph.gotLimit != 50 {
		t.Errorf("应按任务取 limit=50 的验证记录，得 %d 条 task=%q limit=%d", len(resp.Verifications), graph.gotTask, graph.gotLimit)
	}

	// 缺省与非法 limit 取默认值
	getAuthed(t, h, "/api/tasks/"+graphTaskID+"/exploration/verifications?limit=x")
	if graph.gotLimit != defaultVerificationLimit {
		t.Errorf("非法 limit 应取默认 %d，得 %d", defaultVerificationLimit, graph.gotLimit)
	}

	if w := getAuthed(t, h, "/api/tasks/0b9c2f4e-1a2b-4c3d-8e9f-0a1b2c3d4e5f/exploration/verifications"); w.Code != http.StatusNotFound {
		t.Errorf("任务不存在应 404，得 %d", w.Code)
	}
}

// 对话下的探索图接口已删除：图属于任务。
func TestConversationGraphRoutesRemoved(t *testing.T) {
	h := newGraphServer(&fakeExplorationGraph{})
	for _, p := range []string{"/exploration/graph", "/exploration/verifications"} {
		if w := getAuthed(t, h, "/api/conversations/"+graphTaskID+p); w.Code != http.StatusNotFound {
			t.Errorf("%s 应已删除（404），得 %d", p, w.Code)
		}
	}
}
