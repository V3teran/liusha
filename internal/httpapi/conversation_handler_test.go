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
