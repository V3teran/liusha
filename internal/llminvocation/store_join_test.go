//go:build integration

package llminvocation

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/task"
)

// TestListAll_JoinConversationAndAssignment 验证 ListAll 正确 JOIN 了 conversation 和 task 表，
// 返回 conversation_id 和 assignment_id。
func TestListAll_JoinConversationAndAssignment(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	invStore := NewStore(pool)
	taskStore := task.NewStore(pool)
	conversationStore := conversation.NewStore(pool)

	// 1. 创建 assignment + task
	assignmentID := dbtest.SeedAssignment(t, pool)
	tk, err := taskStore.Create(ctx, task.NewParams{
		AssignmentID: assignmentID,
		Goal:         "test brief",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	taskID := tk.ID

	// 2. 创建 conversation
	conv, err := conversationStore.CreateConversation(ctx, "test conversation", taskID)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	conversationID := conv.ID

	// 3. 写入 llm_invocation
	err = invStore.Append(ctx, Invocation{
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
	if *inv.ConversationID != conversationID {
		t.Fatalf("conversation_id mismatch: got %s, want %s", *inv.ConversationID, conversationID)
	}

	if inv.AssignmentID == nil {
		t.Fatal("assignment_id should not be nil")
	}
	if *inv.AssignmentID != assignmentID {
		t.Fatalf("assignment_id mismatch: got %s, want %s", *inv.AssignmentID, assignmentID)
	}
}

// 对话内直接发起的调用（问答、意图分类、标题生成）没有任务，记下所在对话：详情与列表带出
// 这个对话（跳转回去），来源取对话所属任务的批次（代理批次的对话落代理标签页）；按对话
// 搜索同时命中对话内调用与任务运行的调用。
func TestConversationScopedInvocation(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	invStore := NewStore(pool)
	var assignmentID string
	if err := pool.QueryRow(ctx, `INSERT INTO assignment (source, payload, title) VALUES ('proxy', '[]', 'p.test') RETURNING id`).
		Scan(&assignmentID); err != nil {
		t.Fatal(err)
	}
	tk, err := task.NewStore(pool).Create(ctx, task.NewParams{AssignmentID: assignmentID, Goal: "分析 p.test 的流量"})
	if err != nil {
		t.Fatal(err)
	}
	conv, err := conversation.NewStore(pool).CreateConversation(ctx, "p.test", tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	pureChat, err := conversation.NewStore(pool).CreateConversation(ctx, "闲聊", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, inv := range []Invocation{
		{ConversationID: &conv.ID, Role: "inspector", Provider: "p", Model: "m"},
		{TaskID: &tk.ID, Role: "planner", Provider: "p", Model: "m"},
		{ConversationID: &pureChat.ID, Role: "inspector", Provider: "p", Model: "m"},
	} {
		if err := invStore.Append(ctx, inv); err != nil {
			t.Fatal(err)
		}
	}
	if err := invStore.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	byConv, err := invStore.ListAll(ctx, ListFilter{Search: conv.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(byConv) != 2 {
		t.Fatalf("按对话搜索应命中对话内调用与任务运行的调用，得 %d 条", len(byConv))
	}
	for _, v := range byConv {
		if v.ConversationID == nil || *v.ConversationID != conv.ID {
			t.Errorf("调用 %d（%s）应带出对话 %s，得 %v", v.ID, v.Role, conv.ID, v.ConversationID)
		}
	}
	if n, err := invStore.CountAll(ctx, ListFilter{Search: conv.ID}); err != nil || n != 2 {
		t.Errorf("计数应与列表同口径（2），得 %d（%v）", n, err)
	}

	var chatID int64
	for _, v := range byConv {
		if v.Role == "inspector" {
			chatID = v.ID
		}
	}
	detail, err := invStore.GetDetailByID(ctx, chatID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.TaskID != nil {
		t.Errorf("对话内调用不属于任务运行，task_id 应为空，得 %v", *detail.TaskID)
	}
	if detail.ConversationID == nil || *detail.ConversationID != conv.ID || detail.Source != "proxy" {
		t.Errorf("详情应带出所在对话与对话所属批次的来源，得 conversation=%v source=%q", detail.ConversationID, detail.Source)
	}

	all, err := invStore.ListAll(ctx, ListFilter{Search: pureChat.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].ConversationID == nil || *all[0].ConversationID != pureChat.ID {
		t.Errorf("纯聊天对话的调用应带出该对话，得 %+v", all)
	}
}
