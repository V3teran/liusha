//go:build integration

package runner

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"
)

// recordingPublisher 记录广播到对话的消息（实时流的替身）。
type recordingPublisher struct {
	mu   sync.Mutex
	sent []string
}

func (p *recordingPublisher) PublishMessage(_ context.Context, conversationID string, _ any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, conversationID)
	return nil
}

// 自动恢复连续中断达上限、任务落 aborted 时，对话时间线要有终态节点（与用户停止、
// 运行出错同一口径）：否则对话里任务停在「工作中」的最后一条消息，看不出它已被终止及原因。
func TestRunRecovery_AbortAfterRetriesWritesTimeline(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	tasks := task.NewStore(pool)
	runs := agentrun.NewStore(pool)
	convs := conversation.NewStore(pool)

	tk, err := tasks.Create(ctx, task.NewParams{AssignmentID: dbtest.SeedAssignment(t, pool), Goal: "扫描 http://a.test"})
	if err != nil {
		t.Fatal(err)
	}
	conv, err := convs.CreateConversation(ctx, "a.test", tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 已中断 2 次（error run），且当前没有存活 run：搁浅任务，达上限 2
	for i := 0; i < 2; i++ {
		id, err := runs.Create(ctx, agentrun.NewParams{TaskID: tk.ID})
		if err != nil {
			t.Fatal(err)
		}
		if err := runs.SetError(ctx, id); err != nil {
			t.Fatal(err)
		}
	}

	pub := &recordingPublisher{}
	m := &RunRecoveryMonitor{
		pool: pool, agentRuns: runs, tasks: tasks, conversations: convs, publisher: pub,
		logger: zerolog.New(io.Discard), timeout: 1, maxAttempts: 2,
	}
	if err := m.recoverOnce(ctx); err != nil {
		t.Fatal(err)
	}

	got, err := tasks.GetByID(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusAborted {
		t.Fatalf("达上限的任务应落 aborted，得 %s", got.Status)
	}
	msgs, err := convs.ListMessages(ctx, conv.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, msg := range msgs {
		var meta struct{ Kind, Status, Reason string }
		if json.Unmarshal(msg.Metadata, &meta) == nil && meta.Kind == "task_status" && meta.Status == conversation.TaskStatusAborted {
			found = meta.Reason != ""
		}
	}
	if !found {
		t.Fatalf("对话时间线应有带原因的终止节点，得 %d 条消息", len(msgs))
	}
	if len(pub.sent) != 1 || pub.sent[0] != conv.ID {
		t.Errorf("终止节点应广播到对话 %s，得 %v", conv.ID, pub.sent)
	}

	// 再巡检一次：任务已终止，不重复落节点
	if err := m.recoverOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(pub.sent) != 1 {
		t.Errorf("已终止的任务不应重复落节点，得 %d 次广播", len(pub.sent))
	}
}

// 孤儿 pending run：入队后队列里已没有它（Redis 数据丢失、处理中进程被硬杀后归档），run 行永远
// 停在 pending，搁浅规则又把它算作存活 run——任务卡在进行中。恢复监控按队列实况收掉它（从未执行，
// 不计入重投上限）并在同一轮重投；队列里还在排的、刚建不久的、队列查不了的都不动。
func TestRunRecovery_OrphanPendingRunIsRedispatched(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	tasks := task.NewStore(pool)
	runs := agentrun.NewStore(pool)
	mr := miniredis.RunT(t)
	wc := worker.NewClient(asynq.RedisClientOpt{Addr: mr.Addr()})
	t.Cleanup(func() { _ = wc.Close() })

	newTaskWithPending := func(goal string, age time.Duration, enqueue bool) (string, string) {
		t.Helper()
		tk, err := tasks.Create(ctx, task.NewParams{AssignmentID: dbtest.SeedAssignment(t, pool), Goal: goal})
		if err != nil {
			t.Fatal(err)
		}
		runID, err := runs.Create(ctx, agentrun.NewParams{TaskID: tk.ID})
		if err != nil {
			t.Fatal(err)
		}
		if enqueue {
			if _, _, err := wc.Enqueue(ctx, worker.RoleAgent, worker.Payload{AgentRunID: runID, TaskID: tk.ID}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := pool.Exec(ctx, `UPDATE agent_run SET created_at = now() - $2 * interval '1 second' WHERE id = $1`,
			runID, int(age.Seconds())); err != nil {
			t.Fatal(err)
		}
		return tk.ID, runID
	}
	orphanTask, orphanRun := newTaskWithPending("扫描 http://orphan.test", 10*time.Minute, false)
	_, queuedRun := newTaskWithPending("扫描 http://queued.test", 10*time.Minute, true)
	_, freshRun := newTaskWithPending("扫描 http://fresh.test", 0, false)

	m := &RunRecoveryMonitor{
		pool: pool, wc: wc, agentRuns: runs, tasks: tasks, conversations: conversation.NewStore(pool),
		publisher: &recordingPublisher{}, logger: zerolog.New(io.Discard), timeout: 6 * time.Minute, maxAttempts: 4,
	}
	if err := m.recoverOnce(ctx); err != nil {
		t.Fatal(err)
	}

	status := func(id string) agentrun.Status {
		t.Helper()
		r, err := runs.GetByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return r.Status
	}
	if got := status(orphanRun); got != agentrun.StatusAborted {
		t.Fatalf("孤儿 pending run 应落 aborted，得 %s", got)
	}
	if got := status(queuedRun); got != agentrun.StatusPending {
		t.Errorf("队列里还在排的 run 不得回收，得 %s", got)
	}
	if got := status(freshRun); got != agentrun.StatusPending {
		t.Errorf("刚建的 run（生产者可能尚未入队）不得回收，得 %s", got)
	}
	latest, err := runs.LatestByTask(ctx, orphanTask)
	if err != nil || latest == nil {
		t.Fatalf("孤儿任务应在同一轮重投: %v", err)
	}
	if latest.ID == orphanRun || latest.Status != agentrun.StatusPending {
		t.Fatalf("应有新的 pending run，得 %+v", latest)
	}
	if queued, err := wc.RunQueued(latest.ID); err != nil || !queued {
		t.Fatalf("重投的 run 应已入队: queued=%v err=%v", queued, err)
	}
	if tk, _ := tasks.GetByID(ctx, orphanTask); tk.Status != task.StatusActive {
		t.Errorf("孤儿 run 从未执行，任务应保持进行中，得 %s", tk.Status)
	}

	// 队列查不了（Redis 不可达）时不得把正常排队的 run 当孤儿收掉。
	_, unknownRun := newTaskWithPending("扫描 http://unknown.test", 10*time.Minute, true)
	mr.Close()
	if err := m.recoverOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := status(unknownRun); got != agentrun.StatusPending {
		t.Errorf("队列状态未知时不得回收，得 %s", got)
	}
}

// 僵尸 run 的判定只看存活心跳：在跑的 run 超过 timeout 没有续约即回收——进程被硬杀如此，
// 活着但卡住了（心跳停止续约）也如此。心跳新鲜的 run 不碰，哪怕它一条模型调用、工具调用的
// 记录都没有：一个有超时的长操作在途时正是这样（此前按调用记录判定，一个要跑十分钟的命令
// 会在第 6 分钟被回收重投）。暂停中、已终态的任务只收行、不重投。
func TestRunRecovery_ZombieIsRunWithoutHeartbeat(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	tasks := task.NewStore(pool)
	runs := agentrun.NewStore(pool)
	mr := miniredis.RunT(t)
	wc := worker.NewClient(asynq.RedisClientOpt{Addr: mr.Addr()})
	t.Cleanup(func() { _ = wc.Close() })

	running := func(goal string) (string, string) {
		t.Helper()
		tk, err := tasks.Create(ctx, task.NewParams{AssignmentID: dbtest.SeedAssignment(t, pool), Goal: goal})
		if err != nil {
			t.Fatal(err)
		}
		runID, err := runs.Create(ctx, agentrun.NewParams{TaskID: tk.ID})
		if err != nil {
			t.Fatal(err)
		}
		if err := runs.SetRunning(ctx, runID); err != nil {
			t.Fatal(err)
		}
		return tk.ID, runID
	}
	stall := func(runID string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE agent_run SET heartbeat_at = now() - interval '10 minutes' WHERE id = $1`, runID); err != nil {
			t.Fatal(err)
		}
	}
	status := func(id string) agentrun.Status {
		t.Helper()
		r, err := runs.GetByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return r.Status
	}
	liveRuns := func(taskID string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM agent_run WHERE task_id = $1 AND status IN ('pending','running')`, taskID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	staleTask, staleRun := running("扫描 http://stale.test")
	freshTask, freshRun := running("扫描 http://fresh.test")
	pausedTask, pausedRun := running("扫描 http://paused.test")
	endedTask, endedRun := running("扫描 http://ended.test")
	stall(staleRun)
	stall(pausedRun)
	stall(endedRun)
	if _, err := tasks.MarkPaused(ctx, pausedTask); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Abort(ctx, endedTask, "测试终止"); err != nil {
		t.Fatal(err)
	}

	m := &RunRecoveryMonitor{
		pool: pool, wc: wc, agentRuns: runs, tasks: tasks, conversations: conversation.NewStore(pool),
		publisher: &recordingPublisher{}, logger: zerolog.New(io.Discard), timeout: 6 * time.Minute, maxAttempts: 4,
	}
	if err := m.recoverOnce(ctx); err != nil {
		t.Fatal(err)
	}

	if got := status(staleRun); got != agentrun.StatusError {
		t.Fatalf("心跳停止的 run 应落 error，得 %s", got)
	}
	if n := liveRuns(staleTask); n != 1 {
		t.Fatalf("进行中的任务应重投一个新的 run，实际存活 run %d 个", n)
	}
	if got := status(freshRun); got != agentrun.StatusRunning {
		t.Fatalf("心跳新鲜的 run 不得回收（它没有任何调用记录，正像一个长操作在途的 run），得 %s", got)
	}
	if got := status(pausedRun); got != agentrun.StatusAborted {
		t.Fatalf("暂停中任务的僵尸 run 应只收行，得 %s", got)
	}
	if n := liveRuns(pausedTask); n != 0 {
		t.Fatalf("暂停中的任务不重投，实际存活 run %d 个", n)
	}
	if got := status(endedRun); got != agentrun.StatusAborted {
		t.Fatalf("已终态任务的僵尸 run 应只收行，得 %s", got)
	}
	if n := liveRuns(endedTask); n != 0 {
		t.Fatalf("已终态的任务不重投，实际存活 run %d 个", n)
	}

	// 判定跟着心跳走：原本新鲜的 run，心跳一停，下一轮即被回收。
	stall(freshRun)
	if err := m.recoverOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := status(freshRun); got != agentrun.StatusError {
		t.Fatalf("心跳停止后应落 error，得 %s", got)
	}
	if n := liveRuns(freshTask); n != 1 {
		t.Fatalf("进行中的任务应重投一个新的 run，实际存活 run %d 个", n)
	}
}
