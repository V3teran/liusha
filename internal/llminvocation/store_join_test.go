//go:build integration

package llminvocation

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/task"
)

// TestListAll_JoinConversationAndAssignment 验证 ListAll 正确 JOIN 了 conversation 和 task 表，
// 返回 conversation_id 和 assignment_id。
func TestListAll_JoinConversationAndAssignment(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	invStore := NewStoreWithConfig(pool, config.InvocationConfig{})
	taskStore := task.NewStore(pool)
	convStore := conversation.NewStore(pool)

	// 1. 创建 assignment + task
	assignmentID := dbtest.SeedAssignment(t, pool)
	tk, err := taskStore.Create(ctx, task.NewParams{
		AssignmentID: assignmentID,
		Brief:        "test brief",
		TargetHost:   "example.com",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	taskID := tk.ID

	// 2. 创建 conversation
	conv, err := convStore.CreateConversation(ctx, "test conversation", taskID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	convID := conv.ID

	// 3. 写入 llm_invocation
	_, err = invStore.Append(ctx, Invocation{
		TaskID:    &taskID,
		Provider:  "test",
		Model:     "test-model",
		InTokens:  100,
		OutTokens: 50,
	})
	if err != nil {
		t.Fatalf("append invocation: %v", err)
	}

	// 4. flush 到数据库
	err = invStore.Flush(ctx)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}

	// 5. 查询列表，验证返回了 conversation_id 和 assignment_id
	list, err := invStore.ListAll(ctx, ListFilter{})
	if err != nil {
		t.Fatalf("list by task: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 invocation, got %d", len(list))
	}

	inv := list[0]
	if inv.ConversationID == nil {
		t.Fatal("conversation_id should not be nil")
	}
	if *inv.ConversationID != convID {
		t.Fatalf("conversation_id mismatch: got %s, want %s", *inv.ConversationID, convID)
	}

	if inv.AssignmentID == nil {
		t.Fatal("assignment_id should not be nil")
	}
	if *inv.AssignmentID != assignmentID {
		t.Fatalf("assignment_id mismatch: got %s, want %s", *inv.AssignmentID, assignmentID)
	}
}
