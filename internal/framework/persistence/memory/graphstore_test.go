package memory

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemoryGraphStore_CreateAndGetNode 测试节点的创建和获取
func TestMemoryGraphStore_CreateAndGetNode(t *testing.T) {
	store := NewGraphStore()
	ctx := context.Background()

	// 准备测试数据
	node := &core.GraphNode{
		ID:        "node-1",
		Kind:      string(core.KindAction),
		Content:   json.RawMessage(`{"title":"test action"}`),
		Metadata:  map[string]interface{}{"task_id": "task-1"},
		State:     string(core.NodeStateOpen),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Version:   1,
	}

	// 创建节点
	err := store.CreateNode(ctx, node)
	require.NoError(t, err, "创建节点应该成功")

	// 获取节点
	retrieved, err := store.GetNode(ctx, "node-1")
	require.NoError(t, err, "获取节点应该成功")
	assert.Equal(t, "node-1", retrieved.ID)
	assert.Equal(t, string(core.KindAction), retrieved.Kind)
	assert.Equal(t, string(core.NodeStateOpen), retrieved.State)
	assert.JSONEq(t, `{"title":"test action"}`, string(retrieved.Content))
}

// TestMemoryGraphStore_GetNode_NotFound 测试获取不存在的节点
func TestMemoryGraphStore_GetNode_NotFound(t *testing.T) {
	store := NewGraphStore()
	ctx := context.Background()

	_, err := store.GetNode(ctx, "non-existent")
	assert.Error(t, err, "获取不存在的节点应该返回错误")
	assert.ErrorIs(t, err, core.ErrGraphNodeNotFound)
}

// TestMemoryGraphStore_UpdateNode 测试节点更新
func TestMemoryGraphStore_UpdateNode(t *testing.T) {
	store := NewGraphStore()
	ctx := context.Background()

	// 创建初始节点
	node := &core.GraphNode{
		ID:        "node-1",
		Kind:      string(core.KindAction),
		Content:   json.RawMessage(`{"title":"initial"}`),
		State:     string(core.NodeStateOpen),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Version:   1,
	}
	require.NoError(t, store.CreateNode(ctx, node))

	// 更新节点
	update := core.GraphNodeUpdate{
		State:   string(core.NodeStateRunning),
		Content: json.RawMessage(`{"title":"updated"}`),
	}
	err := store.UpdateNode(ctx, "node-1", update)
	require.NoError(t, err, "更新节点应该成功")

	// 验证更新
	updated, err := store.GetNode(ctx, "node-1")
	require.NoError(t, err)
	assert.Equal(t, string(core.NodeStateRunning), updated.State)
	assert.JSONEq(t, `{"title":"updated"}`, string(updated.Content))
	assert.Equal(t, int64(2), updated.Version, "版本号应该递增")
}

// TestMemoryGraphStore_CompareAndSwapState 测试 CAS 操作
func TestMemoryGraphStore_CompareAndSwapState(t *testing.T) {
	store := NewGraphStore()
	ctx := context.Background()

	// 创建节点
	node := &core.GraphNode{
		ID:        "node-1",
		Kind:      string(core.KindAction),
		State:     string(core.NodeStateOpen),
		Metadata:  map[string]interface{}{"task_id": "task-1"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Version:   1,
	}
	require.NoError(t, store.CreateNode(ctx, node))

	t.Run("CAS成功 - 状态匹配", func(t *testing.T) {
		swapped, err := store.CompareAndSwapState(
			ctx, "task-1", "node-1",
			string(core.NodeStateOpen),
			string(core.NodeStateRunning),
		)
		require.NoError(t, err)
		assert.True(t, swapped, "状态匹配时CAS应该成功")

		// 验证状态已更新
		updated, _ := store.GetNode(ctx, "node-1")
		assert.Equal(t, string(core.NodeStateRunning), updated.State)
	})

	t.Run("CAS失败 - 状态不匹配", func(t *testing.T) {
		swapped, err := store.CompareAndSwapState(
			ctx, "task-1", "node-1",
			string(core.NodeStateOpen), // 期望是 open，但实际是 running
			string(core.NodeStateCompleted),
		)
		require.NoError(t, err)
		assert.False(t, swapped, "状态不匹配时CAS应该失败")

		// 验证状态未改变
		node, _ := store.GetNode(ctx, "node-1")
		assert.Equal(t, string(core.NodeStateRunning), node.State)
	})
}

// TestMemoryGraphStore_DeleteNode 测试节点删除
func TestMemoryGraphStore_DeleteNode(t *testing.T) {
	store := NewGraphStore()
	ctx := context.Background()

	// 创建节点
	node := &core.GraphNode{
		ID:        "node-1",
		Kind:      string(core.KindAction),
		State:     string(core.NodeStateOpen),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Version:   1,
	}
	require.NoError(t, store.CreateNode(ctx, node))

	// 删除节点
	err := store.DeleteNode(ctx, "node-1")
	require.NoError(t, err, "删除节点应该成功")

	// 验证节点已删除
	_, err = store.GetNode(ctx, "node-1")
	assert.ErrorIs(t, err, core.ErrGraphNodeNotFound)
}

// TestMemoryGraphStore_ListNodes 测试节点列表查询
func TestMemoryGraphStore_ListNodes(t *testing.T) {
	store := NewGraphStore()
	ctx := context.Background()

	// 创建多个节点
	nodes := []*core.GraphNode{
		{
			ID:        "node-1",
			Kind:      string(core.KindAction),
			State:     string(core.NodeStateOpen),
			Metadata:  map[string]interface{}{"task_id": "task-1"},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Version:   1,
		},
		{
			ID:        "node-2",
			Kind:      string(core.KindObjective),
			State:     string(core.NodeStateRunning),
			Metadata:  map[string]interface{}{"task_id": "task-1"},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Version:   1,
		},
		{
			ID:        "node-3",
			Kind:      string(core.KindAction),
			State:     string(core.NodeStateCompleted),
			Metadata:  map[string]interface{}{"task_id": "task-2"},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Version:   1,
		},
	}

	for _, n := range nodes {
		require.NoError(t, store.CreateNode(ctx, n))
	}

	t.Run("按任务ID查询", func(t *testing.T) {
		query := core.GraphNodeQuery{
			Filters: map[string]interface{}{"metadata.task_id": "task-1"},
		}
		result, err := store.ListNodes(ctx, query)
		require.NoError(t, err)
		assert.Len(t, result, 2, "task-1 应该有2个节点")
	})

	t.Run("按Kind查询", func(t *testing.T) {
		query := core.GraphNodeQuery{
			Kind: string(core.KindAction),
		}
		result, err := store.ListNodes(ctx, query)
		require.NoError(t, err)
		assert.Len(t, result, 2, "应该有2个Action节点")
	})

	t.Run("按State查询", func(t *testing.T) {
		query := core.GraphNodeQuery{
			State: string(core.NodeStateOpen),
		}
		result, err := store.ListNodes(ctx, query)
		require.NoError(t, err)
		assert.Len(t, result, 1, "应该有1个Open状态的节点")
	})

	t.Run("组合查询", func(t *testing.T) {
		query := core.GraphNodeQuery{
			Kind:    string(core.KindAction),
			Filters: map[string]interface{}{"metadata.task_id": "task-1"},
		}
		result, err := store.ListNodes(ctx, query)
		require.NoError(t, err)
		assert.Len(t, result, 1, "task-1的Action节点应该只有1个")
	})
}

// TestMemoryGraphStore_CreateEdge 测试边的创建
func TestMemoryGraphStore_CreateEdge(t *testing.T) {
	store := NewGraphStore()
	ctx := context.Background()

	// 创建两个节点
	node1 := &core.GraphNode{
		ID:        "node-1",
		Kind:      string(core.KindAction),
		Metadata:  map[string]interface{}{"task_id": "task-1"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Version:   1,
	}
	node2 := &core.GraphNode{
		ID:        "node-2",
		Kind:      string(core.KindObservation),
		Metadata:  map[string]interface{}{"task_id": "task-1"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Version:   1,
	}
	require.NoError(t, store.CreateNode(ctx, node1))
	require.NoError(t, store.CreateNode(ctx, node2))

	// 创建边
	edge := &core.GraphEdge{
		TaskID:   "task-1",
		From:     "node-1",
		To:       "node-2",
		Relation: string("generates"),
	}
	err := store.CreateEdge(ctx, edge)
	require.NoError(t, err, "创建边应该成功")

	// 查询边
	edges, err := store.ListEdges(ctx, core.GraphEdgeQuery{TaskID: "task-1"})
	require.NoError(t, err)
	assert.Len(t, edges, 1)
	assert.Equal(t, "node-1", edges[0].From)
	assert.Equal(t, "node-2", edges[0].To)
	assert.Equal(t, string("generates"), edges[0].Relation)
}

// TestMemoryGraphStore_CreateEdge_Idempotent 测试边创建的幂等性
func TestMemoryGraphStore_CreateEdge_Idempotent(t *testing.T) {
	store := NewGraphStore()
	ctx := context.Background()

	// 创建节点
	for _, id := range []string{"node-1", "node-2"} {
		node := &core.GraphNode{
			ID:        id,
			Kind:      string(core.KindAction),
			Metadata:  map[string]interface{}{"task_id": "task-1"},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Version:   1,
		}
		require.NoError(t, store.CreateNode(ctx, node))
	}

	// 创建相同的边两次
	edge := &core.GraphEdge{
		TaskID:   "task-1",
		From:     "node-1",
		To:       "node-2",
		Relation: string("generates"),
	}

	err1 := store.CreateEdge(ctx, edge)
	err2 := store.CreateEdge(ctx, edge)

	require.NoError(t, err1)
	require.NoError(t, err2, "重复创建边应该是幂等的")

	// 验证只有一条边
	edges, _ := store.ListEdges(ctx, core.GraphEdgeQuery{TaskID: "task-1"})
	assert.Len(t, edges, 1)
}

// TestMemoryGraphStore_DeleteEdge 测试边删除
func TestMemoryGraphStore_DeleteEdge(t *testing.T) {
	store := NewGraphStore()
	ctx := context.Background()

	// 创建节点和边
	for _, id := range []string{"node-1", "node-2"} {
		node := &core.GraphNode{
			ID:        id,
			Kind:      string(core.KindAction),
			Metadata:  map[string]interface{}{"task_id": "task-1"},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Version:   1,
		}
		require.NoError(t, store.CreateNode(ctx, node))
	}

	edge := &core.GraphEdge{
		TaskID:   "task-1",
		From:     "node-1",
		To:       "node-2",
		Relation: string("generates"),
	}
	require.NoError(t, store.CreateEdge(ctx, edge))

	// 删除边
	err := store.DeleteEdge(ctx, "node-1", "node-2", string("generates"))
	require.NoError(t, err)

	// 验证边已删除
	edges, _ := store.ListEdges(ctx, core.GraphEdgeQuery{TaskID: "task-1"})
	assert.Len(t, edges, 0)
}

// TestMemoryGraphStore_CreateNodeWithEdges 测试事务性创建
func TestMemoryGraphStore_CreateNodeWithEdges(t *testing.T) {
	store := NewGraphStore()
	ctx := context.Background()

	// 创建一个已存在的节点
	existingNode := &core.GraphNode{
		ID:        "existing-node",
		Kind:      string(core.KindAction),
		Metadata:  map[string]interface{}{"task_id": "task-1"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Version:   1,
	}
	require.NoError(t, store.CreateNode(ctx, existingNode))

	// 创建新节点及其边
	newNode := &core.GraphNode{
		ID:        "new-node",
		Kind:      string(core.KindObservation),
		Metadata:  map[string]interface{}{"task_id": "task-1"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Version:   1,
	}

	edges := []*core.GraphEdge{
		{
			TaskID:   "task-1",
			From:     "existing-node",
			To:       "new-node",
			Relation: string("generates"),
		},
	}

	err := store.CreateNodeWithEdges(ctx, newNode, edges)
	require.NoError(t, err, "事务性创建应该成功")

	// 验证节点和边都已创建
	node, err := store.GetNode(ctx, "new-node")
	require.NoError(t, err)
	assert.Equal(t, "new-node", node.ID)

	allEdges, err := store.ListEdges(ctx, core.GraphEdgeQuery{TaskID: "task-1"})
	require.NoError(t, err)
	assert.Len(t, allEdges, 1)
}

// ApplyChangeSet 与 Postgres 事务同语义：新节点先生效，迁移与批注可作用于它们；
// 任务不符或前置不成立即整体不生效。
func TestGraphStore_ApplyChangeSet(t *testing.T) {
	ctx := context.Background()
	newNode := func(id, task string) *core.GraphNode {
		return &core.GraphNode{ID: id, Kind: "action", State: "open", Metadata: map[string]interface{}{"task_id": task}}
	}

	t.Run("作用于本批新节点", func(t *testing.T) {
		store := NewGraphStore()
		err := store.ApplyChangeSet(ctx, core.GraphChangeSet{
			Nodes:       []*core.GraphNode{newNode("n1", "t1")},
			Transitions: []core.GraphStateTransition{{TaskID: "t1", NodeID: "n1", From: "open", To: "running"}},
			Annotations: []core.GraphNodeAnnotation{{TaskID: "t1", NodeID: "n1", Metadata: map[string]interface{}{"k": "v"}}},
		})
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		n, _ := store.GetNode(ctx, "n1")
		if n.State != "running" || n.Metadata["k"] != "v" || n.Metadata["task_id"] != "t1" {
			t.Fatalf("新节点迁移/批注未生效或身份丢失: %+v", n)
		}
	})

	t.Run("任务不符整体不生效", func(t *testing.T) {
		store := NewGraphStore()
		err := store.ApplyChangeSet(ctx, core.GraphChangeSet{
			Nodes:       []*core.GraphNode{newNode("n1", "t1")},
			Annotations: []core.GraphNodeAnnotation{{TaskID: "t2", NodeID: "n1", Metadata: map[string]interface{}{"k": "v"}}},
		})
		if !errors.Is(err, core.ErrGraphNodeNotFound) {
			t.Fatalf("want ErrGraphNodeNotFound, got %v", err)
		}
		if _, getErr := store.GetNode(ctx, "n1"); getErr == nil {
			t.Fatal("校验失败时新节点不应落下")
		}
	})

	t.Run("前置不成立整体不生效", func(t *testing.T) {
		store := NewGraphStore()
		if err := store.CreateNode(ctx, newNode("old", "t1")); err != nil {
			t.Fatal(err)
		}
		err := store.ApplyChangeSet(ctx, core.GraphChangeSet{
			Nodes:       []*core.GraphNode{newNode("n1", "t1")},
			Transitions: []core.GraphStateTransition{{TaskID: "t1", NodeID: "old", From: "running", To: "completed"}},
			Edges:       []*core.GraphEdge{{TaskID: "t1", From: "n1", To: "old", Relation: "generates"}},
		})
		if !errors.Is(err, core.ErrGraphStateConflict) {
			t.Fatalf("want ErrGraphStateConflict, got %v", err)
		}
		if _, getErr := store.GetNode(ctx, "n1"); getErr == nil {
			t.Fatal("冲突时新节点不应落下")
		}
	})
}

// 一次性批注：键已存在（节点上或本批更早写入）即冲突；连续迁移逐条递增版本。
func TestGraphStore_ApplyChangeSet_OnceAndVersion(t *testing.T) {
	ctx := context.Background()
	store := NewGraphStore()
	node := &core.GraphNode{ID: "n1", Kind: "action", State: "open", Metadata: map[string]interface{}{"task_id": "t1"}}
	if err := store.CreateNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	once := core.GraphNodeAnnotation{TaskID: "t1", NodeID: "n1", Metadata: map[string]interface{}{"mark": true}, Once: true}

	if err := store.ApplyChangeSet(ctx, core.GraphChangeSet{Annotations: []core.GraphNodeAnnotation{once, once}}); !errors.Is(err, core.ErrGraphStateConflict) {
		t.Fatalf("本批重复的一次性批注应冲突，got %v", err)
	}
	if err := store.ApplyChangeSet(ctx, core.GraphChangeSet{Annotations: []core.GraphNodeAnnotation{once}}); err != nil {
		t.Fatalf("首次一次性批注: %v", err)
	}
	if err := store.ApplyChangeSet(ctx, core.GraphChangeSet{Annotations: []core.GraphNodeAnnotation{once}}); !errors.Is(err, core.ErrGraphStateConflict) {
		t.Fatalf("键已存在应冲突，got %v", err)
	}

	before, _ := store.GetNode(ctx, "n1")
	err := store.ApplyChangeSet(ctx, core.GraphChangeSet{Transitions: []core.GraphStateTransition{
		{TaskID: "t1", NodeID: "n1", From: "open", To: "running"},
		{TaskID: "t1", NodeID: "n1", From: "running", To: "completed"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := store.GetNode(ctx, "n1")
	if after.State != "completed" || after.Version != before.Version+2 {
		t.Fatalf("连续迁移应逐条递增版本: state=%s version %d→%d", after.State, before.Version, after.Version)
	}
}
