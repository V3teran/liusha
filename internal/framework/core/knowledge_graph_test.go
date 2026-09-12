package core

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNodeKind(t *testing.T) {
	t.Run("valid node kinds", func(t *testing.T) {
		kinds := ValidNodeKinds()
		assert.Len(t, kinds, 5)
		assert.Contains(t, kinds, KindObjective)
		assert.Contains(t, kinds, KindAction)
		assert.Contains(t, kinds, KindObservation)
		assert.Contains(t, kinds, KindEvaluation)
		assert.Contains(t, kinds, KindResult)
	})

	t.Run("string representation", func(t *testing.T) {
		assert.Equal(t, "objective", KindObjective.String())
		assert.Equal(t, "action", KindAction.String())
		assert.Equal(t, "observation", KindObservation.String())
		assert.Equal(t, "evaluation", KindEvaluation.String())
		assert.Equal(t, "result", KindResult.String())
	})
}

func TestRelationKind(t *testing.T) {
	t.Run("valid relation kinds", func(t *testing.T) {
		relations := ValidRelationKinds()
		assert.Len(t, relations, 7)
		assert.Contains(t, relations, RelationGenerates)
		assert.Contains(t, relations, RelationConfirms)
		assert.Contains(t, relations, RelationRefutes)
		assert.Contains(t, relations, RelationEnables)
		assert.Contains(t, relations, RelationDependsOn)
		assert.Contains(t, relations, RelationContributes)
		assert.Contains(t, relations, RelationInvalidates)
	})

	t.Run("string representation", func(t *testing.T) {
		assert.Equal(t, "generates", RelationGenerates.String())
		assert.Equal(t, "confirms", RelationConfirms.String())
		assert.Equal(t, "refutes", RelationRefutes.String())
		assert.Equal(t, "enables", RelationEnables.String())
		assert.Equal(t, "depends_on", RelationDependsOn.String())
		assert.Equal(t, "contributes", RelationContributes.String())
		assert.Equal(t, "invalidates", RelationInvalidates.String())
	})
}

func TestActionState(t *testing.T) {
	t.Run("valid action states", func(t *testing.T) {
		states := ValidActionStates()
		assert.Len(t, states, 7)
	})

	t.Run("terminal states", func(t *testing.T) {
		assert.True(t, ActionStateDone.IsTerminal())
		assert.True(t, ActionStateFailed.IsTerminal())
		assert.True(t, ActionStateExhausted.IsTerminal())
		assert.True(t, ActionStateAborted.IsTerminal())

		assert.False(t, ActionStateOpen.IsTerminal())
		assert.False(t, ActionStateBlocked.IsTerminal())
		assert.False(t, ActionStateRunning.IsTerminal())
	})

	t.Run("executable states", func(t *testing.T) {
		assert.True(t, ActionStateOpen.IsExecutable())

		assert.False(t, ActionStateBlocked.IsExecutable())
		assert.False(t, ActionStateRunning.IsExecutable())
		assert.False(t, ActionStateDone.IsExecutable())
	})

	t.Run("active states", func(t *testing.T) {
		assert.True(t, ActionStateRunning.IsActive())

		assert.False(t, ActionStateOpen.IsActive())
		assert.False(t, ActionStateDone.IsActive())
	})
}

func TestObservationConfidence(t *testing.T) {
	t.Run("verified confidence", func(t *testing.T) {
		assert.True(t, ConfidenceVerified.IsVerified())
		assert.True(t, ConfidenceVerified.IsHighConfidence())

		assert.False(t, ConfidenceHigh.IsVerified())
		assert.True(t, ConfidenceHigh.IsHighConfidence())

		assert.False(t, ConfidenceMedium.IsHighConfidence())
	})

	t.Run("confidence levels", func(t *testing.T) {
		assert.Equal(t, 0.0, float64(ConfidenceUnknown))
		assert.Equal(t, 0.3, float64(ConfidenceLow))
		assert.Equal(t, 0.6, float64(ConfidenceMedium))
		assert.Equal(t, 0.9, float64(ConfidenceHigh))
		assert.Equal(t, 1.0, float64(ConfidenceVerified))
	})
}

func TestEvaluationOutcome(t *testing.T) {
	t.Run("valid outcomes", func(t *testing.T) {
		outcomes := ValidEvaluationOutcomes()
		assert.Len(t, outcomes, 4)
	})

	t.Run("positive outcomes", func(t *testing.T) {
		assert.True(t, OutcomeConfirmed.IsPositive())
		assert.True(t, OutcomePartial.IsPositive())

		assert.False(t, OutcomeRefuted.IsPositive())
		assert.False(t, OutcomeUncertain.IsPositive())
	})

	t.Run("negative outcomes", func(t *testing.T) {
		assert.True(t, OutcomeRefuted.IsNegative())

		assert.False(t, OutcomeConfirmed.IsNegative())
		assert.False(t, OutcomeUncertain.IsNegative())
		assert.False(t, OutcomePartial.IsNegative())
	})
}

func TestActionComplexity(t *testing.T) {
	t.Run("complexity levels", func(t *testing.T) {
		assert.Equal(t, 1, int(ComplexityTrivial))
		assert.Equal(t, 2, int(ComplexitySimple))
		assert.Equal(t, 3, int(ComplexityModerate))
		assert.Equal(t, 4, int(ComplexityComplex))
		assert.Equal(t, 5, int(ComplexityVeryComplex))
	})
}

// TestKnowledgeGraphSemantics 验证知识图谱语义的正确性
func TestKnowledgeGraphSemantics(t *testing.T) {
	t.Run("ReAct cycle alignment", func(t *testing.T) {
		// 验证对齐 ReAct 模式：Thought → Action → Observation

		// Objective 相当于 Thought（目标/思考）
		objective := KindObjective
		assert.Equal(t, "objective", objective.String())

		// Action 相当于 Action（行动）
		action := KindAction
		assert.Equal(t, "action", action.String())

		// Observation 相当于 Observation（观察）
		observation := KindObservation
		assert.Equal(t, "observation", observation.String())

		// Action → Observation 的关系
		generates := RelationGenerates
		assert.Equal(t, "generates", generates.String())
	})

	t.Run("PDDL alignment", func(t *testing.T) {
		// 验证对齐 PDDL 标准：Goal → Action → State

		// Objective = Goal
		assert.Equal(t, "objective", KindObjective.String())

		// Action = Action
		assert.Equal(t, "action", KindAction.String())

		// Result = State
		assert.Equal(t, "result", KindResult.String())

		// Action → Result 的路径：action → observation → evaluation → result
		assert.Equal(t, "generates", RelationGenerates.String())
		assert.Equal(t, "confirms", RelationConfirms.String())
	})

	t.Run("cognitive cycle", func(t *testing.T) {
		// 完整认知循环：objective → action → observation → evaluation → result

		cycle := []NodeKind{
			KindObjective,   // 设定目标
			KindAction,      // 规划动作
			KindObservation, // 执行观察
			KindEvaluation,  // 评估结果
			KindResult,      // 确认结果
		}

		assert.Len(t, cycle, 5)

		// 验证关系链
		relations := []RelationKind{
			RelationDependsOn,   // objective → action
			RelationGenerates,   // action → observation
			RelationConfirms,    // evaluation → result
			RelationEnables,     // result → action (新循环)
		}

		assert.Len(t, relations, 4)
	})
}
