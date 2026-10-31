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
	req.AddCookie(&http.Cookie{Name: streamCookieName, Value: tok}) // #nosec G124 // 测试请求，非真实 Set-Cookie
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("合法 cookie 应放行，得 %d", w.Code)
	}

	// 错 conversationID 的 cookie + 无 header → 401
	req2 := httptest.NewRequest("GET", "/conversations/conv-2/stream", nil)
	req2.AddCookie(&http.Cookie{Name: streamCookieName, Value: tok}) // #nosec G124 // 测试请求，非真实 Set-Cookie；tok 绑 conv-1
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != 401 {
		t.Errorf("conversationID 不匹配的 cookie 应 401，得 %d", w2.Code)
	}

	// 无 cookie 无 header → 401
	req3 := httptest.NewRequest("GET", "/conversations/conv-1/stream", nil)
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	if w3.Code != 401 {
		t.Errorf("无凭证应 401，得 %d", w3.Code)
	}
}

// TestRequireAPIKey_StreamCookieBypass_APIGroup：路由挂在 /api 组下（生产形态）时旁路仍须生效。
// 回归背景：中间件挂根路由、FullPath 实为 /api/conversations/:id/stream，精确比较曾使
// cookie 旁路成死代码——EventSource（无法带 header）永远 401，实时推送全断。
func TestRequireAPIKey_StreamCookieBypass_APIGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := []byte("secret-32-bytes-long-aaaaaaaaaaaa")
	r := gin.New()
	r.Use(RequireAPIKey("the-key", secret))
	api := r.Group("/api")
	api.GET("/conversations/:id/stream", func(c *gin.Context) { c.String(200, "ok") })

	tok := signStreamToken(secret, "conv-1", time.Now().Add(time.Minute))
	req := httptest.NewRequest("GET", "/api/conversations/conv-1/stream", nil)
	req.AddCookie(&http.Cookie{Name: streamCookieName, Value: tok}) // #nosec G124 // 测试请求，非真实 Set-Cookie
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("/api 组下合法 cookie 应放行，得 %d", w.Code)
	}

	// 组前缀下无 cookie 无 header 仍须 401（旁路不得扩大成全放行）
	req2 := httptest.NewRequest("GET", "/api/conversations/conv-1/stream", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != 401 {
		t.Errorf("/api 组下无凭证应 401，得 %d", w2.Code)
	}
}
