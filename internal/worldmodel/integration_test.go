package worldmodel

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTaskIsolation 测试 task 级别的世界模型隔离
func TestTaskIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := setupTestDB(t)
	defer pool.Close()

	store := NewStore(pool)

	taskA := "task-a"
	taskB := "task-b"

	// taskA 创建 objective 节点
	objectiveA := Node{
		ID:         "obj-a",
		TaskID:     taskA,
		Kind:       KindObjective,
		Content:    json.RawMessage(`{"target_ref":{"domain":"web","ref_kind":"endpoint","locator":"http://example-a.com"}}`),
		Priority:   5,
		SourceType: "user",
		SourceID:   "task_init",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	_, err := store.CreateNode(ctx, objectiveA)
	require.NoError(t, err)

	// taskB 创建 objective 节点
	objectiveB := Node{
		ID:         "obj-b",
		TaskID:     taskB,
		Kind:       KindObjective,
		Content:    json.RawMessage(`{"target_ref":{"domain":"web","ref_kind":"endpoint","locator":"http://example-b.com"}}`),
		Priority:   5,
		SourceType: "user",
		SourceID:   "task_init",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	_, err = store.CreateNode(ctx, objectiveB)
	require.NoError(t, err)

	// taskA 创建 move 节点
	complexity := ComplexitySimple
	moveA := Node{
		ID:         "move-a",
		TaskID:     taskA,
		Kind:       KindMove,
		Content:    json.RawMessage(`{"instruction":"测试 taskA 的目标"}`),
		State:      stringPtr(StateOpen),
		Complexity: &complexity,
		Priority:   5,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	_, err = store.CreateNode(ctx, moveA)
	require.NoError(t, err)

	// taskB 创建 move 节点
	moveB := Node{
		ID:         "move-b",
		TaskID:     taskB,
		Kind:       KindMove,
		Content:    json.RawMessage(`{"instruction":"测试 taskB 的目标"}`),
		State:      stringPtr(StateOpen),
		Complexity: &complexity,
		Priority:   5,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	_, err = store.CreateNode(ctx, moveB)
	require.NoError(t, err)

	// taskA 只能看到自己的节点
	nodesA, err := store.ListNodesByTask(ctx, taskA)
	require.NoError(t, err)
	assert.Len(t, nodesA, 2) // 1 objective + 1 move
	for _, n := range nodesA {
		assert.Equal(t, taskA, n.TaskID)
	}

	// taskB 只能看到自己的节点
	nodesB, err := store.ListNodesByTask(ctx, taskB)
	require.NoError(t, err)
	assert.Len(t, nodesB, 2) // 1 objective + 1 move
	for _, n := range nodesB {
		assert.Equal(t, taskB, n.TaskID)
	}

	// taskA 查询 open moves
	movesA, err := store.ListOpenMoves(ctx, taskA)
	require.NoError(t, err)
	assert.Len(t, movesA, 1)
	assert.Equal(t, "move-a", movesA[0].ID)

	// taskB 查询 open moves
	movesB, err := store.ListOpenMoves(ctx, taskB)
	require.NoError(t, err)
	assert.Len(t, movesB, 1)
	assert.Equal(t, "move-b", movesB[0].ID)
}

// TestCompleteDataFlow 测试完整数据流：objective → move → observation → discovery
func TestCompleteDataFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := setupTestDB(t)
	defer pool.Close()

	store := NewStore(pool)

	taskID := "task-flow"

	// 1. 创建 objective（用户目标）
	objective := Node{
		ID:         "obj-1",
		TaskID:     taskID,
		Kind:       KindObjective,
		Content:    json.RawMessage(`{"target_ref":{"domain":"web","ref_kind":"endpoint","locator":"http://example.com"}}`),
		Priority:   5,
		SourceType: "user",
		SourceID:   "task_init",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	_, err := store.CreateNode(ctx, objective)
	require.NoError(t, err)

	// 2. PlannerAgent 生成 move（执行计划）
	complexity := ComplexityModerate
	move := Node{
		ID:         "move-1",
		TaskID:     taskID,
		Kind:       KindMove,
		Content:    json.RawMessage(`{"instruction":"扫描目标端点","target_ref":{"domain":"web","ref_kind":"endpoint","locator":"http://example.com"}}`),
		State:      stringPtr(StateOpen),
		Complexity: &complexity,
		Priority:   8,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	_, err = store.CreateNode(ctx, move)
	require.NoError(t, err)

	// 3. ExecutionLoop 执行 move → 产出 observation
	err = store.UpdateMoveState(ctx, move.ID, StateRunning)
	require.NoError(t, err)

	confidence := ConfidenceUnverified
	observation := Node{
		ID:         "obs-1",
		TaskID:     taskID,
		Kind:       KindObservation,
		Content:    json.RawMessage(`{"detail":"发现目录 /admin"}`),
		Confidence: &confidence,
		Priority:   5,
		SourceType: "executor",
		SourceID:   "executor-1",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	_, err = store.CreateNode(ctx, observation)
	require.NoError(t, err)

	// 创建边：move produces observation
	err = store.CreateEdge(ctx, Edge{
		FromID:    move.ID,
		ToID:      observation.ID,
		Kind:      EdgeProduces,
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	// 4. Verifier 验证通过 → 晋升为 discovery
	verifiedConf := ConfidenceVerified
	discovery := Node{
		ID:         "disc-1",
		TaskID:     taskID,
		Kind:       KindDiscovery,
		Content:    json.RawMessage(`{"type":"vulnerability","finding_id":"finding-1","severity":"medium","summary":"未授权访问 /admin"}`),
		Confidence: &verifiedConf,
		Priority:   8,
		SourceType: "verifier",
		SourceID:   "verifier-1",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	_, err = store.CreateNode(ctx, discovery)
	require.NoError(t, err)

	// 创建边：observation supports discovery
	err = store.CreateEdge(ctx, Edge{
		FromID:    observation.ID,
		ToID:      discovery.ID,
		Kind:      EdgeSupports,
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	// 5. Move 执行完成
	now := time.Now()
	err = store.UpdateMoveState(ctx, move.ID, StateDone)
	require.NoError(t, err)
	err = store.UpdateMoveCompleted(ctx, move.ID, now)
	require.NoError(t, err)

	// 验证完整链路
	nodes, err := store.ListNodesByTask(ctx, taskID)
	require.NoError(t, err)
	assert.Len(t, nodes, 4) // objective + move + observation + discovery

	// 验证节点类型
	kindCount := make(map[NodeKind]int)
	for _, n := range nodes {
		kindCount[n.Kind]++
	}
	assert.Equal(t, 1, kindCount[KindObjective])
	assert.Equal(t, 1, kindCount[KindMove])
	assert.Equal(t, 1, kindCount[KindObservation])
	assert.Equal(t, 1, kindCount[KindDiscovery])

	// 验证边关系
	edges, err := store.ListEdgesByTask(ctx, taskID)
	require.NoError(t, err)
	assert.Len(t, edges, 2) // move→observation + observation→discovery

	// 验证 verified discoveries
	discoveries, err := store.ListVerifiedDiscoveries(ctx, taskID)
	require.NoError(t, err)
	assert.Len(t, discoveries, 1)
	assert.Equal(t, "disc-1", discoveries[0].ID)
}

// TestMoveDependency 测试 Move 依赖关系
func TestMoveDependency(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := setupTestDB(t)
	defer pool.Close()

	store := NewStore(pool)

	taskID := "task-dep"

	complexity := ComplexitySimple

	// move-1：无依赖
	move1 := Node{
		ID:         "move-1",
		TaskID:     taskID,
		Kind:       KindMove,
		Content:    json.RawMessage(`{"instruction":"第一步：扫描"}`),
		State:      stringPtr(StateOpen),
		Complexity: &complexity,
		Priority:   5,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	_, err := store.CreateNode(ctx, move1)
	require.NoError(t, err)

	// move-2：依赖 move-1
	move2 := Node{
		ID:         "move-2",
		TaskID:     taskID,
		Kind:       KindMove,
		Content:    json.RawMessage(`{"instruction":"第二步：利用"}`),
		State:      stringPtr(StateOpen),
		Complexity: &complexity,
		DependsOn:  []string{"move-1"},
		Priority:   5,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	_, err = store.CreateNode(ctx, move2)
	require.NoError(t, err)

	// 查询 open moves
	moves, err := store.ListOpenMoves(ctx, taskID)
	require.NoError(t, err)
	assert.Len(t, moves, 2)

	// move-1 完成
	err = store.UpdateMoveState(ctx, "move-1", StateDone)
	require.NoError(t, err)

	// 再次查询，只有 move-2
	moves, err = store.ListOpenMoves(ctx, taskID)
	require.NoError(t, err)
	assert.Len(t, moves, 1)
	assert.Equal(t, "move-2", moves[0].ID)
}

// setupTestDB 设置测试数据库连接
func setupTestDB(t *testing.T) *pgxpool.Pool {
	dsn := "postgres://postgres:postgres@localhost:5432/liusha_test?sslmode=disable"

	pool, err := pgxpool.New(context.Background(), dsn)
	require.NoError(t, err, "连接测试数据库失败，请确保 PostgreSQL 运行在 localhost:5432，数据库名为 liusha_test")

	// 清理测试数据（按依赖顺序）
	_, err = pool.Exec(context.Background(), "TRUNCATE TABLE wm_edge CASCADE")
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), "TRUNCATE TABLE wm_node CASCADE")
	require.NoError(t, err)

	return pool
}

func stringPtr(s string) *string {
	return &s
}
