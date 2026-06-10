package httpapi

import (
	"context"
	"fmt"
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
	convID, scanID, status string
	calledBrief            string
}

func (f *fakeFollowUp) GetConversationScan(_ context.Context, convID string) (scanID, scanStatus string, err error) {
	if convID != f.convID {
		return "", "", errFollowUpNotFound
	}
	return f.scanID, f.status, nil
}
func (f *fakeFollowUp) AppendUserMessage(_ context.Context, _, _ string) error { return nil }
func (f *fakeFollowUp) FollowUpScan(_ context.Context, scanID, convID, scenarioID, brief string) (string, error) {
	f.calledBrief = brief
	return "hunter-1", nil
}

var errFollowUpNotFound = fmt.Errorf("not found")

func TestFollowUpHandler_IdleScan_TriggersRun(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fu := &fakeFollowUp{convID: "c1", scanID: "s1", status: "completed"}
	r := gin.New()
	r.POST("/conversations/:id/messages", followUpHandler(fu))
	req := httptest.NewRequest("POST", "/conversations/c1/messages", strings.NewReader(`{"content":"深挖那个 IDOR"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
	}
	if fu.calledBrief != "深挖那个 IDOR" {
		t.Errorf("brief 应透传给 FollowUpScan，得 %q", fu.calledBrief)
	}
}

func TestFollowUpHandler_BusyScan_409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fu := &fakeFollowUp{convID: "c1", scanID: "s1", status: "active"}
	r := gin.New()
	r.POST("/conversations/:id/messages", followUpHandler(fu))
	req := httptest.NewRequest("POST", "/conversations/c1/messages", strings.NewReader(`{"content":"再测下"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 409 {
		t.Errorf("scan 正在跑应 409 busy，得 %d", w.Code)
	}
}
