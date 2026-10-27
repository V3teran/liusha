package explorationgraph

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkObservation 构造一个假设 observation 节点（带 repro、unverified）。
func mkObservation(id, taskID string, confidence *Confidence) Node {
	n := Node{
		ID:         id,
		TaskID:     taskID,
		Kind:       core.KindObservation,
		Content:    []byte(`{"statement":"SQLi at /api","repro":{"domain":"web"}}`),
		SourceType: SourceExecutor,
	}
	if confidence != nil {
		n.Confidence = confidence
	}
	return n
}

// TestStore_ConfidenceRoundTrip 验证 confidence 枚举经存储往返不丢：
// 写入侧落 metadata.confidence 枚举原文，读取侧从 metadata 恢复。
func TestStore_ConfidenceRoundTrip(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	unverified := ConfidenceUnverified
	verified := ConfidenceVerified

	// 假设以 unverified 入图
	_, err := store.CreateNode(ctx, mkObservation("obs-uv", "task-1", &unverified))
	require.NoError(t, err)
	// 复现门坐实后的 result 以 verified 入图
	_, err = store.CreateNode(ctx, Node{
		ID:         "res-1",
		TaskID:     "task-1",
		Kind:       core.KindResult,
		Content:    []byte(`{"summary":"SQLi"}`),
		Confidence: &verified,
		SourceType: SourceEvaluator,
	})
	require.NoError(t, err)
	// 执行记录（无 repro）不携带 confidence——不参与裁决生命周期
	_, err = store.CreateNode(ctx, Node{
		ID:         "obs-record",
		TaskID:     "task-1",
		Kind:       core.KindObservation,
		Content:    []byte(`{"statement":"scan finished"}`),
		SourceType: SourceExecutor,
	})
	require.NoError(t, err)

	obs, err := store.GetNode(ctx, "obs-uv")
	require.NoError(t, err)
	require.NotNil(t, obs.Confidence, "假设的 confidence 不得在往返中丢失")
	assert.Equal(t, ConfidenceUnverified, *obs.Confidence)

	res, err := store.GetNode(ctx, "res-1")
	require.NoError(t, err)
	require.NotNil(t, res.Confidence)
	assert.Equal(t, ConfidenceVerified, *res.Confidence)

	record, err := store.GetNode(ctx, "obs-record")
	require.NoError(t, err)
	assert.Nil(t, record.Confidence, "非假设观察不应携带 confidence")
}

// TestStore_MetadataLeftoversReadable 验证 graph metadata 的非类型化键
// （verification_outcome 等）读回时进入 Node.Metadata——planner 的
// 证伪反馈环依赖此路径。
func TestStore_MetadataLeftoversReadable(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	unverified := ConfidenceUnverified
	_, err := store.CreateNode(ctx, mkObservation("obs-1", "task-1", &unverified))
	require.NoError(t, err)

	mark, err := json.Marshal(map[string]interface{}{
		"verification_outcome": "refuted",
		"confidence":           ConfidenceRefuted,
	})
	require.NoError(t, err)
	require.NoError(t, store.UpdateNodeMetadata(ctx, "obs-1", mark))

	node, err := store.GetNode(ctx, "obs-1")
	require.NoError(t, err)
	require.NotNil(t, node.Confidence)
	assert.Equal(t, ConfidenceRefuted, *node.Confidence)

	var m struct {
		VerificationOutcome string `json:"verification_outcome"`
	}
	require.NoError(t, json.Unmarshal(node.Metadata, &m))
	assert.Equal(t, "refuted", m.VerificationOutcome, "metadata 扩展键必须可读回")
}

// TestStore_ListObservationsByConfidence 验证按裁决态圈定待裁决假设：
// 只返回 confidence=unverified 的 observation。
func TestStore_ListObservationsByConfidence(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	unverified := ConfidenceUnverified
	verified := ConfidenceVerified

	_, err := store.CreateNode(ctx, mkObservation("obs-pending-1", "task-1", &unverified))
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, mkObservation("obs-pending-2", "task-1", &unverified))
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, mkObservation("obs-done", "task-1", &verified))
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, mkObservation("obs-record", "task-1", nil))
	require.NoError(t, err)
	// 其他 task 的假设不得串入
	_, err = store.CreateNode(ctx, mkObservation("obs-other-task", "task-2", &unverified))
	require.NoError(t, err)

	pending, err := store.ListObservationsByConfidence(ctx, "task-1", ConfidenceUnverified, 0)
	require.NoError(t, err)
	assert.Len(t, pending, 2, "只应圈定本 task 的 unverified 假设")

	pendingLimited, err := store.ListObservationsByConfidence(ctx, "task-1", ConfidenceUnverified, 1)
	require.NoError(t, err)
	assert.Len(t, pendingLimited, 1, "limit 应生效")
}

// TestStore_ListActionStates 验证状态投影：只取 id → state，不拉载荷。
func TestStore_ListActionStates(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_, err := store.CreateNode(ctx, mkActionNode("a-open", "task-1", StateOpen, nil))
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, mkActionNode("a-done", "task-1", StateDone, nil))
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, mkActionNode("a-failed", "task-1", StateFailed, nil))
	require.NoError(t, err)

	states, err := store.ListActionStates(ctx, "task-1")
	require.NoError(t, err)
	assert.Equal(t, map[string]State{
		"a-open":   StateOpen,
		"a-done":   StateDone,
		"a-failed": StateFailed,
	}, states)
}

// TestStore_CreateNodeWithEdges 验证复合写：节点与溯源边一并落地；
// 边引用不存在的节点时整体失败。
func TestStore_CreateNodeWithEdges(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_, err := store.CreateNode(ctx, mkActionNode("a-1", "task-1", StateOpen, nil))
	require.NoError(t, err)

	// 正常路径：observation 节点 + action → observation 边
	obs := mkObservation("obs-cwe", "task-1", nil)
	edge := Edge{TaskID: "task-1", SrcID: "a-1", Rel: RelGenerates, DstID: "obs-cwe"}
	require.NoError(t, store.CreateNodeWithEdges(ctx, obs, []Edge{edge}))

	edges, err := store.ListEdgesForAPI(ctx, "task-1")
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
		if _, err := store.CreateNode(ctx, mkActionNode("a-"+task, task, StateOpen, nil)); err != nil {
			require.NoError(t, err)
		}
		require.NoError(t, store.CreateNodeWithEdges(ctx, mkObservation("obs-"+task, task, nil), []Edge{{
			TaskID: task, SrcID: "a-" + task, Rel: RelGenerates, DstID: "obs-" + task,
		}}))
	}

	t1, err := store.ListEdgesForAPI(ctx, "task-1")
	require.NoError(t, err)
	require.Len(t, t1, 1)
	assert.Equal(t, "a-task-1", t1[0].SrcID)

	t2, err := store.ListEdgesForAPI(ctx, "task-2")
	require.NoError(t, err)
	require.Len(t, t2, 1)
	assert.Equal(t, "a-task-2", t2[0].SrcID)
}
