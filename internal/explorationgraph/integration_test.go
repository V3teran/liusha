//go:build integration

package explorationgraph

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

// TestTaskIsolation 测试 task 级别的探索图隔离
func TestTaskIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := setupTestDB(t)
	defer pool.Close()

	store := NewStore(pool)

	taskA := "aaaaaaa1-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	taskB := "bbbbbbb2-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

	// taskA 创建 action 节点
	complexity := ComplexitySimple
	stateOpen := StateOpen
	moveA := Node{
		ID:         "11111111-1111-4111-8111-111111111111",
		TaskID:     taskA,
		Kind:       core.KindAction,
		Content:    json.RawMessage(`{"instruction":"测试 taskA 的目标"}`),
		State:      &stateOpen,
		Complexity: &complexity,
		Priority:   PriorityMedium,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	_, err := store.CreateNode(ctx, moveA)
	require.NoError(t, err)

	// taskB 创建 action 节点
	moveB := Node{
		ID:         "22222222-2222-4222-8222-222222222222",
		TaskID:     taskB,
		Kind:       core.KindAction,
		Content:    json.RawMessage(`{"instruction":"测试 taskB 的目标"}`),
		State:      &stateOpen,
		Complexity: &complexity,
		Priority:   PriorityMedium,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	_, err = store.CreateNode(ctx, moveB)
	require.NoError(t, err)

	// taskA 只能看到自己的 action
	actionsA, err := store.ListOpenActions(ctx, taskA)
	require.NoError(t, err)
	assert.Len(t, actionsA, 1)
	assert.Equal(t, "11111111-1111-4111-8111-111111111111", actionsA[0].ID)

	// taskB 只能看到自己的 action
	actionsB, err := store.ListOpenActions(ctx, taskB)
	require.NoError(t, err)
	assert.Len(t, actionsB, 1)
	assert.Equal(t, "22222222-2222-4222-8222-222222222222", actionsB[0].ID)
}

// TestCompleteDataFlow 测试完整数据流：action → observation → result
func TestCompleteDataFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := setupTestDB(t)
	defer pool.Close()

	store := NewStore(pool)

	taskID := "ccccccc3-cccc-4ccc-8ccc-cccccccccccc"

	// 1. 创建 move
	complexity := ComplexityModerate
	stateOpen := StateOpen
	move := Node{
		ID:         "33333333-3333-4333-8333-333333333333",
		TaskID:     taskID,
		Kind:       core.KindAction,
		Content:    json.RawMessage(`{"instruction":"扫描目标端点"}`),
		State:      &stateOpen,
		Complexity: &complexity,
		Priority:   PriorityHigh,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	_, err := store.CreateNode(ctx, move)
	require.NoError(t, err)

	// 2. 执行 move → 产出 observation（节点与 move→observation 溯源边原子落库）
	err = store.TransitionState(ctx, move.ID, StateRunning, nil)
	require.NoError(t, err)

	confidence := ConfidenceUnverified
	observation := Node{
		ID:         "55555555-5555-4555-8555-555555555555",
		TaskID:     taskID,
		Kind:       core.KindObservation,
		Content:    json.RawMessage(`{"detail":"发现目录 /admin"}`),
		Confidence: &confidence,
		Priority:   PriorityMedium,
		SourceType: "executor",
		SourceID:   "executor-1",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	err = store.CreateNodeWithEdges(ctx, observation, []Edge{{
		SrcID:     move.ID,
		DstID:     observation.ID,
		Rel:       RelGenerates,
		TaskID:    taskID,
		CreatedAt: time.Now(),
	}})
	require.NoError(t, err)

	// 3. 验证通过 → 晋升为 result（复现门产出的唯一终态节点）。
	// 晋升溯源边（observation generates result）与节点原子落库。
	verifiedConf := ConfidenceVerified
	result := Node{
		ID:         "66666666-6666-4666-8666-666666666666",
		TaskID:     taskID,
		Kind:       core.KindResult,
		Content:    json.RawMessage(`{"type":"vulnerability","severity":"medium"}`),
		Confidence: &verifiedConf,
		Priority:   PriorityHigh,
		SourceType: SourceEvaluator,
		SourceID:   "verifier-1",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	err = store.CreateNodeWithEdges(ctx, result, []Edge{{
		SrcID:     observation.ID,
		DstID:     result.ID,
		Rel:       RelGenerates,
		TaskID:    taskID,
		CreatedAt: time.Now(),
	}})
	require.NoError(t, err)

	// 4. Move 完成
	err = store.TransitionState(ctx, move.ID, StateCompleted, nil)
	require.NoError(t, err)

	// 验证已晋升的 result
	results, err := store.ListResults(ctx, taskID)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "66666666-6666-4666-8666-666666666666", results[0].ID)
}

// TestActionDependency 测试 action 依赖关系（depends_on 满足才可执行）
func TestActionDependency(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := setupTestDB(t)
	defer pool.Close()

	store := NewStore(pool)

	taskID := "ddddddd4-dddd-4ddd-8ddd-dddddddddddd"

	complexity := ComplexitySimple
	stateOpen := StateOpen

	// move-1：无依赖
	move1 := Node{
		ID:         "33333333-3333-4333-8333-333333333333",
		TaskID:     taskID,
		Kind:       core.KindAction,
		Content:    json.RawMessage(`{"instruction":"第一步：扫描"}`),
		State:      &stateOpen,
		Complexity: &complexity,
		Priority:   PriorityMedium,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	_, err := store.CreateNode(ctx, move1)
	require.NoError(t, err)

	// move-2：依赖 move-1
	move2 := Node{
		ID:         "44444444-4444-4444-8444-444444444444",
		TaskID:     taskID,
		Kind:       core.KindAction,
		Content:    json.RawMessage(`{"instruction":"第二步：利用"}`),
		State:      &stateOpen,
		Complexity: &complexity,
		DependsOn:  []string{"33333333-3333-4333-8333-333333333333"},
		Priority:   PriorityMedium,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	_, err = store.CreateNode(ctx, move2)
	require.NoError(t, err)

	// 查询 open moves（应该有 2 个）
	actions, err := store.ListOpenActions(ctx, taskID)
	require.NoError(t, err)
	assert.Len(t, actions, 2)

	// move-1 完成
	err = store.TransitionState(ctx, "33333333-3333-4333-8333-333333333333", StateCompleted, nil)
	require.NoError(t, err)

	// 再次查询，只有 move-2 (move-1 已 done)
	actions, err = store.ListOpenActions(ctx, taskID)
	require.NoError(t, err)
	assert.Len(t, actions, 1)
	assert.Equal(t, "44444444-4444-4444-8444-444444444444", actions[0].ID)
}

// setupTestDB 启动一次性 Postgres 容器（含全部迁移），并清理图谱表保证测试隔离。
func setupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := dbtest.NewPgPool(t)

	// 清理测试数据（按依赖顺序）
	_, err := pool.Exec(context.Background(), "TRUNCATE TABLE exploration_edge CASCADE")
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), "TRUNCATE TABLE exploration_node CASCADE")
	require.NoError(t, err)

	return pool
}

// TestCreateNodeWithEdgesAtomicity 验证 Postgres 事务复合写的全有或全无：
// 边引用不存在的节点时整体回滚，不留孤立节点。
func TestCreateNodeWithEdgesAtomicity(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := setupTestDB(t)
	defer pool.Close()

	store := NewStore(pool)

	taskID := "55555555-5555-4555-8555-555555555555"
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM exploration_node WHERE task_id=$1`, taskID)
	}()

	stateOpen := StateOpen
	action := Node{
		ID:         "66666666-6666-4666-8666-666666666666",
		TaskID:     taskID,
		Kind:       core.KindAction,
		Content:    json.RawMessage(`{"instruction":"scan"}`),
		State:      &stateOpen,
		SourceType: SourcePlanner,
	}

	// 正常路径：节点 + 归属边一并落地
	objID := "77777777-7777-4777-8777-777777777777"
	_, err := store.CreateNode(ctx, Node{
		ID:         objID,
		TaskID:     taskID,
		Kind:       core.KindObjective,
		Content:    json.RawMessage(`{"description":"root"}`),
		State:      &stateOpen,
		SourceType: SourceUser,
	})
	require.NoError(t, err)

	edge := Edge{TaskID: taskID, SrcID: objID, Rel: RelGenerates, DstID: action.ID}
	require.NoError(t, store.CreateNodeWithEdges(ctx, action, []Edge{edge}))

	edges, err := store.ListEdgesForAPI(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, edges, 1)
	assert.Equal(t, objID, edges[0].SrcID)

	// 失败路径：边引用幻影节点 → FK 拒绝 → 整体回滚
	orphan := Node{
		ID:         "88888888-8888-4888-8888-888888888888",
		TaskID:     taskID,
		Kind:       core.KindObservation,
		Content:    json.RawMessage(`{"statement":"x"}`),
		SourceType: SourceExecutor,
	}
	badEdge := Edge{TaskID: taskID, SrcID: "99999999-9999-4999-8999-999999999999", Rel: RelGenerates, DstID: orphan.ID}
	err = store.CreateNodeWithEdges(ctx, orphan, []Edge{badEdge})
	require.Error(t, err, "边引用幻影节点应触发 FK 拒绝")

	var orphanCount int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM exploration_node WHERE id=$1`, orphan.ID).Scan(&orphanCount))
	assert.Equal(t, 0, orphanCount, "复合写失败后不得留下孤立节点（事务回滚）")
}
