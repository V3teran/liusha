package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/proxystatus"
)

// fakeProxyStatus 是 ProxyStatusAPI 的内存实现。
type fakeProxyStatus struct {
	rec proxystatus.Record
	ok  bool
	err error
}

func (f fakeProxyStatus) Get(context.Context) (proxystatus.Record, bool, error) {
	return f.rec, f.ok, f.err
}

func onlineRecord() proxystatus.Record {
	now := time.Now()
	return proxystatus.Record{
		StartedAt: now.Add(-time.Hour), HeartbeatAt: now, LastRequestAt: &now,
		PublicAddr: ":58588", CAPEM: "-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----\n", CAFingerprint: "AB:CD",
	}
}

func TestGetProxyStatus(t *testing.T) {
	t.Run("运行中", func(t *testing.T) {
		srv := newTestServer(t, Deps{Proxy: fakeProxyStatus{rec: onlineRecord(), ok: true}})
		defer srv.Close()
		code, body := doJSON(t, "GET", srv.URL+"/api/proxy", nil)
		if code != http.StatusOK || body["state"] != "online" || body["public_addr"] != ":58588" || body["ca_fingerprint"] != "AB:CD" {
			t.Fatalf("status=%d body=%v", code, body)
		}
		if body["last_seen_at"] == nil || body["last_request_at"] == nil {
			t.Fatalf("应带最后心跳与最近请求时间: %v", body)
		}
		if _, leaked := body["ca_pem"]; leaked {
			t.Fatalf("状态不带证书正文: %v", body)
		}
	})
	t.Run("心跳中断即离线", func(t *testing.T) {
		rec := onlineRecord()
		rec.HeartbeatAt = time.Now().Add(-time.Hour)
		srv := newTestServer(t, Deps{Proxy: fakeProxyStatus{rec: rec, ok: true}})
		defer srv.Close()
		_, body := doJSON(t, "GET", srv.URL+"/api/proxy", nil)
		if body["state"] != "offline" || body["last_seen_at"] == nil {
			t.Fatalf("应为离线并保留最后在线时间: %v", body)
		}
	})
	// 代理从未运行过是一种状态（离线、各时刻为空），不是 404。
	t.Run("从未运行过", func(t *testing.T) {
		srv := newTestServer(t, Deps{Proxy: fakeProxyStatus{}})
		defer srv.Close()
		code, body := doJSON(t, "GET", srv.URL+"/api/proxy", nil)
		if code != http.StatusOK || body["state"] != "offline" || body["last_seen_at"] != nil || body["public_addr"] != "" {
			t.Fatalf("status=%d body=%v", code, body)
		}
	})
	t.Run("读取失败", func(t *testing.T) {
		srv := newTestServer(t, Deps{Proxy: fakeProxyStatus{err: errors.New("redis down")}})
		defer srv.Close()
		if code, _ := doJSON(t, "GET", srv.URL+"/api/proxy", nil); code != http.StatusInternalServerError {
			t.Fatalf("status=%d", code)
		}
	})
}

func TestGetProxyCA(t *testing.T) {
	rec := onlineRecord()
	rec.StoppedAt = &rec.HeartbeatAt // 代理已停止也能下载
	srv := newTestServer(t, Deps{Proxy: fakeProxyStatus{rec: rec, ok: true}})
	defer srv.Close()

	resp := authedGet(t, srv.URL+"/api/proxy/ca.crt")
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != rec.CAPEM {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Disposition"); got != `attachment; filename="liusha-ca.crt"` {
		t.Fatalf("下载文件名不符: %q", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/x-x509-ca-cert" {
		t.Fatalf("Content-Type 不符: %q", got)
	}
}

// authedGet 带测试会话发 GET，返回原始响应（下载类接口的响应体不是 JSON）。
func authedGet(t *testing.T, url string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.AddCookie(testSessionCookie())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return resp
}

func TestGetProxyCA_NeverRun(t *testing.T) {
	srv := newTestServer(t, Deps{Proxy: fakeProxyStatus{}})
	defer srv.Close()
	code, body := doJSON(t, "GET", srv.URL+"/api/proxy/ca.crt", nil)
	if code != http.StatusNotFound || body["error"] == nil {
		t.Fatalf("status=%d body=%v", code, body)
	}
}
