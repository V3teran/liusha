//go:build integration

package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPostgresGraphStore_CreateAndGetNode 测试 PostgreSQL 实现的节点创建和获取
func TestPostgresGraphStore_CreateAndGetNode(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	defer pool.Close()

	store := NewPostgresGraphStore(pool)

	// 创建节点
	node := &GraphNode{
		ID:       "11111111-1111-4111-8111-111111111111",
		Kind:     string(KindAction),
		Content:  json.RawMessage(`{"title":"test action"}`),
		Metadata: map[string]interface{}{"task_id": "cccccccc-0000-4000-8000-000000000001"},
		State:    string(ActionStateOpen),
	}

	err := store.CreateNode(ctx, node)
	require.NoError(t, err, "创建节点应该成功")

	// 获取节点
	retrieved, err := store.GetNode(ctx, "11111111-1111-4111-8111-111111111111")
	require.NoError(t, err, "获取节点应该成功")
	assert.Equal(t, "11111111-1111-4111-8111-111111111111", retrieved.ID)
	assert.Equal(t, string(KindAction), retrieved.Kind)
	assert.Equal(t, string(ActionStateOpen), retrieved.State)
	assert.JSONEq(t, `{"title":"test action"}`, string(retrieved.Content))
	assert.Equal(t, int64(1), retrieved.Version, "初始版本应该是1")
}

// TestPostgresGraphStore_UpdateNode 测试节点更新
func TestPostgresGraphStore_UpdateNode(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	defer pool.Close()

	store := NewPostgresGraphStore(pool)

	// 创建初始节点
	node := &GraphNode{
		ID:       "22222222-2222-4222-8222-222222222222",
		Kind:     string(KindAction),
		Content:  json.RawMessage(`{"title":"initial"}`),
		State:    string(ActionStateOpen),
		Metadata: map[string]interface{}{"task_id": "cccccccc-0000-4000-8000-000000000001"},
	}
	require.NoError(t, store.CreateNode(ctx, node))

	// 更新节点
	update := GraphNodeUpdate{
		State:   string(ActionStateRunning),
		Content: json.RawMessage(`{"title":"updated"}`),
	}
	err := store.UpdateNode(ctx, "22222222-2222-4222-8222-222222222222", update)
	require.NoError(t, err, "更新节点应该成功")

	// 验证更新
	updated, err := store.GetNode(ctx, "22222222-2222-4222-8222-222222222222")
	require.NoError(t, err)
	assert.Equal(t, string(ActionStateRunning), updated.State)
	assert.JSONEq(t, `{"title":"updated"}`, string(updated.Content))
	assert.Equal(t, int64(2), updated.Version, "版本号应该递增")
}

// TestPostgresGraphStore_OptimisticLocking 测试乐观锁
func TestPostgresGraphStore_OptimisticLocking(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	defer pool.Close()

	store := NewPostgresGraphStore(pool)

	// 创建节点
	node := &GraphNode{
		ID:       "33333333-3333-4333-8333-333333333333",
		Kind:     string(KindAction),
		Content:  json.RawMessage(`{}`),
		State:    string(ActionStateOpen),
		Metadata: map[string]interface{}{"task_id": "cccccccc-0000-4000-8000-000000000001"},
	}
	require.NoError(t, store.CreateNode(ctx, node))

	// 第一次更新（版本1 -> 2）
	v1 := int64(1)
	update1 := GraphNodeUpdate{
		State:           string(ActionStateRunning),
		ExpectedVersion: &v1,
	}
	err := store.UpdateNode(ctx, "33333333-3333-4333-8333-333333333333", update1)
	require.NoError(t, err, "版本匹配时更新应该成功")

	// 第二次更新，但使用旧版本号（应该失败）
	update2 := GraphNodeUpdate{
		State:           string(ActionStateDone),
		ExpectedVersion: &v1, // 还是用版本1，但实际已经是版本2
	}
	err = store.UpdateNode(ctx, "33333333-3333-4333-8333-333333333333", update2)
	assert.Error(t, err, "版本冲突时更新应该失败")
	assert.Contains(t, err.Error(), "version mismatch", "应该包含版本冲突错误信息")

	// 验证状态未被错误更新
	node, _ = store.GetNode(ctx, "33333333-3333-4333-8333-333333333333")
	assert.Equal(t, string(ActionStateRunning), node.State, "版本冲突时状态不应该被更新")
	assert.Equal(t, int64(2), node.Version)
}

// TestPostgresGraphStore_CompareAndSwapState 测试 CAS 操作
func TestPostgresGraphStore_CompareAndSwapState(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	defer pool.Close()

	store := NewPostgresGraphStore(pool)

	// 创建节点
	node := &GraphNode{
		ID:       "44444444-4444-4444-8444-444444444444",
		Kind:     string(KindAction),
		Content:  json.RawMessage(`{}`),
		State:    string(ActionStateOpen),
		Metadata: map[string]interface{}{"task_id": "cccccccc-0000-4000-8000-000000000001"},
	}
	require.NoError(t, store.CreateNode(ctx, node))

	t.Run("CAS成功", func(t *testing.T) {
		swapped, err := store.CompareAndSwapState(
			ctx, "cccccccc-0000-4000-8000-000000000001", "44444444-4444-4444-8444-444444444444",
			string(ActionStateOpen),
			string(ActionStateRunning),
		)
		require.NoError(t, err)
		assert.True(t, swapped, "状态匹配时CAS应该成功")

		// 验证状态已更新
		updated, _ := store.GetNode(ctx, "44444444-4444-4444-8444-444444444444")
		assert.Equal(t, string(ActionStateRunning), updated.State)
	})

	t.Run("CAS失败", func(t *testing.T) {
		swapped, err := store.CompareAndSwapState(
			ctx, "cccccccc-0000-4000-8000-000000000001", "44444444-4444-4444-8444-444444444444",
			string(ActionStateOpen), // 期望是 open，但实际是 running
			string(ActionStateDone),
		)
		require.NoError(t, err)
		assert.False(t, swapped, "状态不匹配时CAS应该失败")

		// 验证状态未改变
		node, _ := store.GetNode(ctx, "44444444-4444-4444-8444-444444444444")
		assert.Equal(t, string(ActionStateRunning), node.State)
	})
}

// TestPostgresGraphStore_ListNodes 测试节点列表查询
func TestPostgresGraphStore_ListNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	defer pool.Close()

	store := NewPostgresGraphStore(pool)

	// 创建多个节点
	nodes := []*GraphNode{
		{
			ID:       "aaaaaaaa-0000-4000-8000-000000000001",
			Kind:     string(KindAction),
			Content:  json.RawMessage(`{}`),
			State:    string(ActionStateOpen),
			Metadata: map[string]interface{}{"task_id": "cccccccc-0000-4000-8000-000000000001", "priority": "high"},
		},
		{
			ID:       "aaaaaaaa-0000-4000-8000-000000000002",
			Kind:     string(KindObjective),
			Content:  json.RawMessage(`{}`),
			State:    string(ActionStateRunning),
			Metadata: map[string]interface{}{"task_id": "cccccccc-0000-4000-8000-000000000001", "priority": "medium"},
		},
		{
			ID:       "aaaaaaaa-0000-4000-8000-000000000003",
			Kind:     string(KindAction),
			Content:  json.RawMessage(`{}`),
			State:    string(ActionStateDone),
			Metadata: map[string]interface{}{"task_id": "cccccccc-0000-4000-8000-000000000002", "priority": "high"},
		},
	}

	for _, n := range nodes {
		require.NoError(t, store.CreateNode(ctx, n))
	}

	t.Run("按Kind查询", func(t *testing.T) {
		query := GraphNodeQuery{
			Kind: string(KindAction),
		}
		result, err := store.ListNodes(ctx, query)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(result), 2, "至少应该有2个Action节点")
	})

	t.Run("按State查询", func(t *testing.T) {
		query := GraphNodeQuery{
			State: string(ActionStateOpen),
		}
		result, err := store.ListNodes(ctx, query)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(result), 1, "至少应该有1个Open状态的节点")
	})

	t.Run("按Metadata过滤", func(t *testing.T) {
		query := GraphNodeQuery{
			Filters: map[string]interface{}{"metadata.task_id": "cccccccc-0000-4000-8000-000000000001"},
		}
		result, err := store.ListNodes(ctx, query)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(result), 2, "task-1应该至少有2个节点")
	})

	t.Run("组合查询", func(t *testing.T) {
		query := GraphNodeQuery{
			Kind:    string(KindAction),
			State:   string(ActionStateOpen),
			Filters: map[string]interface{}{"metadata.task_id": "cccccccc-0000-4000-8000-000000000001"},
		}
		result, err := store.ListNodes(ctx, query)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(result), 1, "task-1的Action节点至少有1个")
	})

	t.Run("Limit限制", func(t *testing.T) {
		query := GraphNodeQuery{
			Limit: 2,
		}
		result, err := store.ListNodes(ctx, query)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(result), 2, "Limit=2时最多返回2个节点")
	})
}

// TestPostgresGraphStore_CreateAndListEdges 测试边的创建和查询
func TestPostgresGraphStore_CreateAndListEdges(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	defer pool.Close()

	store := NewPostgresGraphStore(pool)

	// 创建节点
	nodes := []*GraphNode{
		{
			ID:       "bbbbbbbb-0000-4000-8000-000000000001",
			Kind:     string(KindAction),
			State:    string(ActionStateOpen),
			Content:  json.RawMessage(`{}`),
			Metadata: map[string]interface{}{"task_id": "cccccccc-0000-4000-8000-000000000001"},
		},
		{
			ID:       "bbbbbbbb-0000-4000-8000-000000000002",
			Kind:     string(KindObservation),
			Content:  json.RawMessage(`{}`),
			Metadata: map[string]interface{}{"task_id": "cccccccc-0000-4000-8000-000000000001"},
		},
	}
	for _, n := range nodes {
		require.NoError(t, store.CreateNode(ctx, n))
	}

	// 创建边
	edge := &GraphEdge{
		TaskID:   "cccccccc-0000-4000-8000-000000000001",
		From:     "bbbbbbbb-0000-4000-8000-000000000001",
		To:       "bbbbbbbb-0000-4000-8000-000000000002",
		Relation: string(RelationGenerates),
	}
	err := store.CreateEdge(ctx, edge)
	require.NoError(t, err, "创建边应该成功")

	// 查询边
	edges, err := store.ListEdges(ctx, GraphEdgeQuery{From: "bbbbbbbb-0000-4000-8000-000000000001"})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(edges), 1, "应该至少有1条边")

	found := false
	for _, e := range edges {
		if e.From == "bbbbbbbb-0000-4000-8000-000000000001" && e.To == "bbbbbbbb-0000-4000-8000-000000000002" && e.Relation == string(RelationGenerates) {
			found = true
			break
		}
	}
	assert.True(t, found, "应该找到创建的边")
}

// TestPostgresGraphStore_CreateNodeWithEdges 测试事务性创建
func TestPostgresGraphStore_CreateNodeWithEdges(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	defer pool.Close()

	store := NewPostgresGraphStore(pool)

	// 创建一个已存在的节点
	existingNode := &GraphNode{
		ID:       "ffffffff-0000-4000-8000-000000000001",
		Kind:     string(KindAction),
		State:    string(ActionStateOpen),
		Content:  json.RawMessage(`{}`),
		Metadata: map[string]interface{}{"task_id": "cccccccc-0000-4000-8000-000000000001"},
	}
	require.NoError(t, store.CreateNode(ctx, existingNode))

	// 事务性创建新节点及其边
	newNode := &GraphNode{
		ID:       "ffffffff-0000-4000-8000-000000000002",
		Kind:     string(KindObservation),
		Content:  json.RawMessage(`{}`),
		Metadata: map[string]interface{}{"task_id": "cccccccc-0000-4000-8000-000000000001"},
	}

	edges := []*GraphEdge{
		{
			TaskID:   "cccccccc-0000-4000-8000-000000000001",
			From:     "ffffffff-0000-4000-8000-000000000001",
			To:       "ffffffff-0000-4000-8000-000000000002",
			Relation: string(RelationGenerates),
		},
	}

	err := store.CreateNodeWithEdges(ctx, newNode, edges)
	require.NoError(t, err, "事务性创建应该成功")

	// 验证节点已创建
	node, err := store.GetNode(ctx, "ffffffff-0000-4000-8000-000000000002")
	require.NoError(t, err)
	assert.Equal(t, "ffffffff-0000-4000-8000-000000000002", node.ID)

	// 验证边已创建
	allEdges, err := store.ListEdges(ctx, GraphEdgeQuery{From: "ffffffff-0000-4000-8000-000000000001"})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(allEdges), 1, "应该至少有1条边")
}

// TestPostgresGraphStore_DeleteNode 测试节点删除
func TestPostgresGraphStore_DeleteNode(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	defer pool.Close()

	store := NewPostgresGraphStore(pool)

	// 创建节点
	node := &GraphNode{
		ID:       "dddddddd-0000-4000-8000-000000000001",
		Kind:     string(KindAction),
		State:    string(ActionStateOpen),
		Content:  json.RawMessage(`{}`),
		Metadata: map[string]interface{}{"task_id": "cccccccc-0000-4000-8000-000000000001"},
	}
	require.NoError(t, store.CreateNode(ctx, node))

	// 删除节点
	err := store.DeleteNode(ctx, "dddddddd-0000-4000-8000-000000000001")
	require.NoError(t, err, "删除节点应该成功")

	// 验证节点已删除
	_, err = store.GetNode(ctx, "dddddddd-0000-4000-8000-000000000001")
	assert.ErrorIs(t, err, ErrGraphNodeNotFound, "节点应该已被删除")
}

// TestPostgresGraphStore_DeleteEdge 测试边删除
func TestPostgresGraphStore_DeleteEdge(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	defer pool.Close()

	store := NewPostgresGraphStore(pool)

	// 创建节点和边
	nodes := []*GraphNode{
		{ID: "eeeeeeee-0000-4000-8000-000000000001", Kind: string(KindAction), State: string(ActionStateOpen), Content: json.RawMessage(`{}`), Metadata: map[string]interface{}{"task_id": "cccccccc-0000-4000-8000-000000000001"}},
		{ID: "eeeeeeee-0000-4000-8000-000000000002", Kind: string(KindObservation), Content: json.RawMessage(`{}`), Metadata: map[string]interface{}{"task_id": "cccccccc-0000-4000-8000-000000000001"}},
	}
	for _, n := range nodes {
		require.NoError(t, store.CreateNode(ctx, n))
	}

	edge := &GraphEdge{
		TaskID:   "cccccccc-0000-4000-8000-000000000001",
		From:     "eeeeeeee-0000-4000-8000-000000000001",
		To:       "eeeeeeee-0000-4000-8000-000000000002",
		Relation: string(RelationGenerates),
	}
	require.NoError(t, store.CreateEdge(ctx, edge))

	// 删除边
	err := store.DeleteEdge(ctx, "eeeeeeee-0000-4000-8000-000000000001", "eeeeeeee-0000-4000-8000-000000000002", string(RelationGenerates))
	require.NoError(t, err, "删除边应该成功")

	// 验证边已删除
	edges, err := store.ListEdges(ctx, GraphEdgeQuery{From: "eeeeeeee-0000-4000-8000-000000000001"})
	require.NoError(t, err)

	found := false
	for _, e := range edges {
		if e.From == "eeeeeeee-0000-4000-8000-000000000001" && e.To == "eeeeeeee-0000-4000-8000-000000000002" {
			found = true
			break
		}
	}
	assert.False(t, found, "边应该已被删除")
}
