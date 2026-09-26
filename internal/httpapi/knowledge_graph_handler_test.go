package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// mockExplorationGraphAPI 是 ExplorationGraphAPI 的测试 mock
type mockExplorationGraphAPI struct {
	nodes []explorationgraph.Node
	edges []explorationgraph.Edge
	stats map[string]int
}

func (m *mockExplorationGraphAPI) ListNodesForAPI(ctx context.Context, taskID string, kind string) ([]explorationgraph.Node, error) {
	if kind == "" {
		return m.nodes, nil
	}
	// 按类型过滤
	filtered := make([]explorationgraph.Node, 0)
	for _, node := range m.nodes {
		if string(node.Kind) == kind {
			filtered = append(filtered, node)
		}
	}
	return filtered, nil
}

func (m *mockExplorationGraphAPI) ListEdgesForAPI(ctx context.Context, taskID string) ([]explorationgraph.Edge, error) {
	return m.edges, nil
}

func (m *mockExplorationGraphAPI) GetStatsForAPI(ctx context.Context, taskID string) (map[string]int, error) {
	return m.stats, nil
}

func TestGetTaskStats(t *testing.T) {
	mock := &mockExplorationGraphAPI{
		stats: map[string]int{
			"objectives":   2,
			"actions":      5,
			"observations": 5,
			"results":      2,
		},
	}

	server := NewServer(Deps{
		APIKey:           "test-key",
		ExplorationGraph: mock,
	})

	req := httptest.NewRequest("GET", "/api/v1/tasks/task-123/stats", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp GraphStats
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp.Objectives != 2 {
		t.Errorf("expected 2 objectives, got %d", resp.Objectives)
	}
	if resp.Actions != 5 {
		t.Errorf("expected 5 actions, got %d", resp.Actions)
	}
	if resp.Observations != 5 {
		t.Errorf("expected 5 observations, got %d", resp.Observations)
	}
	if resp.Results != 2 {
		t.Errorf("expected 2 results, got %d", resp.Results)
	}
}

func TestGetTaskNodes(t *testing.T) {
	mock := &mockExplorationGraphAPI{
		nodes: []explorationgraph.Node{
			{ID: "n1", Kind: core.KindObjective},
			{ID: "n2", Kind: core.KindAction},
			{ID: "n3", Kind: core.KindAction},
		},
	}

	server := NewServer(Deps{
		APIKey:           "test-key",
		ExplorationGraph: mock,
	})

	// 测试不带过滤
	req := httptest.NewRequest("GET", "/api/v1/tasks/task-123/nodes", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp NodesResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(resp.Nodes) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(resp.Nodes))
	}

	// 测试带 kind 过滤
	req = httptest.NewRequest("GET", "/api/v1/tasks/task-123/nodes?kind=action", nil)
	req.Header.Set("X-API-Key", "test-key")
	w = httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(resp.Nodes) != 2 {
		t.Errorf("expected 2 action nodes, got %d", len(resp.Nodes))
	}
}

func TestGetTaskGraph(t *testing.T) {
	mock := &mockExplorationGraphAPI{
		nodes: []explorationgraph.Node{
			{ID: "n1", Kind: core.KindObjective},
			{ID: "n2", Kind: core.KindAction},
		},
		edges: []explorationgraph.Edge{
			{SrcID: "n1", DstID: "n2", Rel: explorationgraph.RelGenerates},
		},
	}

	server := NewServer(Deps{
		APIKey:           "test-key",
		ExplorationGraph: mock,
	})

	req := httptest.NewRequest("GET", "/api/v1/tasks/task-123/graph", nil)
	req.Header.Set("X-API-Key", "test-key")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp GraphResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(resp.Nodes) != 2 {
		t.Errorf("expected 2 nodes, got %d", len(resp.Nodes))
	}
	if len(resp.Edges) != 1 {
		t.Errorf("expected 1 edge, got %d", len(resp.Edges))
	}
}
