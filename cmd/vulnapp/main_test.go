package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// 登录返回 200 + Set-Cookie 含 sessionId
func TestVulnapp_Login(t *testing.T) {
	r := newRouter()

	body := bytes.NewBufferString(`{"name":"admin"}`)
	req := httptest.NewRequest(http.MethodPost, "/login", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("login 状态码错误: want 200, got %d, body=%s", w.Code, w.Body.String())
	}

	// 验证 Set-Cookie 头包含 session=
	setCookie := w.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "session=admin_sess_") {
		t.Fatalf("未返回 session cookie: Set-Cookie=%q", setCookie)
	}

	// 验证 body
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp["ok"] != true {
		t.Fatalf("ok 字段错误: %v", resp)
	}
}

// 登录未知用户返回 401
func TestVulnapp_Login_Unknown(t *testing.T) {
	r := newRouter()

	body := bytes.NewBufferString(`{"name":"hacker"}`)
	req := httptest.NewRequest(http.MethodPost, "/login", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未知用户应返回 401, got %d", w.Code)
	}
}

// IDOR 漏洞验证：test 用户能读 m233241 的订单
func TestVulnapp_OrderIDOR(t *testing.T) {
	r := newRouter()

	// 用 test 的 cookie 访问 m233241 的订单
	req := httptest.NewRequest(http.MethodGet, "/api/order/9", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "test_sess_d4e5f6"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("水平越权预期 200（漏洞）, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if resp["owner"] != "m233241" {
		t.Fatalf("订单 owner 应为 m233241: %v", resp)
	}
}

// 垂直越权验证：普通用户 test 能访问 admin/users
func TestVulnapp_AdminUsers_VertEsc(t *testing.T) {
	r := newRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "test_sess_d4e5f6"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("垂直越权预期 200（漏洞）, got %d, body=%s", w.Code, w.Body.String())
	}
}

// 未授权访问验证：无 cookie 也能调用 admin/user/delete
func TestVulnapp_AdminDelete_NoAuth(t *testing.T) {
	r := newRouter()

	body := bytes.NewBufferString(`{"uid":"42"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/user/delete", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("未授权访问预期 200（漏洞）, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if resp["by"] != "anonymous" {
		t.Fatalf("无 cookie 时 by 字段应为 anonymous: %v", resp)
	}
}

// /api/profile 验证 whoami 中间件
func TestVulnapp_Profile(t *testing.T) {
	r := newRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/profile", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "admin_sess_a1b2c3"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("profile 状态码错误: %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if resp["me"] != "admin" {
		t.Fatalf("admin cookie 应解析为 admin: %v", resp)
	}
}

// 不存在订单返回 404
func TestVulnapp_OrderNotFound(t *testing.T) {
	r := newRouter()

	req := httptest.NewRequest(http.MethodGet, "/api/order/999", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("不存在订单应 404, got %d", w.Code)
	}
}
