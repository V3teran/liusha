package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 测试辅助函数

func setupPostgresTest(t *testing.T) (*PostgresGraphStore, func()) {
	t.Helper()

	// 从环境变量读取数据库连接
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping PostgreSQL tests")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)

	store := NewPostgresGraphStore(pool)

	// 清理函数
	cleanup := func() {
		// 清理测试数据
		_, _ = pool.Exec(ctx, `DELETE FROM wm_edge WHERE task_id LIKE 'test-%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM wm_node WHERE task_id LIKE 'test-%'`)
		pool.Close()
	}

	return store, cleanup
}

func TestPostgresGraphStore_CreateNode(t *testing.T) {
	store, cleanup := setupPostgresTest(t)
	defer cleanup()

	ctx := context.Background()
	taskID := "test-" + uuid.New().String()

	tests := []struct {
		name    string
		node    *GraphNode
		wantErr bool
	}{
		{
			name: "创建 objective 节点",
			node: &GraphNode{
				ID:      uuid.New().String(),
				Kind:    string(KindObjective),
				Content: json.RawMessage(`{"description": "测试目标"}`),
				State:   string(ActionStateOpen),
				Metadata: map[string]interface{}{
					"task_id": taskID,
				},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			wantErr: false,
		},
		{
			name: "创建 action 节点",
			node: &GraphNode{
				ID:      uuid.New().String(),
				Kind:    string(KindAction),
				Content: json.RawMessage(`{"command": "scan"}`),
				State:   string(ActionStateOpen),
				Metadata: map[string]interface{}{
					"task_id":    taskID,
					"complexity": "simple",
				},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			wantErr: false,
		},
		{
			name: "创建 observation 节点",
			node: &GraphNode{
				ID:         uuid.New().String(),
				Kind:       string(KindObservation),
				Content:    json.RawMessage(`{"result": "success"}`),
				Confidence: 0.9,
				Metadata: map[string]interface{}{
					"task_id": taskID,
				},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			wantErr: false,
		},
		{
			name: "缺少 ID",
			node: &GraphNode{
				Kind:    string(KindObjective),
				Content: json.RawMessage(`{}`),
			},
			wantErr: true,
		},
		{
			name: "缺少 Kind",
			node: &GraphNode{
				ID:      uuid.New().String(),
				Content: json.RawMessage(`{}`),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.CreateNode(ctx, tt.node)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)

				// 验证节点已创建
				node, err := store.GetNode(ctx, tt.node.ID)
				require.NoError(t, err)
				assert.Equal(t, tt.node.ID, node.ID)
				assert.Equal(t, tt.node.Kind, node.Kind)
			}
		})
	}
}

func TestPostgresGraphStore_GetNode(t *testing.T) {
	store, cleanup := setupPostgresTest(t)
	defer cleanup()

	ctx := context.Background()
	taskID := "test-" + uuid.New().String()

	// 创建测试节点
	nodeID := uuid.New().String()
	node := &GraphNode{
		ID:      nodeID,
		Kind:    string(KindObjective),
		Content: json.RawMessage(`{"description": "测试目标"}`),
		State:   string(ActionStateOpen),
		Metadata: map[string]interface{}{
			"task_id": taskID,
			"custom":  "value",
		},
		Confidence: 0.8,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	err := store.CreateNode(ctx, node)
	require.NoError(t, err)

	// 测试获取节点
	t.Run("获取存在的节点", func(t *testing.T) {
		got, err := store.GetNode(ctx, nodeID)
		require.NoError(t, err)
		assert.Equal(t, nodeID, got.ID)
		assert.Equal(t, string(KindObjective), got.Kind)
		assert.JSONEq(t, string(node.Content), string(got.Content))
		assert.Equal(t, 0.8, got.Confidence)
		assert.Equal(t, "value", got.Metadata["custom"])
	})

	t.Run("获取不存在的节点", func(t *testing.T) {
		_, err := store.GetNode(ctx, "nonexistent")
		assert.ErrorIs(t, err, ErrGraphNodeNotFound)
	})

	t.Run("空 ID", func(t *testing.T) {
		_, err := store.GetNode(ctx, "")
		assert.Error(t, err)
	})
}

func TestPostgresGraphStore_UpdateNode(t *testing.T) {
	store, cleanup := setupPostgresTest(t)
	defer cleanup()

	ctx := context.Background()
	taskID := "test-" + uuid.New().String()

	// 创建测试节点
	nodeID := uuid.New().String()
	node := &GraphNode{
		ID:      nodeID,
		Kind:    string(KindAction),
		Content: json.RawMessage(`{"command": "scan"}`),
		State:   string(ActionStateOpen),
		Metadata: map[string]interface{}{
			"task_id":    taskID,
			"complexity": "simple",
		},
		Confidence: 0.5,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	err := store.CreateNode(ctx, node)
	require.NoError(t, err)

	t.Run("更新 State", func(t *testing.T) {
		err := store.UpdateNode(ctx, nodeID, GraphNodeUpdate{
			State: string(ActionStateRunning),
		})
		require.NoError(t, err)

		got, err := store.GetNode(ctx, nodeID)
		require.NoError(t, err)
		assert.Equal(t, string(ActionStateRunning), got.State)
	})

	t.Run("更新 Content", func(t *testing.T) {
		newContent := json.RawMessage(`{"command": "exploit"}`)
		err := store.UpdateNode(ctx, nodeID, GraphNodeUpdate{
			Content: newContent,
		})
		require.NoError(t, err)

		got, err := store.GetNode(ctx, nodeID)
		require.NoError(t, err)
		assert.JSONEq(t, string(newContent), string(got.Content))
	})

	t.Run("更新 Confidence", func(t *testing.T) {
		newConf := 0.9
		err := store.UpdateNode(ctx, nodeID, GraphNodeUpdate{
			Confidence: &newConf,
		})
		require.NoError(t, err)

		got, err := store.GetNode(ctx, nodeID)
		require.NoError(t, err)
		assert.Equal(t, 0.9, got.Confidence)
	})

	t.Run("更新 Metadata", func(t *testing.T) {
		newMeta := map[string]interface{}{
			"task_id": taskID,
			"updated": true,
		}
		err := store.UpdateNode(ctx, nodeID, GraphNodeUpdate{
			Metadata: newMeta,
		})
		require.NoError(t, err)

		got, err := store.GetNode(ctx, nodeID)
		require.NoError(t, err)
		assert.True(t, got.Metadata["updated"].(bool))
	})

	t.Run("更新不存在的节点", func(t *testing.T) {
		err := store.UpdateNode(ctx, "nonexistent", GraphNodeUpdate{
			State: string(ActionStateDone),
		})
		assert.ErrorIs(t, err, ErrGraphNodeNotFound)
	})
}

func TestPostgresGraphStore_DeleteNode(t *testing.T) {
	store, cleanup := setupPostgresTest(t)
	defer cleanup()

	ctx := context.Background()
	taskID := "test-" + uuid.New().String()

	// 创建测试节点
	nodeID := uuid.New().String()
	node := &GraphNode{
		ID:      nodeID,
		Kind:    string(KindObjective),
		Content: json.RawMessage(`{}`),
		State:   string(ActionStateOpen),
		Metadata: map[string]interface{}{
			"task_id": taskID,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err := store.CreateNode(ctx, node)
	require.NoError(t, err)

	t.Run("删除存在的节点", func(t *testing.T) {
		err := store.DeleteNode(ctx, nodeID)
		require.NoError(t, err)

		// 验证节点已删除
		_, err = store.GetNode(ctx, nodeID)
		assert.ErrorIs(t, err, ErrGraphNodeNotFound)
	})

	t.Run("删除不存在的节点", func(t *testing.T) {
		err := store.DeleteNode(ctx, "nonexistent")
		assert.ErrorIs(t, err, ErrGraphNodeNotFound)
	})
}

func TestPostgresGraphStore_ListNodes(t *testing.T) {
	store, cleanup := setupPostgresTest(t)
	defer cleanup()

	ctx := context.Background()
	taskID := "test-" + uuid.New().String()

	// 创建多个测试节点
	nodes := []*GraphNode{
		{
			ID:      uuid.New().String(),
			Kind:    string(KindObjective),
			Content: json.RawMessage(`{"name": "obj1"}`),
			State:   string(ActionStateDone),
			Metadata: map[string]interface{}{
				"task_id": taskID,
			},
			Confidence: 0.8,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		},
		{
			ID:      uuid.New().String(),
			Kind:    string(KindAction),
			Content: json.RawMessage(`{"name": "act1"}`),
			State:   string(ActionStateOpen),
			Metadata: map[string]interface{}{
				"task_id":    taskID,
				"complexity": "simple",
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		{
			ID:      uuid.New().String(),
			Kind:    string(KindAction),
			Content: json.RawMessage(`{"name": "act2"}`),
			State:   string(ActionStateDone),
			Metadata: map[string]interface{}{
				"task_id":    taskID,
				"complexity": "simple",
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}

	for _, node := range nodes {
		err := store.CreateNode(ctx, node)
		require.NoError(t, err)
	}

	t.Run("列出所有节点", func(t *testing.T) {
		got, err := store.ListNodes(ctx, GraphNodeQuery{
			Filters: map[string]interface{}{
				"metadata.task_id": taskID,
			},
		})
		require.NoError(t, err)
		assert.Len(t, got, 3)
	})

	t.Run("按 Kind 过滤", func(t *testing.T) {
		got, err := store.ListNodes(ctx, GraphNodeQuery{
			Kind: string(KindAction),
			Filters: map[string]interface{}{
				"metadata.task_id": taskID,
			},
		})
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})

	t.Run("按 State 过滤", func(t *testing.T) {
		got, err := store.ListNodes(ctx, GraphNodeQuery{
			State: string(ActionStateOpen),
			Filters: map[string]interface{}{
				"metadata.task_id": taskID,
			},
		})
		require.NoError(t, err)
		assert.Len(t, got, 1)
		assert.Equal(t, nodes[1].ID, got[0].ID)
	})

	t.Run("按 MinConfidence 过滤", func(t *testing.T) {
		got, err := store.ListNodes(ctx, GraphNodeQuery{
			MinConfidence: 0.7,
			Filters: map[string]interface{}{
				"metadata.task_id": taskID,
			},
		})
		require.NoError(t, err)
		assert.Len(t, got, 1)
		assert.Equal(t, nodes[0].ID, got[0].ID)
	})

	t.Run("分页", func(t *testing.T) {
		got, err := store.ListNodes(ctx, GraphNodeQuery{
			Filters: map[string]interface{}{
				"metadata.task_id": taskID,
			},
			Limit:  2,
			Offset: 0,
		})
		require.NoError(t, err)
		assert.Len(t, got, 2)

		got, err = store.ListNodes(ctx, GraphNodeQuery{
			Filters: map[string]interface{}{
				"metadata.task_id": taskID,
			},
			Limit:  2,
			Offset: 2,
		})
		require.NoError(t, err)
		assert.Len(t, got, 1)
	})
}

func TestPostgresGraphStore_CreateEdge(t *testing.T) {
	store, cleanup := setupPostgresTest(t)
	defer cleanup()

	ctx := context.Background()
	taskID := "test-" + uuid.New().String()

	// 创建测试节点
	node1 := &GraphNode{
		ID:      uuid.New().String(),
		Kind:    string(KindAction),
		Content: json.RawMessage(`{}`),
		State:   string(ActionStateOpen),
		Metadata: map[string]interface{}{
			"task_id":    taskID,
			"complexity": "simple",
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	node2 := &GraphNode{
		ID:      uuid.New().String(),
		Kind:    string(KindObservation),
		Content: json.RawMessage(`{}`),
		Metadata: map[string]interface{}{
			"task_id": taskID,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err := store.CreateNode(ctx, node1)
	require.NoError(t, err)
	err = store.CreateNode(ctx, node2)
	require.NoError(t, err)

	t.Run("创建边", func(t *testing.T) {
		edge := &GraphEdge{
			From:     node1.ID,
			To:       node2.ID,
			Relation: string(RelationGenerates),
			Metadata: map[string]interface{}{
				"weight": 1.0,
			},
			CreatedAt: time.Now(),
		}

		err := store.CreateEdge(ctx, edge)
		require.NoError(t, err)

		// 验证边已创建
		edges, err := store.ListEdges(ctx, GraphEdgeQuery{
			From: node1.ID,
		})
		require.NoError(t, err)
		assert.Len(t, edges, 1)
		assert.Equal(t, node2.ID, edges[0].To)
	})

	t.Run("幂等创建", func(t *testing.T) {
		edge := &GraphEdge{
			From:      node1.ID,
			To:        node2.ID,
			Relation:  string(RelationGenerates),
			CreatedAt: time.Now(),
		}

		// 重复创建不应报错
		err := store.CreateEdge(ctx, edge)
		require.NoError(t, err)
		err = store.CreateEdge(ctx, edge)
		require.NoError(t, err)
	})
}

func TestPostgresGraphStore_ListEdges(t *testing.T) {
	store, cleanup := setupPostgresTest(t)
	defer cleanup()

	ctx := context.Background()
	taskID := "test-" + uuid.New().String()

	// 创建测试节点
	nodes := make([]*GraphNode, 3)
	for i := range nodes {
		nodes[i] = &GraphNode{
			ID:      uuid.New().String(),
			Kind:    string(KindAction),
			Content: json.RawMessage(`{}`),
			State:   string(ActionStateOpen),
			Metadata: map[string]interface{}{
				"task_id":    taskID,
				"complexity": "simple",
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		err := store.CreateNode(ctx, nodes[i])
		require.NoError(t, err)
	}

	// 创建边
	edges := []*GraphEdge{
		{
			From:      nodes[0].ID,
			To:        nodes[1].ID,
			Relation:  string(RelationDependsOn),
			CreatedAt: time.Now(),
		},
		{
			From:      nodes[1].ID,
			To:        nodes[2].ID,
			Relation:  string(RelationDependsOn),
			CreatedAt: time.Now(),
		},
		{
			From:      nodes[0].ID,
			To:        nodes[2].ID,
			Relation:  string(RelationEnables),
			CreatedAt: time.Now(),
		},
	}

	for _, edge := range edges {
		err := store.CreateEdge(ctx, edge)
		require.NoError(t, err)
	}

	t.Run("按 From 过滤", func(t *testing.T) {
		got, err := store.ListEdges(ctx, GraphEdgeQuery{
			From: nodes[0].ID,
		})
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})

	t.Run("按 To 过滤", func(t *testing.T) {
		got, err := store.ListEdges(ctx, GraphEdgeQuery{
			To: nodes[2].ID,
		})
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})

	t.Run("按 Relation 过滤", func(t *testing.T) {
		got, err := store.ListEdges(ctx, GraphEdgeQuery{
			Relation: string(RelationDependsOn),
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(got), 2)
	})

	t.Run("组合过滤", func(t *testing.T) {
		got, err := store.ListEdges(ctx, GraphEdgeQuery{
			From:     nodes[0].ID,
			Relation: string(RelationEnables),
		})
		require.NoError(t, err)
		assert.Len(t, got, 1)
		assert.Equal(t, nodes[2].ID, got[0].To)
	})
}

func TestPostgresGraphStore_DeleteEdge(t *testing.T) {
	store, cleanup := setupPostgresTest(t)
	defer cleanup()

	ctx := context.Background()
	taskID := "test-" + uuid.New().String()

	// 创建测试节点
	node1 := &GraphNode{
		ID:      uuid.New().String(),
		Kind:    string(KindAction),
		Content: json.RawMessage(`{}`),
		State:   string(ActionStateOpen),
		Metadata: map[string]interface{}{
			"task_id":    taskID,
			"complexity": "simple",
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	node2 := &GraphNode{
		ID:      uuid.New().String(),
		Kind:    string(KindObservation),
		Content: json.RawMessage(`{}`),
		Metadata: map[string]interface{}{
			"task_id": taskID,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err := store.CreateNode(ctx, node1)
	require.NoError(t, err)
	err = store.CreateNode(ctx, node2)
	require.NoError(t, err)

	// 创建边
	edge := &GraphEdge{
		From:      node1.ID,
		To:        node2.ID,
		Relation:  string(RelationGenerates),
		CreatedAt: time.Now(),
	}
	err = store.CreateEdge(ctx, edge)
	require.NoError(t, err)

	t.Run("删除存在的边", func(t *testing.T) {
		err := store.DeleteEdge(ctx, node1.ID, node2.ID, string(RelationGenerates))
		require.NoError(t, err)

		// 验证边已删除
		edges, err := store.ListEdges(ctx, GraphEdgeQuery{
			From: node1.ID,
			To:   node2.ID,
		})
		require.NoError(t, err)
		assert.Len(t, edges, 0)
	})

	t.Run("删除不存在的边", func(t *testing.T) {
		err := store.DeleteEdge(ctx, node1.ID, node2.ID, string(RelationGenerates))
		assert.ErrorIs(t, err, ErrGraphEdgeNotFound)
	})
}

func TestPostgresGraphStore_Traverse(t *testing.T) {
	store, cleanup := setupPostgresTest(t)
	defer cleanup()

	ctx := context.Background()
	taskID := "test-" + uuid.New().String()

	// 创建图结构：n1 → n2 → n3 → n4
	nodes := make([]*GraphNode, 4)
	for i := range nodes {
		nodes[i] = &GraphNode{
			ID:      uuid.New().String(),
			Kind:    string(KindAction),
			Content: json.RawMessage(fmt.Sprintf(`{"name": "node%d"}`, i)),
			State:   string(ActionStateOpen),
			Metadata: map[string]interface{}{
				"task_id":    taskID,
				"complexity": "simple",
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		err := store.CreateNode(ctx, nodes[i])
		require.NoError(t, err)
	}

	// 创建链式边
	for i := 0; i < len(nodes)-1; i++ {
		err := store.CreateEdge(ctx, &GraphEdge{
			From:      nodes[i].ID,
			To:        nodes[i+1].ID,
			Relation:  string(RelationDependsOn),
			CreatedAt: time.Now(),
		})
		require.NoError(t, err)
	}

	t.Run("BFS 遍历", func(t *testing.T) {
		got, err := store.Traverse(ctx, nodes[0].ID, GraphTraverseQuery{
			Direction: GraphTraverseOut,
			Strategy:  GraphTraverseBFS,
		})
		require.NoError(t, err)
		assert.Len(t, got, 4)
		assert.Equal(t, nodes[0].ID, got[0].ID)
	})

	t.Run("DFS 遍历", func(t *testing.T) {
		got, err := store.Traverse(ctx, nodes[0].ID, GraphTraverseQuery{
			Direction: GraphTraverseOut,
			Strategy:  GraphTraverseDFS,
		})
		require.NoError(t, err)
		assert.Len(t, got, 4)
		assert.Equal(t, nodes[0].ID, got[0].ID)
	})

	t.Run("限制深度", func(t *testing.T) {
		got, err := store.Traverse(ctx, nodes[0].ID, GraphTraverseQuery{
			Direction: GraphTraverseOut,
			Strategy:  GraphTraverseBFS,
			MaxDepth:  2,
		})
		require.NoError(t, err)
		assert.LessOrEqual(t, len(got), 2)
	})

	t.Run("反向遍历", func(t *testing.T) {
		got, err := store.Traverse(ctx, nodes[3].ID, GraphTraverseQuery{
			Direction: GraphTraverseIn,
			Strategy:  GraphTraverseBFS,
		})
		require.NoError(t, err)
		assert.Len(t, got, 4)
		assert.Equal(t, nodes[3].ID, got[0].ID)
	})

	t.Run("按关系过滤", func(t *testing.T) {
		got, err := store.Traverse(ctx, nodes[0].ID, GraphTraverseQuery{
			Direction: GraphTraverseOut,
			Strategy:  GraphTraverseBFS,
			Relations: []string{string(RelationDependsOn)},
		})
		require.NoError(t, err)
		assert.Len(t, got, 4)
	})

	t.Run("节点过滤", func(t *testing.T) {
		got, err := store.Traverse(ctx, nodes[0].ID, GraphTraverseQuery{
			Direction: GraphTraverseOut,
			Strategy:  GraphTraverseBFS,
			NodeFilter: &GraphNodeQuery{
				Kind: string(KindAction),
			},
		})
		require.NoError(t, err)
		assert.Len(t, got, 4)
	})
}
