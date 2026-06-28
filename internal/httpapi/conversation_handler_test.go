package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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
