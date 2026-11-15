//go:build integration

package task

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// 收敛的任务以收敛依据为前置落 completed：依据之后目标被改写、或图上又有了待执行的
// 动作（如用户指派），任务保持 active、图不收口（由恢复监控续跑新一轮）；依据仍成立才完成。
func TestCompleteConverged_RequiresBasisStillHolds(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))
	graph := explorationgraph.NewStore(s.pool)

	setup := func(t *testing.T) (string, explorationgraph.GoalBasis) {
		t.Helper()
		id := newActiveTask(t, s)
		basis, err := graph.GoalBasis(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return id, basis
	}
	assertActive := func(t *testing.T, id string) {
		t.Helper()
		tk, err := s.GetByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if tk.Status != StatusActive {
			t.Fatalf("任务应保持 active，实际 %s", tk.Status)
		}
		root, _, err := graph.RootObjectiveOf(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if *root.State != explorationgraph.StateOpen {
			t.Fatalf("图不应收口，根目标 %s", *root.State)
		}
	}

	t.Run("目标已改写", func(t *testing.T) {
		id, basis := setup(t)
		if err := s.SetGoal(ctx, id, "调整后的目标"); err != nil {
			t.Fatal(err)
		}
		if err := s.CompleteConverged(ctx, id, basis); !errors.Is(err, ErrStaleConvergence) {
			t.Fatalf("期望 ErrStaleConvergence，得 %v", err)
		}
		assertActive(t, id)
	})

	t.Run("又有了待执行动作", func(t *testing.T) {
		id, basis := setup(t)
		open := explorationgraph.StateOpen
		if _, err := graph.CreateNode(ctx, explorationgraph.Node{ID: uuid.NewString(), TaskID: id, Kind: core.KindAction,
			State: &open, SourceType: explorationgraph.SourceUser, Content: []byte(`{"instruction":"指派"}`),
			CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if err := s.CompleteConverged(ctx, id, basis); !errors.Is(err, ErrStaleConvergence) {
			t.Fatalf("期望 ErrStaleConvergence，得 %v", err)
		}
		assertActive(t, id)
	})

	t.Run("依据仍成立", func(t *testing.T) {
		id, basis := setup(t)
		if err := s.CompleteConverged(ctx, id, basis); err != nil {
			t.Fatal(err)
		}
		tk, err := s.GetByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if tk.Status != StatusCompleted {
			t.Fatalf("应完成，实际 %s", tk.Status)
		}
		if err := s.CompleteConverged(ctx, id, basis); err != nil {
			t.Fatalf("已完成时幂等，得 %v", err)
		}
	})

	t.Run("已被停止", func(t *testing.T) {
		id, basis := setup(t)
		if _, err := s.Abort(ctx, id, "用户停止"); err != nil {
			t.Fatal(err)
		}
		if err := s.CompleteConverged(ctx, id, basis); !errors.Is(err, ErrConflict) {
			t.Fatalf("迟到的收敛不得覆盖停止，得 %v", err)
		}
	})
}
