//go:build integration

package cronschedule

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/dbtest"
)

// TestStore_CreateThenGetByID 验证：建定时模板后可按 ID 读回，next_run_at 已按 cron_expr 算出。
func TestStore_CreateThenGetByID(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	c, err := s.Create(ctx, NewParams{
		CronExpr: "0 4 * * *",
		Items:    []assignment.Item{{Brief: "夜间复扫 http://target.com"}},
		Title:    "夜间复扫",
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
	if got.CronExpr != "0 4 * * *" || got.Title != "夜间复扫" {
		t.Fatalf("字段不匹配: %+v", got)
	}
}

// TestStore_Create_RejectsMissingBriefOrInvalidCron 验证：缺 brief / 非法 cron_expr 建不出模板。
func TestStore_Create_RejectsMissingBriefOrInvalidCron(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	if _, err := s.Create(ctx, NewParams{}); err == nil {
		t.Fatal("缺 cron_expr 应报错")
	}
	if _, err := s.Create(ctx, NewParams{CronExpr: "not-a-cron", Items: []assignment.Item{{Brief: "b"}}}); err == nil {
		t.Fatal("非法 cron_expr 应报错")
	}
}

// TestStore_SetEnabled 验证启用/停用开关：返回写后的行；不存在的 id 包装 pgx.ErrNoRows（HTTP 层收敛 404）。
func TestStore_SetEnabled(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	c, err := s.Create(ctx, NewParams{CronExpr: "* * * * *", Items: []assignment.Item{{Brief: "b"}}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.SetEnabled(ctx, c.ID, false)
	if err != nil {
		t.Fatalf("set disabled: %v", err)
	}
	if got.Enabled || got.ID != c.ID {
		t.Fatalf("应返回已停用的同一模板: %+v", got)
	}
	reread, err := s.GetByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if reread.Enabled {
		t.Fatal("应已停用")
	}

	if _, err := s.SetEnabled(ctx, "00000000-0000-0000-0000-000000000000", true); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("不存在的 id 应包装 pgx.ErrNoRows，得 %v", err)
	}
}

// TestStore_SetEnabled_ReenableDoesNotCatchUp 验证：停用期间错过的触发点不补跑——重新启用时
// next_run_at 从当前时刻按 cron 重算，而不是沿用停用前留下的过去时间（那会在下一轮巡检立即触发）。
func TestStore_SetEnabled_ReenableDoesNotCatchUp(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	c, err := s.Create(ctx, NewParams{CronExpr: "0 4 * * *", Items: []assignment.Item{{Brief: "b"}}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.SetEnabled(ctx, c.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE cron_schedule SET next_run_at=$1 WHERE id=$2", time.Now().Add(-48*time.Hour), c.ID); err != nil {
		t.Fatalf("回拨 next_run_at: %v", err)
	}
	before := time.Now()
	got, err := s.SetEnabled(ctx, c.ID, true)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !got.Enabled || got.NextRunAt == nil || !got.NextRunAt.After(before) {
		t.Fatalf("重新启用后 next_run_at 应在当前之后，得 %+v", got)
	}
	due, err := s.ListDue(ctx, time.Now())
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	for _, d := range due {
		if d.ID == c.ID {
			t.Fatal("重新启用不应立即到点")
		}
	}
}

// TestStore_List 验证：列出全部模板（含已停用），新建的在前。
func TestStore_List(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	older, err := s.Create(ctx, NewParams{CronExpr: "0 4 * * *", Items: []assignment.Item{{Brief: "a"}}, Title: "older"})
	if err != nil {
		t.Fatalf("create older: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE cron_schedule SET created_at = created_at - interval '1 hour' WHERE id=$1", older.ID); err != nil {
		t.Fatalf("回拨 created_at: %v", err)
	}
	if _, err := s.SetEnabled(ctx, older.ID, false); err != nil {
		t.Fatalf("disable older: %v", err)
	}
	newer, err := s.Create(ctx, NewParams{CronExpr: "0 5 * * *", Items: []assignment.Item{{Brief: "b"}}, Title: "newer"})
	if err != nil {
		t.Fatalf("create newer: %v", err)
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].ID != newer.ID || list[1].ID != older.ID {
		t.Fatalf("应按新建时间倒序列出全部模板（含停用），得 %+v", list)
	}
	if list[1].Enabled {
		t.Fatal("停用状态应如实返回")
	}
}

// TestStore_Delete 验证：删除模板后不可再读；已触发的批次保留（schedule_id 置空）；
// 不存在的 id 包装 pgx.ErrNoRows。
func TestStore_Delete(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	c, err := s.Create(ctx, NewParams{CronExpr: "0 4 * * *", Items: []assignment.Item{{Brief: "b"}}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	asg, err := assignment.NewStore(pool).Create(ctx, assignment.NewParams{
		Source: assignment.SourceDirect, Items: []assignment.Item{{Brief: "b"}}, ScheduleID: &c.ID,
	})
	if err != nil {
		t.Fatalf("create cloned assignment: %v", err)
	}

	if err := s.Delete(ctx, c.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetByID(ctx, c.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("删除后读应为 ErrNoRows，得 %v", err)
	}
	var scheduleID *string
	if err := pool.QueryRow(ctx, "SELECT schedule_id::text FROM assignment WHERE id=$1", asg.ID).Scan(&scheduleID); err != nil {
		t.Fatalf("已触发批次应保留: %v", err)
	}
	if scheduleID != nil {
		t.Fatalf("已触发批次的 schedule_id 应置空，得 %v", *scheduleID)
	}
	if err := s.Delete(ctx, c.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("重复删除应包装 pgx.ErrNoRows，得 %v", err)
	}
}

// TestStore_ListDue 验证：只有 enabled 且 next_run_at<=now 的模板会被选中，且按 next_run_at 升序。
func TestStore_ListDue(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ctx := context.Background()
	now := time.Now()

	// 到点且启用：应出现
	due, err := s.Create(ctx, NewParams{CronExpr: "* * * * *", Items: []assignment.Item{{Brief: "b"}}})
	if err != nil {
		t.Fatalf("create due: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE cron_schedule SET next_run_at=$1 WHERE id=$2", now.Add(-time.Minute), due.ID); err != nil {
		t.Fatalf("回拨 due next_run_at: %v", err)
	}

	// 未到点：不应出现
	notDue, err := s.Create(ctx, NewParams{CronExpr: "* * * * *", Items: []assignment.Item{{Brief: "b"}}})
	if err != nil {
		t.Fatalf("create not due: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE cron_schedule SET next_run_at=$1 WHERE id=$2", now.Add(time.Hour), notDue.ID); err != nil {
		t.Fatalf("推迟 not-due next_run_at: %v", err)
	}

	// 到点但已停用：不应出现
	disabled, err := s.Create(ctx, NewParams{CronExpr: "* * * * *", Items: []assignment.Item{{Brief: "b"}}})
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

	c, err := s.Create(ctx, NewParams{CronExpr: "0 4 * * *", Items: []assignment.Item{{Brief: "b"}}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	firedAt := time.Date(2026, 7, 11, 4, 0, 0, 0, time.UTC)
	// 乐观锁前置：Create 后的 next_run_at 即触发方看到的到点时间
	c0, err := s.GetByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("get before mark: %v", err)
	}
	if c0.NextRunAt == nil {
		t.Fatalf("next_run_at 应非空")
	}
	if _, err := s.MarkFired(ctx, c.ID, firedAt, *c0.NextRunAt); err != nil {
		t.Fatalf("mark fired: %v", err)
	}

	// 乐观锁语义：以旧 next_run_at 再抢一次应失败（RowsAffected=0）
	if again, err := s.MarkFired(ctx, c.ID, firedAt, *c0.NextRunAt); err != nil || again {
		t.Fatalf("重复以同一 expected 触发应不生效: again=%v err=%v", again, err)
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
