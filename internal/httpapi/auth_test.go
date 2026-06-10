package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRequireAPIKey_StreamCookieBypass(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := []byte("secret-32-bytes-long-aaaaaaaaaaaa")
	r := gin.New()
	r.Use(RequireAPIKey("the-key", secret))
	r.GET("/conversations/:id/stream", func(c *gin.Context) { c.String(200, "ok") })

	// 合法 cookie（无 header）→ 放行
	tok := signStreamToken(secret, "conv-1", time.Now().Add(time.Minute))
	req := httptest.NewRequest("GET", "/conversations/conv-1/stream", nil)
	req.AddCookie(&http.Cookie{Name: streamCookieName, Value: tok})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("合法 cookie 应放行，得 %d", w.Code)
	}

	// 错 convID 的 cookie + 无 header → 401
	req2 := httptest.NewRequest("GET", "/conversations/conv-2/stream", nil)
	req2.AddCookie(&http.Cookie{Name: streamCookieName, Value: tok}) // tok 绑 conv-1
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != 401 {
		t.Errorf("convID 不匹配的 cookie 应 401，得 %d", w2.Code)
	}

	// 无 cookie 无 header → 401
	req3 := httptest.NewRequest("GET", "/conversations/conv-1/stream", nil)
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	if w3.Code != 401 {
		t.Errorf("无凭证应 401，得 %d", w3.Code)
	}
}
