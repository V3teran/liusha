//go:build integration

package runner

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/task"
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

// 自动恢复连续中断达上限、任务落 aborted 时，对话时间线要有终态节点（与用户停止、心跳回收、
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
		id, err := runs.Create(ctx, agentrun.NewParams{TaskID: tk.ID, Role: "planner"})
		if err != nil {
			t.Fatal(err)
		}
		if err := runs.SetError(ctx, id, "runner 中断"); err != nil {
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
