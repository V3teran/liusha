//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGraphStore_CreateAndGetNode 测试 PostgreSQL 实现的节点创建和获取
func TestGraphStore_CreateAndGetNode(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := newGraphPool(t)
	defer pool.Close()

	store := NewGraphStore(pool)

	// 创建节点
	node := &core.GraphNode{
		ID:      "11111111-1111-4111-8111-111111111111",
		Kind:    string(core.KindAction),
		Content: json.RawMessage(`{"title":"test action"}`),
		TaskID:  "cccccccc-0000-4000-8000-000000000001", Metadata: map[string]interface{}{},
		State: string(core.NodeStateOpen),
	}

	err := store.CreateNode(ctx, node)
	require.NoError(t, err, "创建节点应该成功")

	// 获取节点
	retrieved, err := store.GetNode(ctx, "11111111-1111-4111-8111-111111111111")
	require.NoError(t, err, "获取节点应该成功")
	assert.Equal(t, "11111111-1111-4111-8111-111111111111", retrieved.ID)
	assert.Equal(t, string(core.KindAction), retrieved.Kind)
	assert.Equal(t, string(core.NodeStateOpen), retrieved.State)
	assert.JSONEq(t, `{"title":"test action"}`, string(retrieved.Content))
	assert.Equal(t, int64(1), retrieved.Version, "初始版本应该是1")
}

// TestGraphStore_UpdateNode 测试节点更新
func TestGraphStore_UpdateNode(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := newGraphPool(t)
	defer pool.Close()

	store := NewGraphStore(pool)

	// 创建初始节点
	node := &core.GraphNode{
		ID:      "22222222-2222-4222-8222-222222222222",
		Kind:    string(core.KindAction),
		Content: json.RawMessage(`{"title":"initial"}`),
		State:   string(core.NodeStateOpen),
		TaskID:  "cccccccc-0000-4000-8000-000000000001", Metadata: map[string]interface{}{},
	}
	require.NoError(t, store.CreateNode(ctx, node))

	// 更新节点
	update := core.GraphNodeUpdate{
		State:   string(core.NodeStateRunning),
		Content: json.RawMessage(`{"title":"updated"}`),
	}
	err := store.UpdateNode(ctx, "22222222-2222-4222-8222-222222222222", update)
	require.NoError(t, err, "更新节点应该成功")

	// 验证更新
	updated, err := store.GetNode(ctx, "22222222-2222-4222-8222-222222222222")
	require.NoError(t, err)
	assert.Equal(t, string(core.NodeStateRunning), updated.State)
	assert.JSONEq(t, `{"title":"updated"}`, string(updated.Content))
	assert.Equal(t, int64(2), updated.Version, "版本号应该递增")
}

// TestGraphStore_OptimisticLocking 测试乐观锁
func TestGraphStore_OptimisticLocking(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := newGraphPool(t)
	defer pool.Close()

	store := NewGraphStore(pool)

	// 创建节点
	node := &core.GraphNode{
		ID:      "33333333-3333-4333-8333-333333333333",
		Kind:    string(core.KindAction),
		Content: json.RawMessage(`{}`),
		State:   string(core.NodeStateOpen),
		TaskID:  "cccccccc-0000-4000-8000-000000000001", Metadata: map[string]interface{}{},
	}
	require.NoError(t, store.CreateNode(ctx, node))

	// 第一次更新（版本1 -> 2）
	v1 := int64(1)
	update1 := core.GraphNodeUpdate{
		State:           string(core.NodeStateRunning),
		ExpectedVersion: &v1,
	}
	err := store.UpdateNode(ctx, "33333333-3333-4333-8333-333333333333", update1)
	require.NoError(t, err, "版本匹配时更新应该成功")

	// 第二次更新，但使用旧版本号（应该失败）
	update2 := core.GraphNodeUpdate{
		State:           string(core.NodeStateCompleted),
		ExpectedVersion: &v1, // 还是用版本1，但实际已经是版本2
	}
	err = store.UpdateNode(ctx, "33333333-3333-4333-8333-333333333333", update2)
	assert.Error(t, err, "版本冲突时更新应该失败")
	assert.Contains(t, err.Error(), "version mismatch", "应该包含版本冲突错误信息")

	// 验证状态未被错误更新
	node, _ = store.GetNode(ctx, "33333333-3333-4333-8333-333333333333")
	assert.Equal(t, string(core.NodeStateRunning), node.State, "版本冲突时状态不应该被更新")
	assert.Equal(t, int64(2), node.Version)
}

// TestGraphStore_GuardedTransition 测试条件迁移：状态前置与 Guard 都须成立
func TestGraphStore_GuardedTransition(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := newGraphPool(t)
	defer pool.Close()

	store := NewGraphStore(pool)
	const taskID, nodeID = "cccccccc-0000-4000-8000-000000000001", "44444444-4444-4444-8444-444444444444"

	node := &core.GraphNode{
		ID:      nodeID,
		Kind:    string(core.KindAction),
		Content: json.RawMessage(`{}`),
		State:   string(core.NodeStateRunning),
		TaskID:  taskID, Metadata: map[string]interface{}{"claimed_by": "run-1"},
	}
	require.NoError(t, store.CreateNode(ctx, node))

	transition := func(from string, guard map[string]string) error {
		return store.ApplyChangeSet(ctx, core.GraphChangeSet{Transitions: []core.GraphStateTransition{{
			TaskID: taskID, NodeID: nodeID, From: from, To: string(core.NodeStateCompleted), Guard: guard,
		}}})
	}

	assert.ErrorIs(t, transition(string(core.NodeStateOpen), nil), core.ErrGraphStateConflict, "状态前置不成立")
	assert.ErrorIs(t, transition(string(core.NodeStateRunning), map[string]string{"claimed_by": "run-2"}),
		core.ErrGraphStateConflict, "Guard 不成立")
	require.NoError(t, transition(string(core.NodeStateRunning), map[string]string{"claimed_by": "run-1"}))

	updated, err := store.GetNode(ctx, nodeID)
	require.NoError(t, err)
	assert.Equal(t, string(core.NodeStateCompleted), updated.State)
}

// TestGraphStore_ListNodes 测试节点列表查询
func TestGraphStore_ListNodes(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := newGraphPool(t)
	defer pool.Close()

	store := NewGraphStore(pool)

	// 创建多个节点
	nodes := []*core.GraphNode{
		{
			ID:      "aaaaaaaa-0000-4000-8000-000000000001",
			Kind:    string(core.KindAction),
			Content: json.RawMessage(`{}`),
			State:   string(core.NodeStateOpen),
			TaskID:  "cccccccc-0000-4000-8000-000000000001", Metadata: map[string]interface{}{"priority": "high"},
		},
		{
			ID:      "aaaaaaaa-0000-4000-8000-000000000002",
			Kind:    string(core.KindObjective),
			Content: json.RawMessage(`{}`),
			State:   string(core.NodeStateRunning),
			TaskID:  "cccccccc-0000-4000-8000-000000000001", Metadata: map[string]interface{}{"priority": "medium"},
		},
		{
			ID:      "aaaaaaaa-0000-4000-8000-000000000003",
			Kind:    string(core.KindAction),
			Content: json.RawMessage(`{}`),
			State:   string(core.NodeStateCompleted),
			TaskID:  "cccccccc-0000-4000-8000-000000000002", Metadata: map[string]interface{}{"priority": "high"},
		},
	}

	for _, n := range nodes {
		require.NoError(t, store.CreateNode(ctx, n))
	}

	t.Run("按Kind查询", func(t *testing.T) {
		query := core.GraphNodeQuery{
			Kind: string(core.KindAction),
		}
		result, err := store.ListNodes(ctx, query)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(result), 2, "至少应该有2个Action节点")
	})

	t.Run("按State查询", func(t *testing.T) {
		query := core.GraphNodeQuery{
			State: string(core.NodeStateOpen),
		}
		result, err := store.ListNodes(ctx, query)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(result), 1, "至少应该有1个Open状态的节点")
	})

	t.Run("按Metadata过滤", func(t *testing.T) {
		query := core.GraphNodeQuery{
			Filters: map[string]interface{}{"task_id": "cccccccc-0000-4000-8000-000000000001"},
		}
		result, err := store.ListNodes(ctx, query)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(result), 2, "task-1应该至少有2个节点")
	})

	t.Run("组合查询", func(t *testing.T) {
		query := core.GraphNodeQuery{
			Kind:    string(core.KindAction),
			State:   string(core.NodeStateOpen),
			Filters: map[string]interface{}{"task_id": "cccccccc-0000-4000-8000-000000000001"},
		}
		result, err := store.ListNodes(ctx, query)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(result), 1, "task-1的Action节点至少有1个")
	})

	t.Run("Limit限制", func(t *testing.T) {
		query := core.GraphNodeQuery{
			Limit: 2,
		}
		result, err := store.ListNodes(ctx, query)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(result), 2, "Limit=2时最多返回2个节点")
	})
}

// TestGraphStore_CreateAndListEdges 测试边的创建和查询
func TestGraphStore_CreateAndListEdges(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := newGraphPool(t)
	defer pool.Close()

	store := NewGraphStore(pool)

	// 创建节点
	nodes := []*core.GraphNode{
		{
			ID:      "bbbbbbbb-0000-4000-8000-000000000001",
			Kind:    string(core.KindAction),
			State:   string(core.NodeStateOpen),
			Content: json.RawMessage(`{}`),
			TaskID:  "cccccccc-0000-4000-8000-000000000001", Metadata: map[string]interface{}{},
		},
		{
			ID:      "bbbbbbbb-0000-4000-8000-000000000002",
			Kind:    string(core.KindObservation),
			Content: json.RawMessage(`{}`),
			TaskID:  "cccccccc-0000-4000-8000-000000000001", Metadata: map[string]interface{}{},
		},
	}
	for _, n := range nodes {
		require.NoError(t, store.CreateNode(ctx, n))
	}

	// 创建边
	edge := &core.GraphEdge{
		TaskID:   "cccccccc-0000-4000-8000-000000000001",
		From:     "bbbbbbbb-0000-4000-8000-000000000001",
		To:       "bbbbbbbb-0000-4000-8000-000000000002",
		Relation: "generates",
	}
	err := store.CreateEdge(ctx, edge)
	require.NoError(t, err, "创建边应该成功")

	// 查询边
	edges, err := store.ListEdges(ctx, core.GraphEdgeQuery{From: "bbbbbbbb-0000-4000-8000-000000000001"})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(edges), 1, "应该至少有1条边")

	found := false
	for _, e := range edges {
		if e.From == "bbbbbbbb-0000-4000-8000-000000000001" && e.To == "bbbbbbbb-0000-4000-8000-000000000002" && e.Relation == "generates" {
			found = true
			break
		}
	}
	assert.True(t, found, "应该找到创建的边")
}

// TestGraphStore_CreateNodeWithEdges 测试事务性创建
func TestGraphStore_CreateNodeWithEdges(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := newGraphPool(t)
	defer pool.Close()

	store := NewGraphStore(pool)

	// 创建一个已存在的节点
	existingNode := &core.GraphNode{
		ID:      "ffffffff-0000-4000-8000-000000000001",
		Kind:    string(core.KindAction),
		State:   string(core.NodeStateOpen),
		Content: json.RawMessage(`{}`),
		TaskID:  "cccccccc-0000-4000-8000-000000000001", Metadata: map[string]interface{}{},
	}
	require.NoError(t, store.CreateNode(ctx, existingNode))

	// 事务性创建新节点及其边
	newNode := &core.GraphNode{
		ID:      "ffffffff-0000-4000-8000-000000000002",
		Kind:    string(core.KindObservation),
		Content: json.RawMessage(`{}`),
		TaskID:  "cccccccc-0000-4000-8000-000000000001", Metadata: map[string]interface{}{},
	}

	edges := []*core.GraphEdge{
		{
			TaskID:   "cccccccc-0000-4000-8000-000000000001",
			From:     "ffffffff-0000-4000-8000-000000000001",
			To:       "ffffffff-0000-4000-8000-000000000002",
			Relation: "generates",
		},
	}

	err := store.CreateNodeWithEdges(ctx, newNode, edges)
	require.NoError(t, err, "事务性创建应该成功")

	// 验证节点已创建
	node, err := store.GetNode(ctx, "ffffffff-0000-4000-8000-000000000002")
	require.NoError(t, err)
	assert.Equal(t, "ffffffff-0000-4000-8000-000000000002", node.ID)

	// 验证边已创建
	allEdges, err := store.ListEdges(ctx, core.GraphEdgeQuery{From: "ffffffff-0000-4000-8000-000000000001"})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(allEdges), 1, "应该至少有1条边")
}

// TestGraphStore_DeleteNode 测试节点删除
func TestGraphStore_DeleteNode(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := newGraphPool(t)
	defer pool.Close()

	store := NewGraphStore(pool)

	// 创建节点
	node := &core.GraphNode{
		ID:      "dddddddd-0000-4000-8000-000000000001",
		Kind:    string(core.KindAction),
		State:   string(core.NodeStateOpen),
		Content: json.RawMessage(`{}`),
		TaskID:  "cccccccc-0000-4000-8000-000000000001", Metadata: map[string]interface{}{},
	}
	require.NoError(t, store.CreateNode(ctx, node))

	// 删除节点
	err := store.DeleteNode(ctx, "dddddddd-0000-4000-8000-000000000001")
	require.NoError(t, err, "删除节点应该成功")

	// 验证节点已删除
	_, err = store.GetNode(ctx, "dddddddd-0000-4000-8000-000000000001")
	assert.ErrorIs(t, err, core.ErrGraphNodeNotFound, "节点应该已被删除")
}

// TestGraphStore_DeleteEdge 测试边删除
func TestGraphStore_DeleteEdge(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := newGraphPool(t)
	defer pool.Close()

	store := NewGraphStore(pool)

	// 创建节点和边
	nodes := []*core.GraphNode{
		{ID: "eeeeeeee-0000-4000-8000-000000000001", Kind: string(core.KindAction), State: string(core.NodeStateOpen), Content: json.RawMessage(`{}`), TaskID: "cccccccc-0000-4000-8000-000000000001", Metadata: map[string]interface{}{}},
		{ID: "eeeeeeee-0000-4000-8000-000000000002", Kind: string(core.KindObservation), Content: json.RawMessage(`{}`), TaskID: "cccccccc-0000-4000-8000-000000000001", Metadata: map[string]interface{}{}},
	}
	for _, n := range nodes {
		require.NoError(t, store.CreateNode(ctx, n))
	}

	edge := &core.GraphEdge{
		TaskID:   "cccccccc-0000-4000-8000-000000000001",
		From:     "eeeeeeee-0000-4000-8000-000000000001",
		To:       "eeeeeeee-0000-4000-8000-000000000002",
		Relation: "generates",
	}
	require.NoError(t, store.CreateEdge(ctx, edge))

	// 删除边
	err := store.DeleteEdge(ctx, "eeeeeeee-0000-4000-8000-000000000001", "eeeeeeee-0000-4000-8000-000000000002", "generates")
	require.NoError(t, err, "删除边应该成功")

	// 验证边已删除
	edges, err := store.ListEdges(ctx, core.GraphEdgeQuery{From: "eeeeeeee-0000-4000-8000-000000000001"})
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

// TestGraphStore_VersionPrecondition 锁定版本前置在 PG 上的语义：版本过期整批回滚；
// 前置节点在事务内被共享锁定——并发改写它的事务要等变更集提交后才能落定，因此
// 改写之后的语句总能看到变更集的写入（目标调整撤回规划依赖这一点）。
func TestGraphStore_VersionPrecondition(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool := newGraphPool(t)
	defer pool.Close()
	store := NewGraphStore(pool)

	const (
		taskID = "cccccccc-0000-4000-8000-0000000000a1"
		rootID = "21111111-1111-4111-8111-111111111111"
	)
	require.NoError(t, store.CreateNode(ctx, &core.GraphNode{ID: rootID, Kind: string(core.KindObjective),
		Content: json.RawMessage(`{"description":"旧目标"}`), TaskID: taskID,
		Metadata: map[string]interface{}{"source_type": "user"}, State: string(core.NodeStateOpen)}))
	action := func(id string) []*core.GraphNode {
		return []*core.GraphNode{{ID: id, Kind: string(core.KindAction), Content: json.RawMessage(`{"instruction":"x"}`),
			TaskID: taskID, Metadata: map[string]interface{}{"source_type": "planner"}, State: string(core.NodeStateOpen)}}
	}
	basis, err := store.GetNode(ctx, rootID)
	require.NoError(t, err)

	require.NoError(t, store.UpdateNode(ctx, rootID, core.GraphNodeUpdate{Content: json.RawMessage(`{"description":"新目标"}`)}))
	err = store.ApplyChangeSet(ctx, core.GraphChangeSet{
		Preconditions: []core.GraphNodeVersion{{TaskID: taskID, NodeID: rootID, Version: basis.Version}},
		Nodes:         action("31111111-1111-4111-8111-111111111111"),
	})
	require.ErrorIs(t, err, core.ErrVersionMismatch)
	_, err = store.GetNode(ctx, "31111111-1111-4111-8111-111111111111")
	require.ErrorIs(t, err, core.ErrGraphNodeNotFound, "前置不成立时整批回滚")

	err = store.ApplyChangeSet(ctx, core.GraphChangeSet{
		Preconditions: []core.GraphNodeVersion{{TaskID: "cccccccc-0000-4000-8000-0000000000a2", NodeID: rootID, Version: basis.Version + 1}},
	})
	require.ErrorIs(t, err, core.ErrGraphNodeNotFound, "前置节点不属于声明的任务视为不存在")

	// 并发：改写前置节点的事务未提交时，变更集等它落定，再按最新版本判定——不会
	// 基于旧快照放行。
	current, err := store.GetNode(ctx, rootID)
	require.NoError(t, err)
	writer, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = writer.Rollback(ctx) }()
	_, err = writer.Exec(ctx, `UPDATE exploration_node SET content = '{"description":"再改"}' WHERE id = $1`, rootID)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		done <- store.ApplyChangeSet(ctx, core.GraphChangeSet{
			Preconditions: []core.GraphNodeVersion{{TaskID: taskID, NodeID: rootID, Version: current.Version}},
			Nodes:         action("41111111-1111-4111-8111-111111111111"),
		})
	}()
	select {
	case err := <-done:
		t.Fatalf("改写未提交时变更集应等待，却已返回: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	require.NoError(t, writer.Commit(ctx))
	require.ErrorIs(t, <-done, core.ErrVersionMismatch, "等到改写提交后按最新版本判定")
}

// graphTaskIDs 是本文件测试节点所属的任务：节点以外键挂在任务上，先建任务。
var graphTaskIDs = []string{
	"cccccccc-0000-4000-8000-000000000001",
	"cccccccc-0000-4000-8000-000000000002",
	"cccccccc-0000-4000-8000-0000000000a1",
	"cccccccc-0000-4000-8000-0000000000a2",
}

// newGraphPool 起一个全迁移的库并建好测试用到的任务。
func newGraphPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	for _, id := range graphTaskIDs {
		dbtest.SeedTask(t, pool, id)
	}
	return pool
}
