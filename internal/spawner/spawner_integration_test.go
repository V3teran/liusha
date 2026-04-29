//go:build integration

package spawner

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
)

// TestSpawn_DepthLimit：父任务自身已是子任务（depth=1）→ Spawn 必须拒绝。
// 用真 pgxpool 来复现 model.Task.ParentTaskID 字段读写的实际行为。
func TestSpawn_DepthLimit(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	es := engagement.NewStore(pool)
	ts := task.NewStore(pool)
	ctx := context.Background()

	e, err := es.LookupOrCreate(ctx, "default", "h", engagement.ModeProxy)
	if err != nil {
		t.Fatalf("engagement.LookupOrCreate err=%v", err)
	}
	parentID, err := ts.Create(ctx, task.NewParams{EngagementID: e.ID, Role: "sniffer"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	pid := parentID
	childID, err := ts.Create(ctx, task.NewParams{EngagementID: e.ID, ParentTaskID: &pid, Role: "sniffer"})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	mr := miniredis.RunT(t)
	wc := worker.NewClient(asynq.RedisClientOpt{Addr: mr.Addr()})
	defer wc.Close()

	s := New(ts, wc, Limits{MaxChildrenPerParent: 5, MaxInflightPerEngagement: 10})
	if _, err := s.Spawn(ctx, childID, "vuln/web/bac", json.RawMessage(`{}`), nil); err == nil {
		t.Fatal("从子任务再 spawn 应被 depth 校验拒绝")
	}
}

// TestSpawner_E2E：用真 task.Store + 真 worker.Client（miniredis），
// spawn 一个子任务，验证：
//  1. agent_task 表新插入一行 child（parent_task_id 指向 parent，role 继承）
//  2. asynq queue 中确实有 1 条任务
func TestSpawner_E2E(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	es := engagement.NewStore(pool)
	ts := task.NewStore(pool)
	ctx := context.Background()

	e, err := es.LookupOrCreate(ctx, "default", "vulnapp", engagement.ModeProxy)
	if err != nil {
		t.Fatalf("engagement.LookupOrCreate err=%v", err)
	}
	parentID, err := ts.Create(ctx, task.NewParams{EngagementID: e.ID, Role: "sniffer"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	mr := miniredis.RunT(t)
	redisOpt := asynq.RedisClientOpt{Addr: mr.Addr()}
	wc := worker.NewClient(redisOpt)
	defer wc.Close()

	s := New(ts, wc, Limits{MaxChildrenPerParent: 10, MaxInflightPerEngagement: 20})

	childID, err := s.Spawn(
		ctx,
		parentID,
		"vuln/web/bac",
		json.RawMessage(`{"flow_id":1,"host":"vulnapp"}`),
		json.RawMessage(`{"max_steps":20}`),
	)
	if err != nil {
		t.Fatalf("Spawn err=%v", err)
	}
	if childID == "" {
		t.Fatal("childID 不能为空")
	}

	// 1. 验证 agent_task 表已新增一行 child。
	got, err := ts.GetByID(ctx, childID)
	if err != nil {
		t.Fatalf("查不到刚创建的子任务: %v", err)
	}
	if got.ParentTaskID == nil || *got.ParentTaskID != parentID {
		t.Fatalf("ParentTaskID = %v, want %q", got.ParentTaskID, parentID)
	}
	if got.Role != "sniffer" {
		t.Fatalf("Role = %q, want sniffer（应继承父）", got.Role)
	}
	if got.Skill != "vuln/web/bac" {
		t.Fatalf("Skill = %q, want vuln/web/bac", got.Skill)
	}
	if got.EngagementID != e.ID {
		t.Fatalf("EngagementID = %q, want %q", got.EngagementID, e.ID)
	}

	// 2. 验证 asynq queue 中有 1 条任务。
	insp := asynq.NewInspector(redisOpt)
	defer insp.Close()
	qinfo, err := insp.GetQueueInfo(worker.QueueSniffer)
	if err != nil {
		t.Fatalf("inspector GetQueueInfo err=%v", err)
	}
	// 入队后任务应处于 pending（尚未被消费者拉走）。
	if qinfo.Pending != 1 {
		t.Fatalf("queue %s pending = %d, want 1", worker.QueueSniffer, qinfo.Pending)
	}
}
