package explorationgraph

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkObservation 构造一个观察节点：status 非空即假设（带 repro），为空即事实。
func mkObservation(id, taskID string, status *VerificationStatus) Node {
	n := Node{
		ID:         id,
		TaskID:     taskID,
		Kind:       core.KindObservation,
		Content:    []byte(`{"statement":"SQLi at /api","repro":{"domain":"web"}}`),
		SourceType: SourceExecutor,
	}
	if status != nil {
		n.VerificationStatus = status
	}
	return n
}

func statusOf(s VerificationStatus) *VerificationStatus { return &s }

// 验证状态经存储往返不丢；只有假设带验证状态：事实与 result 都没有。
func TestStore_VerificationStatusRoundTrip(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_, err := store.CreateNode(ctx, mkObservation("obs-hyp", "task-1", statusOf(VerificationPending)))
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, Node{
		ID: "res-1", TaskID: "task-1", Kind: core.KindResult,
		Content: []byte(`{"statement":"SQLi"}`), SourceType: SourceEvaluator,
	})
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, mkObservation("obs-fact", "task-1", nil))
	require.NoError(t, err)

	hyp, err := store.GetNode(ctx, "obs-hyp")
	require.NoError(t, err)
	require.NotNil(t, hyp.VerificationStatus, "假设的验证状态不得在往返中丢失")
	assert.Equal(t, VerificationPending, *hyp.VerificationStatus)
	assert.True(t, hyp.IsHypothesis())
	assert.False(t, hyp.IsFact())

	res, err := store.GetNode(ctx, "res-1")
	require.NoError(t, err)
	assert.Nil(t, res.VerificationStatus, "result 就是坐实本身，不带验证状态")
	assert.False(t, res.IsHypothesis() || res.IsFact(), "result 不是观察")

	fact, err := store.GetNode(ctx, "obs-fact")
	require.NoError(t, err)
	assert.Nil(t, fact.VerificationStatus, "事实写入即终态，不带验证状态")
	assert.True(t, fact.IsFact())
}

// evaluator 以浅合并回写验证结论：验证状态读回到类型化字段，证伪原因读回到 Metadata。
func TestStore_VerificationMarkReadable(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_, err := store.CreateNode(ctx, mkObservation("obs-1", "task-1", statusOf(VerificationPending)))
	require.NoError(t, err)

	mark, err := json.Marshal(map[string]interface{}{
		"verification_status": VerificationRefuted,
		"refute_reason":       "invalid_recipe",
	})
	require.NoError(t, err)
	require.NoError(t, store.UpdateNodeMetadata(ctx, "obs-1", mark))

	node, err := store.GetNode(ctx, "obs-1")
	require.NoError(t, err)
	assert.True(t, node.HasVerificationStatus(VerificationRefuted))
	assert.Equal(t, "invalid_recipe", node.RefuteReason())
}

// 按验证状态圈定待验证假设：只返回本任务 pending 的假设。
func TestStore_ListHypotheses(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for _, n := range []Node{
		mkObservation("obs-pending-1", "task-1", statusOf(VerificationPending)),
		mkObservation("obs-pending-2", "task-1", statusOf(VerificationPending)),
		mkObservation("obs-done", "task-1", statusOf(VerificationConfirmed)),
		mkObservation("obs-fact", "task-1", nil),
		mkObservation("obs-other-task", "task-2", statusOf(VerificationPending)), // 其他任务不得串入
	} {
		_, err := store.CreateNode(ctx, n)
		require.NoError(t, err)
	}

	pending, err := store.ListHypotheses(ctx, "task-1", VerificationPending, 0)
	require.NoError(t, err)
	assert.Len(t, pending, 2, "只应圈定本任务 pending 的假设")

	limited, err := store.ListHypotheses(ctx, "task-1", VerificationPending, 1)
	require.NoError(t, err)
	assert.Len(t, limited, 1, "limit 应生效")
}

// TestStore_ListActionStates 验证状态投影：只取 id → state，不拉载荷。
func TestStore_ListActionStates(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_, err := store.CreateNode(ctx, mkActionNode("a-open", "task-1", StateOpen))
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, mkActionNode("a-done", "task-1", StateCompleted))
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, mkActionNode("a-failed", "task-1", StateFailed))
	require.NoError(t, err)

	states, err := store.ListActionStates(ctx, "task-1")
	require.NoError(t, err)
	assert.Equal(t, map[string]State{
		"a-open":   StateOpen,
		"a-done":   StateCompleted,
		"a-failed": StateFailed,
	}, states)
}

// TestStore_CreateNodeWithEdges 验证复合写：节点与溯源边一并落地；
// 边引用不存在的节点时整体失败。
func TestStore_CreateNodeWithEdges(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_, err := store.CreateNode(ctx, mkActionNode("a-1", "task-1", StateOpen))
	require.NoError(t, err)

	// 正常路径：observation 节点 + action → observation 边
	obs := mkObservation("obs-cwe", "task-1", nil)
	edge := Edge{TaskID: "task-1", SrcID: "a-1", Rel: RelGenerates, DstID: "obs-cwe"}
	require.NoError(t, store.CreateNodeWithEdges(ctx, obs, []Edge{edge}))

	edges, err := store.ListEdgesByTask(ctx, "task-1")
	require.NoError(t, err)
	require.Len(t, edges, 1)
	assert.Equal(t, "a-1", edges[0].SrcID)
	assert.Equal(t, "obs-cwe", edges[0].DstID)
	assert.Equal(t, RelGenerates, edges[0].Rel)

	// 失败路径：边引用幻影节点 → 整体失败，节点不落
	orphan := mkObservation("obs-orphan", "task-1", nil)
	badEdge := Edge{TaskID: "task-1", SrcID: "ghost-action", Rel: RelGenerates, DstID: "obs-orphan"}
	err = store.CreateNodeWithEdges(ctx, orphan, []Edge{badEdge})
	require.Error(t, err, "边引用不存在的节点应整体失败")

	_, err = store.GetNode(ctx, "obs-orphan")
	assert.Error(t, err, "复合写失败后不得留下孤立节点")
}

// TestStore_ListEdgesTaskScoped 验证边查询按 task 隔离。
func TestStore_ListEdgesTaskScoped(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for _, task := range []string{"task-1", "task-2"} {
		// 溯源边随复合写落地（生产唯一边写入口）；动作节点前置创建供边引用。
		if _, err := store.CreateNode(ctx, mkActionNode("a-"+task, task, StateOpen)); err != nil {
			require.NoError(t, err)
		}
		require.NoError(t, store.CreateNodeWithEdges(ctx, mkObservation("obs-"+task, task, nil), []Edge{{
			TaskID: task, SrcID: "a-" + task, Rel: RelGenerates, DstID: "obs-" + task,
		}}))
	}

	t1, err := store.ListEdgesByTask(ctx, "task-1")
	require.NoError(t, err)
	require.Len(t, t1, 1)
	assert.Equal(t, "a-task-1", t1[0].SrcID)

	t2, err := store.ListEdgesByTask(ctx, "task-2")
	require.NoError(t, err)
	require.Len(t, t2, 1)
	assert.Equal(t, "a-task-2", t2[0].SrcID)
}
