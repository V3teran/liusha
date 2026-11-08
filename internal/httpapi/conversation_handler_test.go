package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/attachment"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/gin-gonic/gin"
)

type fakeChat struct{}

func (fakeChat) StartChatTask(_ context.Context, _ string, _ []attachment.Ref) (string, string, error) {
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

func (f *fakeFollowUp) HandleMessage(_ context.Context, conversationID, content string, _ []attachment.Ref) (string, error) {
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

type abortFn func(context.Context, string) error

func (f abortFn) AbortConversationTask(ctx context.Context, conversationID string) error {
	return f(ctx, conversationID)
}

func TestAbortHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	called := ""
	r := gin.New()
	r.POST("/conversations/:id/abort", abortConversationHandler(abortFn(func(_ context.Context, conversationID string) error {
		called = conversationID
		return nil
	})))
	req := httptest.NewRequest("POST", "/conversations/c1/abort", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 || called != "c1" {
		t.Errorf("abort 应调用并 200，得 code=%d called=%q", w.Code, called)
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

// fakeConversations 满足 ConversationsAPI，记录 ListConversations 收到的 limit/offset/source。
type fakeConversations struct {
	gotLimit, gotOffset int
	gotSource           string
	convs               []conversation.Conversation
	hasMore             bool
}

func (f *fakeConversations) ListConversations(_ context.Context, limit, offset int, source string) ([]conversation.Conversation, bool, error) {
	f.gotLimit, f.gotOffset, f.gotSource = limit, offset, source
	return f.convs, f.hasMore, nil
}

func (f *fakeConversations) ListMessages(_ context.Context, _ string, _ int64, _ int) ([]conversation.Message, error) {
	return nil, nil
}

func (f *fakeConversations) CountConversations(_ context.Context, _ string) (int, error) {
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
// 供前端「自主下发 / 代理模式」双 tab 按来源分流（self/proxy）。
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
