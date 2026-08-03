package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/V3teran/liusha/internal/conversation"
)

type fakeChat struct{}

func (fakeChat) StartChatScan(_ context.Context, brief, roleID string) (string, string, error) {
	return "conv-abc", "scan-xyz", nil
}

func TestChatHandler_SetsStreamCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := []byte("secret-32-bytes-long-bbbbbbbbbbbb")
	r := gin.New()
	r.POST("/chat", chatHandler(fakeChat{}, secret, true))

	req := httptest.NewRequest("POST", "/chat", strings.NewReader(`{"brief":"扫这个","role_id":"web-pentest"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var ck *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == streamCookieName {
			ck = c
		}
	}
	if ck == nil {
		t.Fatal("响应未 Set-Cookie liusha_stream")
	}
	if !ck.HttpOnly {
		t.Error("cookie 应 HttpOnly")
	}
	if !ck.Secure {
		t.Error("secure=true 时 cookie 应带 Secure")
	}
	if !verifyStreamToken(secret, ck.Value, "conv-abc") {
		t.Error("cookie 值应是 conv-abc 的合法 token")
	}
}

type fakeFollowUp struct {
	convID       string
	calledBrief  string
	handleIntent string
	handleBusy   bool
}

func (f *fakeFollowUp) HandleMessage(_ context.Context, convID, content string) (string, bool, error) {
	f.calledBrief = content
	return f.handleIntent, f.handleBusy, nil
}

func TestFollowUpHandler_QA(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fu := &fakeFollowUp{convID: "c1", handleIntent: "qa"}
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
	fu := &fakeFollowUp{convID: "c1", handleIntent: "action", handleBusy: false}
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

func TestFollowUpHandler_ActionBusy_409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fu := &fakeFollowUp{convID: "c1", handleIntent: "action", handleBusy: true}
	r := gin.New()
	r.POST("/conversations/:id/messages", followUpHandler(fu))
	req := httptest.NewRequest("POST", "/conversations/c1/messages", strings.NewReader(`{"content":"再扫"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 409 {
		t.Errorf("action 忙应 409，得 %d", w.Code)
	}
}

type abortFn func(context.Context, string) error

func (f abortFn) AbortConversationScan(ctx context.Context, convID string) error {
	return f(ctx, convID)
}

func TestAbortHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	called := ""
	r := gin.New()
	r.POST("/conversations/:id/abort", abortConversationHandler(abortFn(func(_ context.Context, convID string) error {
		called = convID
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

// TestDeleteHandler_ScanActive_409：关联扫描仍在跑 → 409（先停后删的服务端兜底）。
func TestDeleteHandler_ScanActive_409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.DELETE("/conversations/:id", deleteConversationHandler(deleteFn(func(_ context.Context, _ string) error {
		return ErrConversationScanActive
	})))
	req := httptest.NewRequest("DELETE", "/conversations/c1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 409 {
		t.Errorf("扫描进行中删除应 409，得 %d (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "scan_active") {
		t.Errorf("409 响应应含 scan_active 标记，得 %s", w.Body.String())
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

// fakeConversations 满足 ConversationsAPI，记录 ListConversations 收到的 limit/offset/scenarioID。
type fakeConversations struct {
	gotLimit, gotOffset int
	gotScenarioID       string
	convs               []conversation.Conversation
	hasMore             bool
	getMessageResult    conversation.Message
	getMessageErr       error
}

func (f *fakeConversations) ListConversations(_ context.Context, limit, offset int, scenarioID string) ([]conversation.Conversation, bool, error) {
	f.gotLimit, f.gotOffset, f.gotScenarioID = limit, offset, scenarioID
	return f.convs, f.hasMore, nil
}

func (f *fakeConversations) ListMessages(_ context.Context, _ string, _ int64, _ int) ([]conversation.Message, error) {
	return nil, nil
}

func (f *fakeConversations) GetMessage(_ context.Context, _, _ string) (conversation.Message, error) {
	if f.getMessageErr != nil {
		return conversation.Message{}, f.getMessageErr
	}
	return f.getMessageResult, nil
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
	if fc.gotScenarioID != "" {
		t.Errorf("未传 scenario_id 应默认空（不过滤），得 %q", fc.gotScenarioID)
	}
}

// TestListConversationsHandler_ScenarioFilterPassedToStore：scenario_id query 透传给 store，
// 分页边界必须建立在过滤后的集合上（否则页码与实际条数会错位）。
func TestListConversationsHandler_ScenarioFilterPassedToStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fc := &fakeConversations{}
	r := gin.New()
	r.GET("/conversations", listConversationsHandler(fc))
	req := httptest.NewRequest("GET", "/conversations?scenario_id=traffic-analysis", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if fc.gotScenarioID != "traffic-analysis" {
		t.Errorf("scenario_id=traffic-analysis 应透传给 store，得 %q", fc.gotScenarioID)
	}
}

// TestMessageDetailHandler：按需拉单条消息（供执行图详情面板点开看原文），
// 覆盖 200/404/500 三条路径——404 专门验证 pgx.ErrNoRows 被正确转译，不是泄漏成 500。
func TestMessageDetailHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("200 返回消息正文", func(t *testing.T) {
		fc := &fakeConversations{getMessageResult: conversation.Message{
			ID: "m1", ConversationID: "c1", Content: "工具输出原文",
		}}
		r := gin.New()
		r.GET("/conversations/:id/messages/:msg_id", messageDetailHandler(fc))
		req := httptest.NewRequest("GET", "/conversations/c1/messages/m1", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var got conversation.Message
		if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.ID != "m1" || got.Content != "工具输出原文" {
			t.Errorf("响应不符：%+v", got)
		}
	})

	t.Run("消息不存在返回 404", func(t *testing.T) {
		fc := &fakeConversations{getMessageErr: pgx.ErrNoRows}
		r := gin.New()
		r.GET("/conversations/:id/messages/:msg_id", messageDetailHandler(fc))
		req := httptest.NewRequest("GET", "/conversations/c1/messages/missing", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("status=%d 期望 404", w.Code)
		}
	})

	t.Run("其他错误返回 500", func(t *testing.T) {
		fc := &fakeConversations{getMessageErr: errors.New("db 连接超时")}
		r := gin.New()
		r.GET("/conversations/:id/messages/:msg_id", messageDetailHandler(fc))
		req := httptest.NewRequest("GET", "/conversations/c1/messages/m1", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("status=%d 期望 500", w.Code)
		}
	})
}
