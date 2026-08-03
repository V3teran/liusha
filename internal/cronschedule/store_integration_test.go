//go:build integration

package cronschedule

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/dbtest"
)

const testScenario = "web-pentest-killchain"

// TestStore_CreateThenGetByID 验证：建定时模板后可按 ID 读回，next_run_at 已按 cron_expr 算出。
func TestStore_CreateThenGetByID(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	c, err := s.Create(ctx, NewParams{
		ScenarioID: testScenario,
		CronExpr:   "0 4 * * *",
		Items:      []assignment.Item{{Brief: "夜间复扫 http://target.com"}},
		Title:      "夜间复扫",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.ID == "" {
		t.Fatal("create 应返回非空 ID")
	}
	if c.NextRunAt == nil {
		t.Fatal("create 后 next_run_at 应已算出")
	}
	if !c.Enabled {
		t.Fatal("默认应 enabled=true")
	}

	got, err := s.GetByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.ScenarioID != testScenario || got.CronExpr != "0 4 * * *" || got.Title != "夜间复扫" {
		t.Fatalf("字段不匹配: %+v", got)
	}
}

// TestStore_Create_RejectsMissingScenarioOrInvalidCron 验证：缺 scenario_id / 非法 cron_expr 建不出模板。
func TestStore_Create_RejectsMissingScenarioOrInvalidCron(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	if _, err := s.Create(ctx, NewParams{ScenarioID: "", CronExpr: "* * * * *"}); err == nil {
		t.Fatal("缺 scenario_id 应报错")
	}
	if _, err := s.Create(ctx, NewParams{ScenarioID: testScenario, CronExpr: "not-a-cron"}); err == nil {
		t.Fatal("非法 cron_expr 应报错")
	}
}

// TestStore_SetEnabled 验证启用/停用开关。
func TestStore_SetEnabled(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	c, err := s.Create(ctx, NewParams{ScenarioID: testScenario, CronExpr: "* * * * *"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.SetEnabled(ctx, c.ID, false); err != nil {
		t.Fatalf("set disabled: %v", err)
	}
	got, err := s.GetByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Enabled {
		t.Fatal("应已停用")
	}

	if err := s.SetEnabled(ctx, "00000000-0000-0000-0000-000000000000", true); err == nil {
		t.Fatal("不存在的 id 应报错")
	}
}

// TestStore_ListDue 验证：只有 enabled 且 next_run_at<=now 的模板会被选中，且按 next_run_at 升序。
func TestStore_ListDue(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	// 到点且启用：应出现
	due, err := s.Create(ctx, NewParams{ScenarioID: testScenario, CronExpr: "* * * * *"})
	if err != nil {
		t.Fatalf("create due: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE cron_schedule SET next_run_at=$1 WHERE id=$2", now.Add(-time.Minute), due.ID); err != nil {
		t.Fatalf("回拨 due next_run_at: %v", err)
	}

	// 未到点：不应出现
	notDue, err := s.Create(ctx, NewParams{ScenarioID: testScenario, CronExpr: "* * * * *"})
	if err != nil {
		t.Fatalf("create not due: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE cron_schedule SET next_run_at=$1 WHERE id=$2", now.Add(time.Hour), notDue.ID); err != nil {
		t.Fatalf("推迟 not-due next_run_at: %v", err)
	}

	// 到点但已停用：不应出现
	disabled, err := s.Create(ctx, NewParams{ScenarioID: testScenario, CronExpr: "* * * * *"})
	if err != nil {
		t.Fatalf("create disabled: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE cron_schedule SET next_run_at=$1, enabled=false WHERE id=$2", now.Add(-time.Minute), disabled.ID); err != nil {
		t.Fatalf("停用: %v", err)
	}

	list, err := s.ListDue(ctx, now)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(list) != 1 || list[0].ID != due.ID {
		t.Fatalf("ListDue 应恰好返回 due 一条，got %+v", list)
	}
}

// TestStore_MarkFired 验证：触发后 last_run_at/next_run_at 都正确回写。
func TestStore_MarkFired(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	c, err := s.Create(ctx, NewParams{ScenarioID: testScenario, CronExpr: "0 4 * * *"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	firedAt := time.Date(2026, 7, 11, 4, 0, 0, 0, time.UTC)
	if err := s.MarkFired(ctx, c.ID, firedAt); err != nil {
		t.Fatalf("mark fired: %v", err)
	}

	got, err := s.GetByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.LastRunAt == nil || !got.LastRunAt.Equal(firedAt) {
		t.Fatalf("last_run_at 应等于 firedAt，got %v", got.LastRunAt)
	}
	wantNext := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	if got.NextRunAt == nil || !got.NextRunAt.Equal(wantNext) {
		t.Fatalf("next_run_at 应推进到 %v，got %v", wantNext, got.NextRunAt)
	}
}

// TestStore_List_FiltersByScenario 验证 List 按 scenario_id 过滤。
func TestStore_List_FiltersByScenario(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	if _, err := s.Create(ctx, NewParams{ScenarioID: testScenario, CronExpr: "* * * * *", Title: "a1"}); err != nil {
		t.Fatalf("create scenario a: %v", err)
	}
	if _, err := s.Create(ctx, NewParams{ScenarioID: "traffic-analysis", CronExpr: "* * * * *", Title: "p1"}); err != nil {
		t.Fatalf("create scenario b: %v", err)
	}

	filtered, err := s.List(ctx, testScenario, 0)
	if err != nil {
		t.Fatalf("list by scenario: %v", err)
	}
	for _, c := range filtered {
		if c.ScenarioID != testScenario {
			t.Fatalf("List(%q) 混入了 %s", testScenario, c.ScenarioID)
		}
	}

	all, err := s.List(ctx, "", 0)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) < 2 {
		t.Fatalf("List(\"\") 应至少含 2 条，got %d", len(all))
	}
}
