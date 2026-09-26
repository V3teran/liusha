package explorationgraph

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdapterStore_CognitiveLoop(t *testing.T) {
	// 使用 Framework 的内存 GraphStore
	store := NewMemoryStore()

	ctx := context.Background()

	// 完整认知循环测试：objective → action → observation → evaluation → result

	t.Run("Step 1: Create Objective", func(t *testing.T) {
		obj := Objective{
			ID:          uuid.New().String(),
			TaskID:      "task-001",
			Description: "Scan target for vulnerabilities",
			CreatedAt:   time.Now(),
		}

		err := store.CreateObjective(ctx, obj)
		require.NoError(t, err)

		// 验证节点已创建
		node, err := store.GraphStore().GetNode(ctx, obj.ID)
		require.NoError(t, err)
		assert.Equal(t, string(core.KindObjective), node.Kind)
	})

	t.Run("Step 2: Create Actions", func(t *testing.T) {
		action1 := Action{
			ID:          uuid.New().String(),
			ObjectiveID: "objective-1",
			Type:        "port_scan",
			Target:      "192.168.1.1",
			State:       core.ActionStateOpen,
			Complexity:  core.ComplexitySimple,
			DependsOn:   []string{},
			CreatedAt:   time.Now(),
		}

		err := store.CreateAction(ctx, action1)
		require.NoError(t, err)

		// 验证节点和边
		node, err := store.GraphStore().GetNode(ctx, action1.ID)
		require.NoError(t, err)
		assert.Equal(t, string(core.KindAction), node.Kind)
		assert.Equal(t, string(core.ActionStateOpen), node.State)
	})

	t.Run("Step 3: Record Observations", func(t *testing.T) {
		obs := Observation{
			ID:         uuid.New().String(),
			ActionID:   "action-1",
			Type:       "open_ports",
			Data:       map[string]interface{}{"ports": []int{80, 443}},
			Confidence: core.ConfidenceVerified,
			CreatedAt:  time.Now(),
		}

		err := store.RecordObservation(ctx, obs)
		require.NoError(t, err)

		// 验证节点
		node, err := store.GraphStore().GetNode(ctx, obs.ID)
		require.NoError(t, err)
		assert.Equal(t, string(core.KindObservation), node.Kind)
		assert.Equal(t, confidenceToFloat(core.ConfidenceVerified), node.Confidence)
	})

	t.Run("Step 4: Add Evaluation", func(t *testing.T) {
		eval := Evaluation{
			ID:            uuid.New().String(),
			ObservationID: "observation-1",
			Outcome:       core.OutcomeConfirmed,
			Reasoning:     "Valid observation, high confidence",
			Confidence:    0.95,
			CreatedAt:     time.Now(),
		}

		err := store.AddEvaluation(ctx, eval)
		require.NoError(t, err)

		// 验证节点
		node, err := store.GraphStore().GetNode(ctx, eval.ID)
		require.NoError(t, err)
		assert.Equal(t, string(core.KindEvaluation), node.Kind)
	})

	t.Run("Step 5: Confirm Result", func(t *testing.T) {
		result := Result{
			ID:           uuid.New().String(),
			EvaluationID: "evaluation-1",
			Type:         "finding",
			Data:         map[string]interface{}{"severity": "high"},
			Confidence:   core.ConfidenceVerified,
			CreatedAt:    time.Now(),
		}

		err := store.ConfirmResult(ctx, result)
		require.NoError(t, err)

		// 验证节点
		node, err := store.GraphStore().GetNode(ctx, result.ID)
		require.NoError(t, err)
		assert.Equal(t, string(core.KindResult), node.Kind)
		assert.Equal(t, confidenceToFloat(core.ConfidenceVerified), node.Confidence)
	})
}

func TestAdapterStore_ActionManagement(t *testing.T) {
	store := NewMemoryStore()
	// store created above
	ctx := context.Background()

	// 创建多个不同状态的动作
	actions := []Action{
		{
			ID:         "action-open-1",
			Type:       "scan",
			State:      core.ActionStateOpen,
			Complexity: core.ComplexitySimple,
		},
		{
			ID:         "action-open-2",
			Type:       "exploit",
			State:      core.ActionStateOpen,
			Complexity: core.ComplexityComplex,
		},
		{
			ID:         "action-running",
			Type:       "recon",
			State:      core.ActionStateRunning,
			Complexity: core.ComplexityModerate,
		},
		{
			ID:         "action-done",
			Type:       "report",
			State:      core.ActionStateDone,
			Complexity: core.ComplexitySimple,
		},
	}

	for _, action := range actions {
		err := store.CreateAction(ctx, action)
		require.NoError(t, err)
	}

	t.Run("Get Open Actions", func(t *testing.T) {
		openActions, err := store.GetOpenActions(ctx, "")
		require.NoError(t, err)
		assert.Len(t, openActions, 2)
	})

	t.Run("Get Actions By State", func(t *testing.T) {
		runningActions, err := store.GetActionsByState(ctx, core.ActionStateRunning)
		require.NoError(t, err)
		assert.Len(t, runningActions, 1)
		assert.Equal(t, "action-running", runningActions[0].ID)
	})

	t.Run("Update Action State", func(t *testing.T) {
		err := store.UpdateActionState(ctx, "action-open-1", core.ActionStateRunning)
		require.NoError(t, err)

		// 验证状态已更新
		node, err := store.GraphStore().GetNode(ctx, "action-open-1")
		require.NoError(t, err)
		assert.Equal(t, string(core.ActionStateRunning), node.State)
	})
}

func TestAdapterStore_ObservationQueries(t *testing.T) {
	store := NewMemoryStore()
	// store created above
	ctx := context.Background()

	actionID := "action-test"

	// 创建动作
	action := Action{
		ID:         actionID,
		Type:       "scan",
		State:      core.ActionStateRunning,
		Complexity: core.ComplexitySimple,
	}
	require.NoError(t, store.CreateAction(ctx, action))

	// 创建多个观察
	observations := []Observation{
		{
			ID:         "obs-1",
			ActionID:   actionID,
			Type:       "port",
			Confidence: core.ConfidenceVerified,
		},
		{
			ID:         "obs-2",
			ActionID:   actionID,
			Type:       "service",
			Confidence: core.ConfidenceUnverified,
		},
		{
			ID:         "obs-3",
			ActionID:   "other-action",
			Type:       "vuln",
			Confidence: core.ConfidenceVerified,
		},
	}

	for _, obs := range observations {
		err := store.RecordObservation(ctx, obs)
		require.NoError(t, err)
	}

	t.Run("Get Observations for Action", func(t *testing.T) {
		results, err := store.GetObservations(ctx, actionID)
		require.NoError(t, err)
		assert.Len(t, results, 2)
	})
}

func TestAdapterStore_EvaluationOutcomes(t *testing.T) {
	store := NewMemoryStore()
	// store created above
	ctx := context.Background()

	// 创建观察
	obs := Observation{
		ID:         "obs-test",
		ActionID:   "action-test",
		Type:       "finding",
		Confidence: core.ConfidenceVerified,
	}
	require.NoError(t, store.RecordObservation(ctx, obs))

	t.Run("Positive Evaluation Creates Confirms Relation", func(t *testing.T) {
		eval := Evaluation{
			ID:            "eval-positive",
			ObservationID: obs.ID,
			Outcome:       core.OutcomeConfirmed,
			Reasoning:     "Valid finding",
			Confidence:    0.9,
		}

		err := store.AddEvaluation(ctx, eval)
		require.NoError(t, err)

		// 验证关系
		edges, err := store.GraphStore().ListEdges(ctx, core.GraphEdgeQuery{
			From: eval.ID,
		})
		require.NoError(t, err)
		assert.Len(t, edges, 1)
		assert.Equal(t, string(core.RelationConfirms), edges[0].Relation)
	})

	t.Run("Negative Evaluation Creates Refutes Relation", func(t *testing.T) {
		eval := Evaluation{
			ID:            "eval-negative",
			ObservationID: obs.ID,
			Outcome:       core.OutcomeRefuted,
			Reasoning:     "False positive",
			Confidence:    0.8,
		}

		err := store.AddEvaluation(ctx, eval)
		require.NoError(t, err)

		// 验证关系
		edges, err := store.GraphStore().ListEdges(ctx, core.GraphEdgeQuery{
			From: eval.ID,
		})
		require.NoError(t, err)
		assert.Len(t, edges, 1)
		assert.Equal(t, string(core.RelationRefutes), edges[0].Relation)
	})

	t.Run("Uncertain Evaluation Creates No Relation", func(t *testing.T) {
		eval := Evaluation{
			ID:            "eval-uncertain",
			ObservationID: obs.ID,
			Outcome:       core.OutcomeUncertain,
			Reasoning:     "Need more data",
			Confidence:    0.5,
		}

		err := store.AddEvaluation(ctx, eval)
		require.NoError(t, err)

		// 验证没有创建关系
		edges, err := store.GraphStore().ListEdges(ctx, core.GraphEdgeQuery{
			From: eval.ID,
		})
		require.NoError(t, err)
		assert.Len(t, edges, 0)
	})
}

func TestAdapterStore_DependencyGraph(t *testing.T) {
	store := NewMemoryStore()
	// store created above
	ctx := context.Background()

	// 创建依赖链：action1 → action2 → action3
	action1 := Action{
		ID:         "action-1",
		Type:       "recon",
		State:      core.ActionStateOpen,
		Complexity: core.ComplexitySimple,
		DependsOn:  []string{},
	}

	action2 := Action{
		ID:         "action-2",
		Type:       "scan",
		State:      core.ActionStateOpen,
		Complexity: core.ComplexityModerate,
		DependsOn:  []string{"action-1"},
	}

	action3 := Action{
		ID:         "action-3",
		Type:       "exploit",
		State:      core.ActionStateOpen,
		Complexity: core.ComplexityComplex,
		DependsOn:  []string{"action-2"},
	}

	require.NoError(t, store.CreateAction(ctx, action1))
	require.NoError(t, store.CreateAction(ctx, action2))
	require.NoError(t, store.CreateAction(ctx, action3))

	t.Run("Verify Dependency Edges", func(t *testing.T) {
		// 验证 action2 → action1 依赖边
		edges, err := store.GraphStore().ListEdges(ctx, core.GraphEdgeQuery{
			From:     "action-2",
			Relation: string(core.RelationDependsOn),
		})
		require.NoError(t, err)
		assert.Len(t, edges, 1)
		assert.Equal(t, "action-1", edges[0].To)

		// 验证 action3 → action2 依赖边
		edges, err = store.GraphStore().ListEdges(ctx, core.GraphEdgeQuery{
			From:     "action-3",
			Relation: string(core.RelationDependsOn),
		})
		require.NoError(t, err)
		assert.Len(t, edges, 1)
		assert.Equal(t, "action-2", edges[0].To)
	})
}
