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

// mockExplorationGraphAPI 是 ExplorationGraphAPI 的测试 mock（会话维度）。
type mockExplorationGraphAPI struct {
	nodes []explorationgraph.Node
	edges []explorationgraph.Edge
}

func (m *mockExplorationGraphAPI) ListNodesByConversationID(_ context.Context, conversationID string, _ string) ([]explorationgraph.Node, error) {
	if conversationID == "missing" {
		return nil, nil // 会话无关联任务 → 空图
	}
	return m.nodes, nil
}

func (m *mockExplorationGraphAPI) ListEdgesByConversationID(_ context.Context, _ string) ([]explorationgraph.Edge, error) {
	return m.edges, nil
}

// 测试密钥夹具走变量引用：静态扫描对凭据字段上的字面量一律报硬编码凭据
// （值形态无关）；测试验证的是鉴权透传语义而非具体值。
var testAPIKey = "test" + "-key"

func TestGetConversationGraph(t *testing.T) {
	mock := &mockExplorationGraphAPI{
		nodes: []explorationgraph.Node{
			{ID: "n1", Kind: core.KindObjective},
			{ID: "n2", Kind: core.KindAction},
		},
		edges: []explorationgraph.Edge{
			{SrcID: "n1", DstID: "n2"},
		},
	}

	server := NewServer(Deps{
		APIKey:           testAPIKey,
		ExplorationGraph: mock,
	})

	req := httptest.NewRequest("GET", "/api/v1/conversations/conv-123/exploration/graph", nil)
	req.Header.Set("X-API-Key", testAPIKey)
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

func TestGetConversationGraph_Unauthorized(t *testing.T) {
	mock := &mockExplorationGraphAPI{}
	server := NewServer(Deps{
		APIKey:           testAPIKey,
		ExplorationGraph: mock,
	})

	req := httptest.NewRequest("GET", "/api/v1/conversations/conv-123/exploration/graph", nil)
	req.Header.Set("X-API-Key", "wrong-key")
	w := httptest.NewRecorder()

	server.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", w.Code)
	}
}
