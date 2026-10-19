//go:build integration

package insight_test

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/insight"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_ListByRelatedNode(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	store := insight.NewStore(pool)

	// 清理测试数据
	_, err := pool.Exec(ctx, "DELETE FROM insight WHERE assignment_id LIKE 'test-assignment-%'")
	require.NoError(t, err)

	assignmentID := "test-assignment-related-node"
	nodeID1 := "node-123"
	nodeID2 := "node-456"

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM insight WHERE assignment_id = $1", assignmentID)
	})

	// 创建测试数据
	insights := []insight.Insight{
		{
			Category:     insight.CategoryBehavior,
			Priority:     insight.PriorityHigh,
			Confidence:   insight.ConfidenceConfirmed,
			Summary:      "发现可疑行为",
			RelatedNodes: []string{nodeID1, nodeID2},
			SourceTaskID: "task-1",
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		},
		{
			Category:     insight.CategoryIssue,
			Priority:     insight.PriorityCritical,
			Confidence:   insight.ConfidenceProbable,
			Summary:      "发现安全问题",
			RelatedNodes: []string{nodeID1},
			SourceTaskID: "task-1",
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		},
		{
			Category:     insight.CategoryNote,
			Priority:     insight.PriorityLow,
			Confidence:   insight.ConfidencePossible,
			Summary:      "其他备注",
			RelatedNodes: []string{nodeID2},
			SourceTaskID: "task-1",
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		},
	}

	// 插入测试数据
	for _, ins := range insights {
		err := store.Append(ctx, assignmentID, ins)
		require.NoError(t, err)
	}

	// 测试：查询包含 nodeID1 的洞察
	t.Run("查询包含node-123的洞察", func(t *testing.T) {
		results, err := store.ListByRelatedNode(ctx, assignmentID, nodeID1, 10)
		require.NoError(t, err)
		assert.Len(t, results, 2, "应该找到 2 个包含 node-123 的洞察")

		// 验证都包含 nodeID1
		for _, r := range results {
			assert.Contains(t, r.RelatedNodes, nodeID1)
		}
	})

	// 测试：查询包含 nodeID2 的洞察
	t.Run("查询包含node-456的洞察", func(t *testing.T) {
		results, err := store.ListByRelatedNode(ctx, assignmentID, nodeID2, 10)
		require.NoError(t, err)
		assert.Len(t, results, 2, "应该找到 2 个包含 node-456 的洞察")

		for _, r := range results {
			assert.Contains(t, r.RelatedNodes, nodeID2)
		}
	})

	// 测试：查询不存在的节点
	t.Run("查询不存在的节点", func(t *testing.T) {
		results, err := store.ListByRelatedNode(ctx, assignmentID, "node-999", 10)
		require.NoError(t, err)
		assert.Empty(t, results, "不应该找到任何结果")
	})

	// 测试：参数验证
	t.Run("参数验证", func(t *testing.T) {
		_, err := store.ListByRelatedNode(ctx, "", nodeID1, 10)
		assert.Error(t, err, "assignmentID 为空应该报错")

		_, err = store.ListByRelatedNode(ctx, assignmentID, "", 10)
		assert.Error(t, err, "nodeID 为空应该报错")
	})
}
