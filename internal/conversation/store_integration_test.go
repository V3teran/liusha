//go:build integration

package conversation_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/db"

	"github.com/rs/zerolog"
)

// 用 dev DB（已跑过 0066 migration）测 store 往返：验证 scan 字段顺序、COALESCE/NULLIF、
// bigserial seq 递增、增量拉取。需 LIUSHA_POSTGRES_DSN；未设则 skip。
//
//	LIUSHA_POSTGRES_DSN='postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable' \
//	  go test -tags integration ./internal/conversation/
func TestConversationStore_RoundTrip(t *testing.T) {
	dsn := os.Getenv("LIUSHA_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("LIUSHA_POSTGRES_DSN 未设，跳过 conversation store 集成测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.NewPgPool(ctx, dsn, zerolog.Nop(), 5, 1, 5, 0)
	if err != nil {
		t.Fatalf("连库: %v", err)
	}
	defer pool.Close()
	store := conversation.NewStore(pool)

	// 1. 建会话（空 title/taskID → 应存 NULL，读回空串）
	c, err := store.CreateConversation(ctx, "", "")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if c.ID == "" {
		t.Fatal("会话 ID 为空")
	}
	if c.Title != "" || c.TaskID != "" {
		t.Errorf("空字段应读回空串: title=%q task=%q", c.Title, c.TaskID)
	}
	// conversation.status 僵尸字段已退役（不读入 Conversation）——运行态派生自关联任务，见 RunStatus。
	t.Cleanup(func() {
		// message 经 FK CASCADE 随会话删。
		fctx, fc := context.WithTimeout(context.Background(), 10*time.Second)
		defer fc()
		_, _ = pool.Exec(fctx, "DELETE FROM conversation WHERE id=$1", c.ID)
	})

	// 2. 追加普通消息 + 带 metadata 的 event 消息
	m1, err := store.AppendMessage(ctx, c.ID, conversation.RoleUser, conversation.KindMessage, "扫这个站找 SQLi", nil)
	if err != nil {
		t.Fatalf("AppendMessage 1: %v", err)
	}
	meta := json.RawMessage(`{"tool":"run_command","tag":"sqlmap"}`)
	m2, err := store.AppendMessage(ctx, c.ID, conversation.RoleTool, conversation.KindEvent, "sqlmap 注入确认", meta)
	if err != nil {
		t.Fatalf("AppendMessage 2: %v", err)
	}

	// 3. seq 应递增（稳定顺序键）
	if m2.Seq <= m1.Seq {
		t.Errorf("seq 应递增: m1=%d m2=%d", m1.Seq, m2.Seq)
	}

	// 4. ListMessages 全量回看
	msgs, err := store.ListMessages(ctx, c.ID, 0, 100)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("应 2 条消息，得 %d", len(msgs))
	}
	if msgs[0].Seq != m1.Seq || msgs[1].Seq != m2.Seq {
		t.Errorf("消息应按 seq 升序: %d,%d vs %d,%d", msgs[0].Seq, msgs[1].Seq, m1.Seq, m2.Seq)
	}
	if msgs[0].Content != "扫这个站找 SQLi" || msgs[0].Role != conversation.RoleUser {
		t.Errorf("消息1 内容/角色错: %q / %q", msgs[0].Content, msgs[0].Role)
	}
	if string(msgs[1].Metadata) == "" || msgs[1].Kind != conversation.KindEvent {
		t.Errorf("event 消息 metadata/kind 错: %q / %q", msgs[1].Metadata, msgs[1].Kind)
	}

	// 5. 增量拉取：seq>m1.Seq 只应得 m2
	inc, err := store.ListMessages(ctx, c.ID, m1.Seq, 100)
	if err != nil {
		t.Fatalf("增量 ListMessages: %v", err)
	}
	if len(inc) != 1 || inc[0].Seq != m2.Seq {
		t.Errorf("增量拉取应只得 m2，得 %d 条", len(inc))
	}

	// 6. SetTitle 回填 + GetConversation 读回
	if err := store.SetTitle(ctx, c.ID, "SQLi 扫描会话"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	got, err := store.GetConversation(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if got.Title != "SQLi 扫描会话" {
		t.Errorf("标题回填失败: %q", got.Title)
	}
}

// SetMessageMetadata 是下发收口点的落位改写通道：附件引用随落位从暂存路径
// 改写为 inbox 路径。会话+seq 双重定位——改错行 = 芯片指向别人的文件。
func TestSetMessageMetadata(t *testing.T) {
	dsn := os.Getenv("LIUSHA_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("LIUSHA_POSTGRES_DSN 未设，跳过")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.NewPgPool(ctx, dsn, zerolog.Nop(), 5, 1, 5, 0)
	if err != nil {
		t.Fatalf("连库: %v", err)
	}
	defer pool.Close()
	store := conversation.NewStore(pool)

	c, err := store.CreateConversation(ctx, "meta-relink", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM conversation WHERE id=$1", c.ID) })

	m1, err := store.AppendMessage(ctx, c.ID, conversation.RoleUser, conversation.KindMessage, "第一条", nil)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := store.AppendMessage(ctx, c.ID, conversation.RoleUser, conversation.KindMessage, "第二条", nil)
	if err != nil {
		t.Fatal(err)
	}

	relinked, _ := json.Marshal(map[string]any{"attachments": []map[string]any{
		{"name": "a.txt", "size": 3, "path": "asg/task/inbox/a.txt"},
	}})
	if err := store.SetMessageMetadata(ctx, c.ID, m2.Seq, relinked); err != nil {
		t.Fatalf("改写第二条: %v", err)
	}

	// 只动目标行：第一条 metadata 不受波及，第二条读回新值
	got1, err := store.GetMessage(ctx, c.ID, m1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got1.Metadata != nil {
		t.Fatalf("第一条 metadata 不应被波及: %s", got1.Metadata)
	}
	got2, err := store.GetMessage(ctx, c.ID, m2.ID)
	if err != nil {
		t.Fatal(err)
	}
	// jsonb 会规范化键序，按值比较而非字节串比较
	var want, got any
	if err := json.Unmarshal(relinked, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got2.Metadata, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("第二条 metadata 应被改写: %s", got2.Metadata)
	}

	// 不存在的 seq：明确报错（静默成功会让改写丢失无从察觉）
	if err := store.SetMessageMetadata(ctx, c.ID, 999999999, relinked); err == nil {
		t.Fatal("改写不存在的消息应报错")
	}
}
