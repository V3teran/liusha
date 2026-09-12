package core

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPostgresGraphStore_Integration 测试 PostgresGraphStore 与业务层的集成
func TestPostgresGraphStore_Integration(t *testing.T) {
	// 从环境变量读取数据库连接
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, skipping integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()

	store := NewPostgresGraphStore(pool)
	taskID := "test-integration-" + uuid.New().String()

	// 清理函数
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM wm_edge WHERE task_id = $1`, taskID)
		_, _ = pool.Exec(ctx, `DELETE FROM wm_node WHERE task_id = $1`, taskID)
	}()

	t.Run("完整认知循环", func(t *testing.T) {
		// 1. 创建 Objective 节点
		objectiveID := uuid.New().String()
		objective := &GraphNode{
			ID:      objectiveID,
			Kind:    string(KindObjective),
			Content: json.RawMessage(`{"description": "扫描目标系统"}`),
			Metadata: map[string]interface{}{
				"task_id": taskID,
			},
		}
		err := store.CreateNode(ctx, objective)
		require.NoError(t, err)

		// 2. 创建 Action 节点
		actionID := uuid.New().String()
		action := &GraphNode{
			ID:      actionID,
			Kind:    string(KindAction),
			Content: json.RawMessage(`{"tool": "nmap", "args": ["-sV", "target"]}`),
			State:   string(ActionStateOpen),
			Metadata: map[string]interface{}{
				"task_id":    taskID,
				"complexity": "simple",
			},
		}
		err = store.CreateNode(ctx, action)
		require.NoError(t, err)

		// 3. 创建 Observation 节点
		observationID := uuid.New().String()
		observation := &GraphNode{
			ID:         observationID,
			Kind:       string(KindObservation),
			Content:    json.RawMessage(`{"ports": [80, 443], "services": ["http", "https"]}`),
			Confidence: 0.95,
			Metadata: map[string]interface{}{
				"task_id": taskID,
			},
		}
		err = store.CreateNode(ctx, observation)
		require.NoError(t, err)

		// 4. 创建 Evaluation 节点
		evaluationID := uuid.New().String()
		evaluation := &GraphNode{
			ID:         evaluationID,
			Kind:       string(KindEvaluation),
			Content:    json.RawMessage(`{"verdict": "confirmed", "reason": "服务响应正常"}`),
			Confidence: 1.0,
			Metadata: map[string]interface{}{
				"task_id": taskID,
			},
		}
		err = store.CreateNode(ctx, evaluation)
		require.NoError(t, err)

		// 5. 创建 Result 节点
		resultID := uuid.New().String()
		result := &GraphNode{
			ID:         resultID,
			Kind:       string(KindResult),
			Content:    json.RawMessage(`{"finding": "发现开放端口 80, 443", "severity": "info"}`),
			Confidence: 1.0,
			Metadata: map[string]interface{}{
				"task_id": taskID,
			},
		}
		err = store.CreateNode(ctx, result)
		require.NoError(t, err)

		// 6. 创建关系边
		edges := []*GraphEdge{
			{
				From:     actionID,
				To:       observationID,
				Relation: string(RelationGenerates),
			},
			{
				From:     observationID,
				To:       objectiveID,
				Relation: string(RelationContributes),
			},
			{
				From:     evaluationID,
				To:       observationID,
				Relation: string(RelationConfirms),
			},
			{
				From:     evaluationID,
				To:       resultID,
				Relation: string(RelationConfirms),
			},
		}

		for _, edge := range edges {
			err := store.CreateEdge(ctx, edge)
			require.NoError(t, err)
		}

		// 7. 验证图结构
		// 从 action 出发，应该能遍历到 observation
		neighbors, err := store.Traverse(ctx, actionID, GraphTraverseQuery{
			Direction: GraphTraverseOut,
			Strategy:  GraphTraverseBFS,
			MaxDepth:  2,
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(neighbors), 1) // 至少包含 action 自己

		// 8. 查询所有节点
		allNodes, err := store.ListNodes(ctx, GraphNodeQuery{
			Filters: map[string]interface{}{
				"metadata.task_id": taskID,
			},
		})
		require.NoError(t, err)
		assert.Len(t, allNodes, 5) // objective + action + observation + evaluation + result

		// 9. 查询所有边
		allEdges, err := store.ListEdges(ctx, GraphEdgeQuery{})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(allEdges), 4)

		// 10. 更新 Action 状态
		err = store.UpdateNode(ctx, actionID, GraphNodeUpdate{
			State: string(ActionStateDone),
		})
		require.NoError(t, err)

		updatedAction, err := store.GetNode(ctx, actionID)
		require.NoError(t, err)
		assert.Equal(t, string(ActionStateDone), updatedAction.State)
	})

	t.Run("查询高置信度节点", func(t *testing.T) {
		nodes, err := store.ListNodes(ctx, GraphNodeQuery{
			MinConfidence: 0.9,
			Filters: map[string]interface{}{
				"metadata.task_id": taskID,
			},
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(nodes), 2) // observation + evaluation + result
	})

	t.Run("按类型过滤", func(t *testing.T) {
		actions, err := store.ListNodes(ctx, GraphNodeQuery{
			Kind: string(KindAction),
			Filters: map[string]interface{}{
				"metadata.task_id": taskID,
			},
		})
		require.NoError(t, err)
		assert.Len(t, actions, 1)
	})
}
