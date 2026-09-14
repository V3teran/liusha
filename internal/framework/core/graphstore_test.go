package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGraphStoreInterface 验证 GraphStore 接口的基本功能
func TestGraphStoreInterface(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryGraphStore()

	t.Run("CreateNode and GetNode", func(t *testing.T) {
		// 创建节点
		node := &GraphNode{
			ID:         "node-1",
			Kind:       "action",
			Content:    json.RawMessage(`{"type":"scan","target":"example.com"}`),
			State:      "open",
			Confidence: 0.8,
			Metadata: map[string]interface{}{
				"task_id": "task-123",
				"priority": "high",
			},
		}

		err := store.CreateNode(ctx, node)
		require.NoError(t, err)

		// 获取节点
		retrieved, err := store.GetNode(ctx, "node-1")
		require.NoError(t, err)
		assert.Equal(t, "action", retrieved.Kind)
		assert.Equal(t, "open", retrieved.State)
		assert.Equal(t, 0.8, retrieved.Confidence)
		assert.Equal(t, "task-123", retrieved.Metadata["task_id"])
	})

	t.Run("UpdateNode", func(t *testing.T) {
		// 更新节点
		confidence := 0.95
		update := GraphNodeUpdate{
			State:      "completed",
			Confidence: &confidence,
		}

		err := store.UpdateNode(ctx, "node-1", update)
		require.NoError(t, err)

		// 验证更新
		retrieved, err := store.GetNode(ctx, "node-1")
		require.NoError(t, err)
		assert.Equal(t, "completed", retrieved.State)
		assert.Equal(t, 0.95, retrieved.Confidence)
	})

	t.Run("ListNodes with filters", func(t *testing.T) {
		// 创建多个节点
		nodes := []*GraphNode{
			{
				ID:         "node-2",
				Kind:       "observation",
				State:      "verified",
				Confidence: 0.9,
				Metadata:   map[string]interface{}{"task_id": "task-123"},
			},
			{
				ID:         "node-3",
				Kind:       "action",
				State:      "open",
				Confidence: 0.6,
				Metadata:   map[string]interface{}{"task_id": "task-456"},
			},
		}

		for _, node := range nodes {
			err := store.CreateNode(ctx, node)
			require.NoError(t, err)
		}

		// 按 Kind 过滤
		result, err := store.ListNodes(ctx, GraphNodeQuery{Kind: "action"})
		require.NoError(t, err)
		assert.Len(t, result, 2) // node-1 和 node-3

		// 按 State 过滤
		result, err = store.ListNodes(ctx, GraphNodeQuery{State: "open"})
		require.NoError(t, err)
		assert.Len(t, result, 1)

		// 按 Confidence 过滤
		result, err = store.ListNodes(ctx, GraphNodeQuery{MinConfidence: 0.8})
		require.NoError(t, err)
		assert.Len(t, result, 2) // node-1 (0.95) 和 node-2 (0.9)

		// 按 Metadata 过滤
		result, err = store.ListNodes(ctx, GraphNodeQuery{
			Filters: map[string]interface{}{"task_id": "task-123"},
		})
		require.NoError(t, err)
		assert.Len(t, result, 2) // node-1 和 node-2
	})

	t.Run("Pagination", func(t *testing.T) {
		// 测试分页
		result, err := store.ListNodes(ctx, GraphNodeQuery{
			Limit:  2,
			Offset: 1,
		})
		require.NoError(t, err)
		assert.LessOrEqual(t, len(result), 2)
	})

	t.Run("DeleteNode", func(t *testing.T) {
		err := store.DeleteNode(ctx, "node-3")
		require.NoError(t, err)

		// 验证删除
		_, err = store.GetNode(ctx, "node-3")
		assert.ErrorIs(t, err, ErrGraphNodeNotFound)
	})
}

// TestGraphStoreEdges 验证边操作
func TestGraphStoreEdges(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryGraphStore()

	// 创建节点
	nodes := []*GraphNode{
		{ID: "n1", Kind: "action"},
		{ID: "n2", Kind: "observation"},
		{ID: "n3", Kind: "result"},
	}

	for _, node := range nodes {
		err := store.CreateNode(ctx, node)
		require.NoError(t, err)
	}

	t.Run("CreateEdge and ListEdges", func(t *testing.T) {
		// 创建边：n1 -> n2 (generates)
		edge := &GraphEdge{
			From:     "n1",
			To:       "n2",
			Relation: "generates",
			Metadata: map[string]interface{}{"weight": 1.0},
		}

		err := store.CreateEdge(ctx, edge)
		require.NoError(t, err)

		// 创建边：n2 -> n3 (confirms)
		edge2 := &GraphEdge{
			From:     "n2",
			To:       "n3",
			Relation: "confirms",
		}

		err = store.CreateEdge(ctx, edge2)
		require.NoError(t, err)

		// 列出所有边
		edges, err := store.ListEdges(ctx, GraphEdgeQuery{})
		require.NoError(t, err)
		assert.Len(t, edges, 2)

		// 按 From 过滤
		edges, err = store.ListEdges(ctx, GraphEdgeQuery{From: "n1"})
		require.NoError(t, err)
		assert.Len(t, edges, 1)
		assert.Equal(t, "generates", edges[0].Relation)

		// 按 Relation 过滤
		edges, err = store.ListEdges(ctx, GraphEdgeQuery{Relation: "confirms"})
		require.NoError(t, err)
		assert.Len(t, edges, 1)
		assert.Equal(t, "n2", edges[0].From)
	})

	t.Run("DeleteEdge", func(t *testing.T) {
		err := store.DeleteEdge(ctx, "n1", "n2", "generates")
		require.NoError(t, err)

		// 验证删除
		edges, err := store.ListEdges(ctx, GraphEdgeQuery{From: "n1"})
		require.NoError(t, err)
		assert.Len(t, edges, 0)
	})
}

// TestGraphStoreTraverse 验证图遍历
func TestGraphStoreTraverse(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryGraphStore()

	// 创建图结构：
	//   n1 -> n2 -> n4
	//   n1 -> n3 -> n5
	nodes := []*GraphNode{
		{ID: "n1", Kind: "action", State: "completed"},
		{ID: "n2", Kind: "observation", State: "verified"},
		{ID: "n3", Kind: "observation", State: "pending"},
		{ID: "n4", Kind: "result", State: "confirmed"},
		{ID: "n5", Kind: "result", State: "pending"},
	}

	for _, node := range nodes {
		err := store.CreateNode(ctx, node)
		require.NoError(t, err)
	}

	edges := []*GraphEdge{
		{From: "n1", To: "n2", Relation: "generates"},
		{From: "n1", To: "n3", Relation: "generates"},
		{From: "n2", To: "n4", Relation: "confirms"},
		{From: "n3", To: "n5", Relation: "confirms"},
	}

	for _, edge := range edges {
		err := store.CreateEdge(ctx, edge)
		require.NoError(t, err)
	}

	t.Run("BFS traversal - all nodes", func(t *testing.T) {
		result, err := store.Traverse(ctx, "n1", GraphTraverseQuery{
			Direction: GraphTraverseOut,
			Strategy:  GraphTraverseBFS,
		})
		require.NoError(t, err)
		assert.Len(t, result, 5) // 所有节点
	})

	t.Run("BFS traversal - max depth 1", func(t *testing.T) {
		result, err := store.Traverse(ctx, "n1", GraphTraverseQuery{
			Direction: GraphTraverseOut,
			Strategy:  GraphTraverseBFS,
			MaxDepth:  1,
		})
		require.NoError(t, err)
		assert.Len(t, result, 3) // n1, n2, n3
	})

	t.Run("BFS traversal - with node filter", func(t *testing.T) {
		result, err := store.Traverse(ctx, "n1", GraphTraverseQuery{
			Direction: GraphTraverseOut,
			Strategy:  GraphTraverseBFS,
			NodeFilter: &GraphNodeQuery{
				Kind: "result",
			},
		})
		require.NoError(t, err)
		assert.Len(t, result, 2) // n4, n5
	})

	t.Run("BFS traversal - with relation filter", func(t *testing.T) {
		result, err := store.Traverse(ctx, "n1", GraphTraverseQuery{
			Direction: GraphTraverseOut,
			Strategy:  GraphTraverseBFS,
			Relations: []string{"generates"},
			MaxDepth:  1,
		})
		require.NoError(t, err)
		assert.Len(t, result, 3) // n1, n2, n3 (只走 generates 关系)
	})

	t.Run("DFS traversal", func(t *testing.T) {
		result, err := store.Traverse(ctx, "n1", GraphTraverseQuery{
			Direction: GraphTraverseOut,
			Strategy:  GraphTraverseDFS,
		})
		require.NoError(t, err)
		assert.Len(t, result, 5)
	})

	t.Run("Traverse in direction", func(t *testing.T) {
		// 从 n4 反向遍历
		result, err := store.Traverse(ctx, "n4", GraphTraverseQuery{
			Direction: GraphTraverseIn,
			Strategy:  GraphTraverseBFS,
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(result), 2) // 至少 n4, n2
	})
}

// TestGraphStoreEdgeCases 验证边界情况
func TestGraphStoreEdgeCases(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryGraphStore()

	t.Run("GetNode - not found", func(t *testing.T) {
		_, err := store.GetNode(ctx, "nonexistent")
		assert.ErrorIs(t, err, ErrGraphNodeNotFound)
	})

	t.Run("UpdateNode - not found", func(t *testing.T) {
		err := store.UpdateNode(ctx, "nonexistent", GraphNodeUpdate{State: "test"})
		assert.ErrorIs(t, err, ErrGraphNodeNotFound)
	})

	t.Run("DeleteNode - not found", func(t *testing.T) {
		err := store.DeleteNode(ctx, "nonexistent")
		assert.ErrorIs(t, err, ErrGraphNodeNotFound)
	})

	t.Run("CreateEdge - node not found", func(t *testing.T) {
		edge := &GraphEdge{
			From:     "nonexistent1",
			To:       "nonexistent2",
			Relation: "test",
		}
		err := store.CreateEdge(ctx, edge)
		assert.ErrorIs(t, err, ErrGraphNodeNotFound)
	})

	t.Run("DeleteEdge - not found", func(t *testing.T) {
		err := store.DeleteEdge(ctx, "n1", "n2", "nonexistent")
		assert.ErrorIs(t, err, ErrGraphEdgeNotFound)
	})

	t.Run("Traverse - start node not found", func(t *testing.T) {
		_, err := store.Traverse(ctx, "nonexistent", GraphTraverseQuery{})
		assert.ErrorIs(t, err, ErrGraphNodeNotFound)
	})
}

// TestGraphStoreTimestamps 验证时间戳自动设置
func TestGraphStoreTimestamps(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryGraphStore()

	t.Run("Node timestamps", func(t *testing.T) {
		node := &GraphNode{
			ID:   "n1",
			Kind: "action",
		}

		before := time.Now()
		err := store.CreateNode(ctx, node)
		require.NoError(t, err)
		after := time.Now()

		retrieved, err := store.GetNode(ctx, "n1")
		require.NoError(t, err)

		// 验证 CreatedAt 在合理范围内
		assert.True(t, retrieved.CreatedAt.After(before) || retrieved.CreatedAt.Equal(before))
		assert.True(t, retrieved.CreatedAt.Before(after) || retrieved.CreatedAt.Equal(after))

		// 更新后 UpdatedAt 应该变化
		time.Sleep(10 * time.Millisecond)
		err = store.UpdateNode(ctx, "n1", GraphNodeUpdate{State: "updated"})
		require.NoError(t, err)

		updated, err := store.GetNode(ctx, "n1")
		require.NoError(t, err)
		assert.True(t, updated.UpdatedAt.After(retrieved.UpdatedAt))
	})
}

// BenchmarkGraphStore 性能基准测试
func BenchmarkGraphStore(b *testing.B) {
	ctx := context.Background()
	store := NewInMemoryGraphStore()

	// 预创建一些节点
	for i := 0; i < 1000; i++ {
		node := &GraphNode{
			ID:   string(rune(i)),
			Kind: "action",
		}
		_ = store.CreateNode(ctx, node)
	}

	b.Run("CreateNode", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			node := &GraphNode{
				ID:   string(rune(1000 + i)),
				Kind: "action",
			}
			_ = store.CreateNode(ctx, node)
		}
	})

	b.Run("GetNode", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = store.GetNode(ctx, string(rune(i%1000)))
		}
	})

	b.Run("ListNodes", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = store.ListNodes(ctx, GraphNodeQuery{Kind: "action", Limit: 10})
		}
	})
}
