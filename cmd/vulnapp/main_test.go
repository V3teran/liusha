package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// testLogger 返回禁言 logger，避免单测污染输出。
func testLogger() zerolog.Logger {
	return zerolog.New(io.Discard)
}

// 登录返回 200 + Set-Cookie 含 sessionId
func TestVulnapp_Login(t *testing.T) {
	r := newRouter(testLogger())

	body := bytes.NewBufferString(`{"name":"admin"}`)
	req := httptest.NewRequest(http.MethodPost, "/login", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("login 状态码错误: want 200, got %d, body=%s", w.Code, w.Body.String())
	}

	setCookie := w.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "session=admin_sess_") {
		t.Fatalf("未返回 session cookie: Set-Cookie=%q", setCookie)
	}

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
	r := newRouter(testLogger())

	body := bytes.NewBufferString(`{"name":"hacker"}`)
	req := httptest.NewRequest(http.MethodPost, "/login", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("未知用户应返回 401, got %d", w.Code)
	}
}

// 水平越权：test 用户能读 m233241 的订单
func TestVulnapp_OrderHorizontalEsc(t *testing.T) {
	r := newRouter(testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/bac/order/9", nil)
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

// 垂直越权：普通用户 test 能访问 admin/users
func TestVulnapp_AdminUsers_VerticalEsc(t *testing.T) {
	r := newRouter(testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/bac/admin/users", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "test_sess_d4e5f6"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("垂直越权预期 200（漏洞）, got %d, body=%s", w.Code, w.Body.String())
	}
}

// 未授权访问：无 cookie 也能调用 admin/delete
func TestVulnapp_AdminDelete_NoAuth(t *testing.T) {
	r := newRouter(testLogger())

	body := bytes.NewBufferString(`{"uid":"42"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/bac/admin/delete", body)
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

// /api/bac/profile baseline：登录用户拿自己资料
func TestVulnapp_Profile(t *testing.T) {
	r := newRouter(testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/bac/profile", nil)
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

// 不存在订单返回 404（已登录）
func TestVulnapp_OrderNotFound(t *testing.T) {
	r := newRouter(testLogger())

	req := httptest.NewRequest(http.MethodGet, "/api/bac/order/999", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "test_sess_d4e5f6"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("不存在订单应 404, got %d", w.Code)
	}
}

// anonymous 访问需登录接口被 requireLogin 拒：200 + body 含 Unauthorized。
// BAC SKILL.md 第 80 行：body 含 "Unauthorized" 视为被拒绝。
//
// /api/bac/admin/delete 不在此用例 — 它故意不调 requireLogin（unauthorized_access 漏洞）。
func TestVulnapp_Anonymous_Rejected(t *testing.T) {
	r := newRouter(testLogger())

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/bac/profile"},
		{http.MethodGet, "/api/bac/order/7"},
		{http.MethodGet, "/api/bac/admin/users"},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("%s %s anonymous 应 200+detail, got %d", tc.method, tc.path, w.Code)
		}
		if !strings.Contains(w.Body.String(), "Unauthorized") {
			t.Fatalf("%s %s anonymous 响应应含 Unauthorized: %s", tc.method, tc.path, w.Body.String())
		}
	}
}

// /health 返回 ok
func TestVulnapp_Health(t *testing.T) {
	r := newRouter(testLogger())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("health 应 200, got %d", w.Code)
	}
}
