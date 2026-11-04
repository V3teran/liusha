package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/auth"
)

// mountAuth 挂中间件 + 一个业务端点，返回可 ServeHTTP 的 router。
func mountAuth(store AuthStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequireAuth(store, false))
	r.GET("/healthz", func(c *gin.Context) { c.String(200, "ok") })
	r.GET("/api/things", func(c *gin.Context) { c.String(200, "name="+c.GetString(ctxAccountName)) })
	return r
}

func mwReq(r http.Handler, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	if cookie != nil {
		req.AddCookie(cookie) // #nosec G124 // 测试注入会话凭证
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestRequireAuth_SessionAccepted 验证：有效会话 Cookie 放行且身份进 context。
func TestRequireAuth_SessionAccepted(t *testing.T) {
	r := mountAuth(newFakeAuthStore())
	w := mwReq(r, "/api/things", testSessionCookie())
	if w.Code != 200 {
		t.Fatalf("有效会话应放行，得 %d", w.Code)
	}
	if w.Body.String() != "name="+testAccountName {
		t.Errorf("context 身份不符: %q", w.Body.String())
	}
}

// TestRequireAuth_RejectsMissingOrForged 验证：无 Cookie / 伪造 token 一律 401。
func TestRequireAuth_RejectsMissingOrForged(t *testing.T) {
	r := mountAuth(newFakeAuthStore())
	if w := mwReq(r, "/api/things", nil); w.Code != 401 {
		t.Errorf("无凭证应 401，得 %d", w.Code)
	}
	forged := &http.Cookie{Name: sessionCookieName, Value: "forged"} // #nosec G101 // 测试伪造值
	if w := mwReq(r, "/api/things", forged); w.Code != 401 {
		t.Errorf("伪造 token 应 401，得 %d", w.Code)
	}
}

// TestRequireAuth_NilStoreFailClosed 验证：未装配 Auth 时除白名单外全部 401（fail-closed）。
func TestRequireAuth_NilStoreFailClosed(t *testing.T) {
	r := mountAuth(nil)
	if w := mwReq(r, "/api/things", testSessionCookie()); w.Code != 401 {
		t.Errorf("nil store 应 fail-closed 401，得 %d", w.Code)
	}
	if w := mwReq(r, "/healthz", nil); w.Code != 200 {
		t.Errorf("healthz 白名单应放行，得 %d", w.Code)
	}
}

// TestRequireAuth_Whitelist 验证：登录/初始化/登出端点免会话可达
// （登录前当然没有会话；登出须容忍带着失效会话来清 Cookie）。
func TestRequireAuth_Whitelist(t *testing.T) {
	r := mountAuth(newFakeAuthStore())
	// 未注册这些路由时走 URL.Path 白名单兜底，结果应是 404（gin NoRoute）而非 401。
	for _, p := range []string{"/api/auth/login", "/api/auth/logout"} {
		if w := mwReq(r, p, nil); w.Code == 401 {
			t.Errorf("%s 在白名单内，不应 401", p)
		}
	}
	// 注册后的白名单端点真实可达。
	r.GET("/api/auth/login", func(c *gin.Context) { c.String(200, "ok") })
	if w := mwReq(r, "/api/auth/login", nil); w.Code != 200 {
		t.Errorf("注册后的 login 白名单应放行，得 %d", w.Code)
	}
}

// TestRequireAuth_SlidingRenewal 验证：剩余不足半程的会话被滑动续期并重发 Cookie；
// 剩余充足的会话不产生写放大。
func TestRequireAuth_SlidingRenewal(t *testing.T) {
	f := newFakeAuthStore()
	// 把预置会话拨到「剩余 < 半程」。
	f.mu.Lock()
	ss := f.sessions[testSessionToken]
	ss.ExpiresAt = time.Now().Add(auth.SessionRenewAfter / 2)
	f.sessions[testSessionToken] = ss
	f.mu.Unlock()

	r := mountAuth(f)
	w := mwReq(r, "/api/things", testSessionCookie())
	if w.Code != 200 {
		t.Fatalf("会话仍有效应放行，得 %d", w.Code)
	}
	f.mu.Lock()
	slides := f.slides
	f.mu.Unlock()
	if slides != 1 {
		t.Errorf("剩余不足半程应触发一次续期，得 %d", slides)
	}
	renewed := false
	for _, ck := range w.Result().Cookies() {
		if ck.Name == sessionCookieName && ck.Value == testSessionToken && ck.MaxAge > 0 {
			renewed = true
		}
	}
	if !renewed {
		t.Error("续期时应重发会话 Cookie（同步 MaxAge）")
	}

	// 剩余充足的会话：不续期。
	w2 := mwReq(r, "/api/things", testSessionCookie())
	if w2.Code != 200 {
		t.Fatalf("第二次请求应放行，得 %d", w2.Code)
	}
	f.mu.Lock()
	slides = f.slides
	f.mu.Unlock()
	if slides != 1 {
		t.Errorf("剩余充足不应重复续期，得 %d", slides)
	}
}
