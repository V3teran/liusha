package proxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/filter"
	"github.com/rs/zerolog"
)

// 共用：构造一台有 dedup + sink 的 Server（不会真启 listener，仅做回调测试）。
func newTestServer(t *testing.T, listenAddr, certDir string, cfg config.ProxyConfig) (*Server, *mockSink) {
	t.Helper()
	tf := filter.NewTrafficFilter(cfg)
	sink := newMockSink()
	dedup := NewTrafficDeduplicator()
	agg := NewAggregator(time.Hour, time.Minute, 1000, dedup, sink)

	srv, err := NewServer(ServerDeps{
		Filter:     tf,
		Aggregator: agg,
		Cfg:        cfg,
		ListenAddr: listenAddr,
		CertDir:    certDir,
		Logger:     zerolog.Nop(),
	})
	if err != nil {
		t.Fatalf("NewServer 失败: %v", err)
	}
	return srv, sink
}

// 默认放行全部的 cfg；上限设大避免误截断。
func defaultCfg() config.ProxyConfig {
	return config.ProxyConfig{
		MaxRequestBodySize:  1 << 20,
		MaxResponseBodySize: 1 << 20,
	}
}

func TestNewServer_DefaultListenAddr(t *testing.T) {
	tmp := t.TempDir()
	srv, _ := newTestServer(t, "", tmp, defaultCfg())
	if srv.listenAddr != defaultListenAddr {
		t.Fatalf("期望默认监听地址 %q, 实际 %q", defaultListenAddr, srv.listenAddr)
	}
}

func TestNewServer_CertDirCreated(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "nested-not-exist-yet")
	srv, _ := newTestServer(t, "127.0.0.1:0", tmp, defaultCfg())
	if srv.certDir != tmp {
		t.Fatalf("certDir 期望 %q, 实际 %q", tmp, srv.certDir)
	}
	// proxify 的 LoadCerts 会写两个文件
	for _, name := range []string{"cacert.pem", "cakey.pem"} {
		path := filepath.Join(tmp, name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("证书文件 %s 应已生成: %v", path, err)
		}
	}
}

func TestNewServer_NilDeps(t *testing.T) {
	tmp := t.TempDir()
	if _, err := NewServer(ServerDeps{Filter: nil, Aggregator: nil, CertDir: tmp}); err == nil {
		t.Fatal("Filter / Aggregator 必填，应返回错误")
	}
}

// 拒绝过滤：方法在黑名单 → snapshot 不入 aggregator。
func TestServer_OnResponse_FilterReject(t *testing.T) {
	tmp := t.TempDir()
	cfg := defaultCfg()
	cfg.ExcludeMethods = []string{"OPTIONS"}
	srv, sink := newTestServer(t, "127.0.0.1:0", tmp, cfg)

	resp := makeResponse(t, "OPTIONS", "http://api.example.com/foo", nil, 200, []byte("{}"))
	if err := srv.onResponse(resp, nil); err != nil {
		t.Fatalf("onResponse 不应返回错误: %v", err)
	}
	// 同步 add，无 ticker；期望 0 条 pending
	if got := srv.aggregator.Pending(); got != 0 {
		t.Fatalf("被过滤的流量不应入 aggregator, pending=%d", got)
	}
	if got := sink.flushed.Load(); got != 0 {
		t.Fatalf("被过滤的流量不应触发 flush, flushed=%d", got)
	}
}

// 通过过滤：snapshot 字段正确填充。
func TestServer_OnResponse_BuildSnapshot(t *testing.T) {
	tmp := t.TempDir()
	srv, _ := newTestServer(t, "127.0.0.1:0", tmp, defaultCfg())

	reqBody := []byte(`{"a":1}`)
	respBody := []byte(`{"ok":true}`)
	resp := makeResponse(t, "POST", "http://target.example.com/api/v1/login?x=1", reqBody, 201, respBody)
	resp.Header.Set("Content-Type", "application/json")
	resp.Header.Add("Set-Cookie", "a=1")
	resp.Header.Add("Set-Cookie", "b=2")

	if err := srv.onResponse(resp, nil); err != nil {
		t.Fatalf("onResponse: %v", err)
	}

	if got := srv.aggregator.Pending(); got != 1 {
		t.Fatalf("应有 1 条 snapshot, pending=%d", got)
	}

	snap := srv.aggregator.snapshots[0]
	if snap.Method != "POST" {
		t.Errorf("Method=%q, 期望 POST", snap.Method)
	}
	if snap.Host != "target.example.com" {
		t.Errorf("Host=%q, 期望 target.example.com", snap.Host)
	}
	if !strings.HasPrefix(snap.URI, "/api/v1/login") {
		t.Errorf("URI=%q, 期望以 /api/v1/login 开头", snap.URI)
	}
	if !strings.Contains(snap.URI, "x=1") {
		t.Errorf("URI 应保留 query, 实际 %q", snap.URI)
	}
	if snap.StatusCode != 201 {
		t.Errorf("StatusCode=%d, 期望 201", snap.StatusCode)
	}
	if !bytes.Equal(snap.RequestBody, reqBody) {
		t.Errorf("RequestBody=%q, 期望 %q", snap.RequestBody, reqBody)
	}
	if !bytes.Equal(snap.ResponseBody, respBody) {
		t.Errorf("ResponseBody=%q, 期望 %q", snap.ResponseBody, respBody)
	}
	if snap.ID == "" || len(snap.ID) != 64 {
		t.Errorf("ID 期望 sha256 十六进制（64 字符）, 实际 %q", snap.ID)
	}
	if snap.ResponseHeaders["set-cookie"] != "a=1,b=2" {
		t.Errorf("多值 header 拼接异常: %q", snap.ResponseHeaders["set-cookie"])
	}
	if snap.ResponseHeaders["content-type"] != "application/json" {
		t.Errorf("Content-Type lower-case 失败: %v", snap.ResponseHeaders)
	}
	if snap.Timestamp.IsZero() {
		t.Error("Timestamp 应被设置")
	}
}

// onResponse 读完 body 后必须重建，下游可继续读到完整内容。
func TestServer_OnResponse_BodyRebuild(t *testing.T) {
	tmp := t.TempDir()
	srv, _ := newTestServer(t, "127.0.0.1:0", tmp, defaultCfg())

	reqBody := []byte("hello-req")
	respBody := []byte("hello-resp")
	resp := makeResponse(t, "GET", "http://x.example.com/", reqBody, 200, respBody)

	if err := srv.onResponse(resp, nil); err != nil {
		t.Fatalf("onResponse: %v", err)
	}

	// 重读 body：客户端模拟下游消费
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("重读 resp.Body: %v", err)
	}
	if !bytes.Equal(got, respBody) {
		t.Fatalf("resp.Body 重建失败: 期望 %q, 实际 %q", respBody, got)
	}

	gotReq, err := io.ReadAll(resp.Request.Body)
	if err != nil {
		t.Fatalf("重读 req.Body: %v", err)
	}
	if !bytes.Equal(gotReq, reqBody) {
		t.Fatalf("req.Body 重建失败: 期望 %q, 实际 %q", reqBody, gotReq)
	}
}

// 大 body 截断：MaxResponseBodySize=10，原始 body=20 → 实际只保留 10 字节。
func TestServer_OnResponse_BodyTruncated(t *testing.T) {
	tmp := t.TempDir()
	cfg := defaultCfg()
	cfg.MaxResponseBodySize = 10
	srv, _ := newTestServer(t, "127.0.0.1:0", tmp, cfg)

	full := bytes.Repeat([]byte("A"), 20)
	resp := makeResponse(t, "GET", "http://x.example.com/", nil, 200, full)
	if err := srv.onResponse(resp, nil); err != nil {
		t.Fatalf("onResponse: %v", err)
	}

	if got := srv.aggregator.Pending(); got != 1 {
		t.Fatalf("pending=%d", got)
	}
	snap := srv.aggregator.snapshots[0]
	if len(snap.ResponseBody) != 10 {
		t.Fatalf("body 应被截断到 10 字节, 实际 %d", len(snap.ResponseBody))
	}
}

func TestServer_RunCancellation(t *testing.T) {
	// 启 listener 风险大（端口冲突），仅验证 ctx 取消立即返回。
	tmp := t.TempDir()
	srv, _ := newTestServer(t, "127.0.0.1:0", tmp, defaultCfg())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消
	err := srv.Run(ctx)
	if err == nil {
		t.Fatal("ctx 已取消, Run 应返回错误")
	}
}

// 工具：构造一个带 req 的 *http.Response（body 为流式 ReadCloser）。
func makeResponse(t *testing.T, method, rawURL string, reqBody []byte, status int, respBody []byte) *http.Response {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	req := &http.Request{
		Method: method,
		URL:    u,
		Host:   u.Host,
		Header: http.Header{},
		Body:   io.NopCloser(bytes.NewReader(reqBody)),
	}
	resp := &http.Response{
		StatusCode: status,
		Header:     http.Header{},
		Request:    req,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}
	return resp
}
