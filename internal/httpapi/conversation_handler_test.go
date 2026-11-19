package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/attachment"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

type fakeChat struct{}

func (fakeChat) StartChatTask(_ context.Context, _ string, _ []attachment.Ref, _ *string) (string, string, error) {
	return "conv-abc", "scan-xyz", nil
}

func TestChatHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/chat", chatHandler(fakeChat{}))

	req := httptest.NewRequest("POST", "/chat", strings.NewReader(`{"brief":"扫这个"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var resp ChatResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ConversationID != "conv-abc" || resp.TaskID != "scan-xyz" {
		t.Errorf("响应不符: %+v", resp)
	}
}

type fakeFollowUp struct {
	conversationID string
	calledBrief    string
	handleIntent   string
}

func (f *fakeFollowUp) HandleMessage(_ context.Context, conversationID, content string, _ []attachment.Ref, _ *string) (string, error) {
	f.conversationID = conversationID
	f.calledBrief = content
	return f.handleIntent, nil
}

func TestFollowUpHandler_QA(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fu := &fakeFollowUp{conversationID: "c1", handleIntent: "qa"}
	r := gin.New()
	r.POST("/conversations/:id/messages", followUpHandler(fu))
	req := httptest.NewRequest("POST", "/conversations/c1/messages", strings.NewReader(`{"content":"解释下那个洞"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "qa") {
		t.Errorf("qa 应 200 且含 intent qa，得 code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestFollowUpHandler_ActionIdle_200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fu := &fakeFollowUp{conversationID: "c1", handleIntent: "action"}
	r := gin.New()
	r.POST("/conversations/:id/messages", followUpHandler(fu))
	req := httptest.NewRequest("POST", "/conversations/c1/messages", strings.NewReader(`{"content":"再扫"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("action 空闲应 200，得 %d", w.Code)
	}
}

type deleteFn func(context.Context, string) error

func (f deleteFn) DeleteConversation(ctx context.Context, id string) error { return f(ctx, id) }

// TestDeleteHandler_TaskActive_409：关联扫描仍在跑 → 409（先停后删的服务端兜底）。
func TestDeleteHandler_TaskActive_409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.DELETE("/conversations/:id", deleteConversationHandler(deleteFn(func(_ context.Context, _ string) error {
		return ErrConversationTaskActive
	})))
	req := httptest.NewRequest("DELETE", "/conversations/c1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 409 {
		t.Errorf("扫描进行中删除应 409，得 %d (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "task_active") {
		t.Errorf("409 响应应含 task_active 标记，得 %s", w.Body.String())
	}
}

// TestDeleteHandler_OK_200：无活跃扫描 → 正常删除 200。
func TestDeleteHandler_OK_200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	called := ""
	r := gin.New()
	r.DELETE("/conversations/:id", deleteConversationHandler(deleteFn(func(_ context.Context, id string) error {
		called = id
		return nil
	})))
	req := httptest.NewRequest("DELETE", "/conversations/c1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 || called != "c1" {
		t.Errorf("正常删除应调用并 200，得 code=%d called=%q", w.Code, called)
	}
}

// fakeConversations 满足 ConversationsAPI，记录 ListConversations / CountConversations 收到的筛选。
type fakeConversations struct {
	gotLimit, gotOffset int
	gotSource           string
	gotList, gotCount   conversation.ListFilter
	convs               []conversation.Conversation
	hasMore             bool
}

func (f *fakeConversations) ListConversations(_ context.Context, fl conversation.ListFilter) ([]conversation.Conversation, bool, error) {
	f.gotLimit, f.gotOffset, f.gotSource, f.gotList = fl.Limit, fl.Offset, fl.Source, fl
	return f.convs, f.hasMore, nil
}

func (f *fakeConversations) ListMessages(_ context.Context, _ string, _ int64, _ int) ([]conversation.Message, error) {
	return nil, nil
}

func (f *fakeConversations) GetConversation(_ context.Context, id string) (conversation.Conversation, error) {
	for _, c := range f.convs {
		if c.ID == id {
			return c, nil
		}
	}
	return conversation.Conversation{}, fmt.Errorf("get conversation %s: %w", id, pgx.ErrNoRows)
}

func (f *fakeConversations) CountConversations(_ context.Context, fl conversation.ListFilter) (int, error) {
	f.gotCount = fl
	return len(f.convs), nil
}

// TestListConversationsHandler_Pagination：offset/limit 透传给 store，响应带 has_more。
func TestListConversationsHandler_Pagination(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fc := &fakeConversations{
		convs:   []conversation.Conversation{{ID: "c1"}, {ID: "c2"}},
		hasMore: true,
	}
	r := gin.New()
	r.GET("/conversations", listConversationsHandler(fc))
	req := httptest.NewRequest("GET", "/conversations?limit=2&offset=30", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
	}
	if fc.gotLimit != 2 || fc.gotOffset != 30 {
		t.Errorf("limit/offset 应透传给 store，得 limit=%d offset=%d", fc.gotLimit, fc.gotOffset)
	}
	if !strings.Contains(w.Body.String(), `"has_more":true`) {
		t.Errorf("响应应含 has_more:true，得 %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"total":2`) {
		t.Errorf("响应应含同口径 total（前端总页数用），得 %s", w.Body.String())
	}
}

// TestListConversationsHandler_DefaultOffsetZero：未传 offset → 默认 0（首页）。
func TestListConversationsHandler_DefaultOffsetZero(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fc := &fakeConversations{}
	r := gin.New()
	r.GET("/conversations", listConversationsHandler(fc))
	req := httptest.NewRequest("GET", "/conversations", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if fc.gotOffset != 0 {
		t.Errorf("未传 offset 应默认 0，得 %d", fc.gotOffset)
	}
	if fc.gotLimit != 30 {
		t.Errorf("未传 limit 应默认 30，得 %d", fc.gotLimit)
	}
}

// TestListConversationsHandler_SourceFilterPassedToStore：source query 透传给 store，
// 供前端「人工下发 / 代理捕获」双 tab 按来源分流（direct/proxy）。
func TestListConversationsHandler_SourceFilterPassedToStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fc := &fakeConversations{}
	r := gin.New()
	r.GET("/conversations", listConversationsHandler(fc))
	req := httptest.NewRequest("GET", "/conversations?source=proxy", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if fc.gotSource != "proxy" {
		t.Errorf("source=proxy 应透传给 store，得 %q", fc.gotSource)
	}
}

// 关键词搜索在后端执行：列表与计数收到同一个关键词（总数与搜索结果同口径）。
func TestListConversationsHandler_SearchPassedToListAndCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fc := &fakeConversations{}
	r := gin.New()
	r.GET("/conversations", listConversationsHandler(fc))
	req := httptest.NewRequest("GET", "/conversations?search=%E5%95%86%E5%9F%8E&source=direct", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	if fc.gotList.Search != "商城" || fc.gotCount.Search != "商城" || fc.gotCount.Source != "direct" {
		t.Errorf("列表与计数应收到同一筛选，得 list=%+v count=%+v", fc.gotList, fc.gotCount)
	}
}
func TestChatHandler_TooManyAttachments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/chat", chatHandler(fakeChat{}))
	refs := make([]attachment.Ref, 11)
	body, _ := json.Marshal(ChatRequest{Brief: "x", Attachments: refs})
	req := httptest.NewRequest("POST", "/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("超界附件应 400，得 %d (%s)", w.Code, w.Body.String())
	}
}

// TestMessageHandlers_BlankTextRejected：纯空白文本且无附件等同空消息——必须在边界 400，
// 否则会建出空会话、落空白消息，并白白发起一次意图分类调用。
func TestMessageHandlers_BlankTextRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fu := &fakeFollowUp{handleIntent: "accepted"}
	r := gin.New()
	r.POST("/chat", chatHandler(fakeChat{}))
	r.POST("/conversations/:id/messages", followUpHandler(fu))
	cases := []struct{ path, body string }{
		{"/chat", `{"brief":"   "}`},
		{"/chat", `{"brief":"\n\t "}`},
		{"/conversations/c1/messages", `{"content":"  \n "}`},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 400 {
			t.Errorf("%s %s 应 400，得 %d (%s)", tc.path, tc.body, w.Code, w.Body.String())
		}
	}
	if fu.calledBrief != "" || fu.conversationID != "" {
		t.Errorf("空白追问不应进入业务层，得 conv=%q content=%q", fu.conversationID, fu.calledBrief)
	}
}

// TestMessageHandlers_BlankTextWithAttachmentAccepted：纯附件消息（文本空白）仍合法。
func TestMessageHandlers_BlankTextWithAttachmentAccepted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/chat", chatHandler(fakeChat{}))
	body, _ := json.Marshal(ChatRequest{Brief: "  ", Attachments: []attachment.Ref{{Name: "a.txt"}}})
	req := httptest.NewRequest("POST", "/chat", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("纯附件消息应 200，得 %d (%s)", w.Code, w.Body.String())
	}
}

// 单个会话：存在返回会话，不存在 404。
func TestGetConversationHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const id = "55555555-5555-4555-8555-555555555555"
	fc := &fakeConversations{convs: []conversation.Conversation{{ID: id, Title: "测试商城越权"}}}
	r := gin.New()
	r.GET("/conversations/:id", getConversationHandler(fc))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/conversations/"+id, nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "测试商城越权") {
		t.Fatalf("want 200 + 标题，得 %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/conversations/66666666-6666-4666-8666-666666666666", nil))
	if w.Code != 404 {
		t.Fatalf("不存在应 404，得 %d", w.Code)
	}
}
