//go:build integration

package task

import (
	"context"
	"errors"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/explorationgraph"
)

// rootGoal 读取任务根目标的描述与数量。
func rootGoal(t *testing.T, graph *explorationgraph.Store, id string) (string, int) {
	t.Helper()
	objectives, err := graph.ListNodesByTask(context.Background(), id, "objective")
	if err != nil {
		t.Fatal(err)
	}
	roots := 0
	goal := ""
	for _, o := range objectives {
		if !o.IsDerivedObjective() {
			roots++
			goal = o.Goal()
		}
	}
	return goal, roots
}

// 任务创建即带根目标：目标在任务行与根目标描述各一份，同一事务写入。
func TestCreate_CreatesRootObjective(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))
	graph := explorationgraph.NewStore(s.pool)

	tk, err := s.Create(ctx, NewParams{AssignmentID: dbtest.SeedAssignment(t, s.pool), Goal: "测试 http://a.test 的登录页"})
	if err != nil {
		t.Fatal(err)
	}
	if tk.Goal != "测试 http://a.test 的登录页" {
		t.Fatalf("任务行目标 %q", tk.Goal)
	}
	goal, roots := rootGoal(t, graph, tk.ID)
	if roots != 1 || goal != tk.Goal {
		t.Fatalf("应恰有一个根目标且描述与任务目标一致，得 %d 个、%q", roots, goal)
	}
	root, _, err := graph.RootObjectiveOf(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if root.SourceType != explorationgraph.SourceUser || *root.State != explorationgraph.StateOpen {
		t.Fatalf("根目标应为 user 来源、open，得 %s %s", root.SourceType, *root.State)
	}

	if _, err := s.Create(ctx, NewParams{AssignmentID: tk.AssignmentID, Goal: ""}); err == nil {
		t.Fatal("目标必填")
	}
}

// SetGoal 是目标的唯一写入口：任务行与根目标描述同事务改写；内容不变不写；已结束的任务拒绝。
func TestSetGoal_WritesTaskAndRootTogether(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))
	graph := explorationgraph.NewStore(s.pool)
	id := newActiveTask(t, s)
	before, err := graph.GoalBasis(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetGoal(ctx, id, "guard test"); err != nil {
		t.Fatal(err)
	}
	if same, _ := graph.GoalBasis(ctx, id); same != before {
		t.Fatal("内容不变不应改写（依据不变）")
	}

	if err := s.SetGoal(ctx, id, "调整后的目标"); err != nil {
		t.Fatal(err)
	}
	tk, err := s.GetByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	goal, _ := rootGoal(t, graph, id)
	if tk.Goal != "调整后的目标" || goal != "调整后的目标" {
		t.Fatalf("任务行 %q / 根目标 %q 应同时改写", tk.Goal, goal)
	}
	if after, _ := graph.GoalBasis(ctx, id); after == before {
		t.Fatal("改写后依据应变化")
	}

	if err := s.Abort(ctx, id, "停止"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGoal(ctx, id, "迟到的调整"); !errors.Is(err, ErrNotActive) {
		t.Fatalf("已结束的任务应拒绝，得 %v", err)
	}
	if goal, _ := rootGoal(t, graph, id); goal != "调整后的目标" {
		t.Fatalf("被拒时根目标不动，得 %q", goal)
	}
}
