package explorationgraph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/framework/core"
)

// 图面按类计数，目标版本取自调用方给出的依据。
func TestSurface_CountsByKindWithBasisVersion(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	open, done := StateOpen, StateCompleted
	refuted, pending := VerificationRefuted, VerificationPending
	for _, n := range []Node{
		{ID: "root", TaskID: "t", Kind: core.KindObjective, State: &open, SourceType: SourceUser},
		{ID: "a1", TaskID: "t", Kind: core.KindAction, State: &open},
		{ID: "a2", TaskID: "t", Kind: core.KindAction, State: &done},
		{ID: "o1", TaskID: "t", Kind: core.KindObservation},
		{ID: "h1", TaskID: "t", Kind: core.KindObservation, VerificationStatus: &refuted},
		{ID: "h2", TaskID: "t", Kind: core.KindObservation, VerificationStatus: &pending},
		{ID: "r1", TaskID: "t", Kind: core.KindResult},
		{ID: "other", TaskID: "t2", Kind: core.KindResult},
	} {
		_, err := store.CreateNode(ctx, n)
		require.NoError(t, err)
	}

	got, err := store.Surface(ctx, "t", GoalBasis{ObjectiveID: "root", Version: 7})
	require.NoError(t, err)
	assert.Equal(t, Surface{Goal: 7, Objectives: 1, Actions: 2, Settled: 1, Results: 1, Observations: 3, Refuted: 1}, got)
}
