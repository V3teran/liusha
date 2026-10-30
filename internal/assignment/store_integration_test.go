//go:build integration

package assignment

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/task"
)

// assignment 是批次容器域：「读时派生状态」（不落存储）是本 store 的核心设计，
// 直测保证派生口径与子 task 真实态一致。

func newStores(t *testing.T) (*Store, *task.Store) {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	return NewStore(pool), task.NewStore(pool)
}

func mkAssignment(t *testing.T, s *Store, ctx context.Context, briefs ...string) Assignment {
	t.Helper()
	items := make([]Item, 0, len(briefs))
	for _, b := range briefs {
		items = append(items, Item{Brief: b})
	}
	a, err := s.Create(ctx, NewParams{Source: SourceManual, Items: items, Title: "批次", Description: "d"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return a
}

func TestCreateAndGetByID(t *testing.T) {
	asg, _ := newStores(t)
	ctx := context.Background()

	a := mkAssignment(t, asg, ctx, "brief-1", "brief-2")
	got, err := asg.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != a.ID || got.Source != SourceManual || got.Title != "批次" || got.Description != "d" {
		t.Fatalf("字段回读不符: %+v", got)
	}
	if got.ScheduleID != nil {
		t.Errorf("立即下发批次不应有 schedule_id: %+v", got.ScheduleID)
	}
}

func TestDeriveStatus(t *testing.T) {
	asgs, tasks := newStores(t)
	ctx := context.Background()

	a := mkAssignment(t, asgs, ctx, "对 http://e.example.com 做存活探测", "对 http://f.example.com 做存活探测")

	// 空：无子 task
	if st, err := asgs.DeriveStatus(ctx, a.ID); err != nil || st != StatusEmpty {
		t.Fatalf("无子 task 应 empty: st=%s err=%v", st, err)
	}

	// 建子 task → running
	t1, _ := tasks.Create(ctx, task.NewParams{AssignmentID: a.ID, Brief: "对 http://e.example.com 做存活探测"})
	t2, _ := tasks.Create(ctx, task.NewParams{AssignmentID: a.ID, Brief: "对 http://f.example.com 做存活探测"})
	if st, _ := asgs.DeriveStatus(ctx, a.ID); st != StatusRunning {
		t.Fatalf("有 active 子 task 应 running, got %s", st)
	}

	// 全 completed → done
	if err := tasks.Complete(ctx, t1.ID); err != nil {
		t.Fatal(err)
	}
	if st, _ := asgs.DeriveStatus(ctx, a.ID); st != StatusRunning {
		t.Fatalf("仍有 active 应保持 running, got %s", st)
	}
	if err := tasks.Complete(ctx, t2.ID); err != nil {
		t.Fatal(err)
	}
	if st, _ := asgs.DeriveStatus(ctx, a.ID); st != StatusDone {
		t.Fatalf("全 completed 应 done, got %s", st)
	}
}

func TestSourceValidation(t *testing.T) {
	asg, _ := newStores(t)
	ctx := context.Background()

	if _, err := asg.Create(ctx, NewParams{Source: "bogus", Items: []Item{{Brief: "x"}}}); err == nil {
		t.Fatal("非法 source 应报错")
	}
}
