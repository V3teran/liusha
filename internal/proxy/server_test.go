package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/filter"
	"github.com/rs/zerolog"
)

// fakePublisher 在内存中捕获 snapshot，避免单测起 Redis。
type fakePublisher struct {
	mu        sync.Mutex
	snapshots []*TrafficSnapshot
	failOn    error
}

func (p *fakePublisher) Publish(_ context.Context, snap *TrafficSnapshot) error {
	if p.failOn != nil {
		return p.failOn
	}
	p.mu.Lock()
	p.snapshots = append(p.snapshots, snap)
	p.mu.Unlock()
	return nil
}

func (p *fakePublisher) Snapshots() []*TrafficSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*TrafficSnapshot, len(p.snapshots))
	copy(out, p.snapshots)
	return out
}

// 共用：构造一台带 fake publisher 的 Server（不会真启 listener，仅做回调测试）。
func newTestServer(t *testing.T, listenAddr, certDir string, cfg config.ProxyConfig) (*Server, *fakePublisher) {
	t.Helper()
	tf := filter.NewTrafficFilter(cfg)
	pub := &fakePublisher{}

	srv, err := NewServer(ServerDeps{
		Filter:     tf,
		Publisher:  pub,
		Cfg:        cfg,
		ListenAddr: listenAddr,
		CertDir:    certDir,
		Source:     "external",
		Logger:     zerolog.Nop(),
	})
	if err != nil {
		t.Fatalf("NewServer 失败: %v", err)
	}
	return srv, pub
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
	if srv.listenAddr != fallbackListenAddr {
		t.Fatalf("期望默认监听地址 %q, 实际 %q", fallbackListenAddr, srv.listenAddr)
	}
}

func TestNewServer_CertDirCreated(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "nested-not-exist-yet")
	srv, _ := newTestServer(t, "127.0.0.1:0", tmp, defaultCfg())
	if srv.certDir != tmp {
		t.Fatalf("certDir 期望 %q, 实际 %q", tmp, srv.certDir)
	}
	for _, name := range []string{"cacert.pem", "cakey.pem"} {
		path := filepath.Join(tmp, name)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("证书文件 %s 应已生成: %v", path, err)
		}
	}
}

func TestNewServer_NilDeps(t *testing.T) {
	tmp := t.TempDir()
	if _, err := NewServer(ServerDeps{Filter: nil, Publisher: nil, CertDir: tmp}); err == nil {
		t.Fatal("Filter / Publisher 必填，应返回错误")
	}
}

// 拒绝过滤：方法在黑名单 → snapshot 不入 publisher。
func TestServer_OnResponse_FilterReject(t *testing.T) {
	tmp := t.TempDir()
	cfg := defaultCfg()
	cfg.ExcludeMethods = []string{"OPTIONS"}
	srv, pub := newTestServer(t, "127.0.0.1:0", tmp, cfg)

	resp := makeResponse(t, "OPTIONS", "http://api.example.com/foo", nil, 200, []byte("{}"))
	if err := srv.onResponse(resp, nil); err != nil {
		t.Fatalf("onResponse 不应返回错误: %v", err)
	}
	if got := len(pub.Snapshots()); got != 0 {
		t.Fatalf("被过滤的流量不应 publish, snapshots=%d", got)
	}
}

// 通过过滤：snapshot 字段正确填充。
func TestServer_OnResponse_BuildSnapshot(t *testing.T) {
	tmp := t.TempDir()
	srv, pub := newTestServer(t, "127.0.0.1:0", tmp, defaultCfg())

	reqBody := []byte(`{"a":1}`)
	respBody := []byte(`{"ok":true}`)
	resp := makeResponse(t, "POST", "http://target.example.com/api/v1/login?x=1", reqBody, 201, respBody)
	resp.Header.Set("Content-Type", "application/json")
	resp.Header.Add("Set-Cookie", "a=1")
	resp.Header.Add("Set-Cookie", "b=2")

	if err := srv.onResponse(resp, nil); err != nil {
		t.Fatalf("onResponse: %v", err)
	}

	snaps := pub.Snapshots()
	if len(snaps) != 1 {
		t.Fatalf("应有 1 条 snapshot, 实际 %d", len(snaps))
	}
	snap := snaps[0]
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
	// Set-Cookie 必须保留独立多值条目（RFC 6265），不能被合并 join。
	gotCookies := snap.ResponseHeaders["set-cookie"]
	if len(gotCookies) != 2 || gotCookies[0] != "a=1" || gotCookies[1] != "b=2" {
		t.Errorf("Set-Cookie 多值切片错: %#v, 期望 [a=1, b=2]", gotCookies)
	}
	// 单值 header 仍以单元素切片返回（与 http.Header 原 shape 一致）。
	gotCT := snap.ResponseHeaders["content-type"]
	if len(gotCT) != 1 || gotCT[0] != "application/json" {
		t.Errorf("Content-Type lower-case 失败: %#v", gotCT)
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
	srv, pub := newTestServer(t, "127.0.0.1:0", tmp, cfg)

	full := bytes.Repeat([]byte("A"), 20)
	resp := makeResponse(t, "GET", "http://x.example.com/", nil, 200, full)
	if err := srv.onResponse(resp, nil); err != nil {
		t.Fatalf("onResponse: %v", err)
	}

	snaps := pub.Snapshots()
	if len(snaps) != 1 {
		t.Fatalf("snapshots=%d", len(snaps))
	}
	if got := len(snaps[0].ResponseBody); got != 10 {
		t.Fatalf("body 应被截断到 10 字节, 实际 %d", got)
	}
}

// publish 失败不阻断转发：仅 warn，onResponse 仍返回 nil。
func TestServer_OnResponse_PublishError_NotPropagated(t *testing.T) {
	tmp := t.TempDir()
	srv, pub := newTestServer(t, "127.0.0.1:0", tmp, defaultCfg())
	pub.failOn = errors.New("synthetic-publish-fail")

	resp := makeResponse(t, "GET", "http://x.example.com/", nil, 200, []byte("ok"))
	if err := srv.onResponse(resp, nil); err != nil {
		t.Fatalf("publish 失败应被吞，onResponse=%v", err)
	}
}

// 热换过滤链：SwapFilter 后同一条流量的处置随新规则改变（原子替换生效）。
func TestServer_SwapFilter_HotReload(t *testing.T) {
	tmp := t.TempDir()
	srv, pub := newTestServer(t, "127.0.0.1:0", tmp, defaultCfg())

	// 初始：OPTIONS 放行 → 有 snapshot。
	resp1 := makeResponse(t, "OPTIONS", "http://api.example.com/foo", nil, 200, []byte("{}"))
	if err := srv.onResponse(resp1, nil); err != nil {
		t.Fatalf("onResponse#1: %v", err)
	}
	if got := len(pub.Snapshots()); got != 1 {
		t.Fatalf("热换前 OPTIONS 应放行，snapshots=%d", got)
	}

	// 热换：把 OPTIONS 加入方法黑名单 + 收紧 body 上限。
	newCfg := defaultCfg()
	newCfg.ExcludeMethods = []string{"OPTIONS"}
	newCfg.MaxResponseBodySize = 5
	srv.SwapFilter(filter.NewTrafficFilter(newCfg), newCfg.MaxRequestBodySize, newCfg.MaxResponseBodySize)

	// 换后：同样的 OPTIONS 流量被拦 → snapshot 数不增。
	resp2 := makeResponse(t, "OPTIONS", "http://api.example.com/foo", nil, 200, []byte("{}"))
	if err := srv.onResponse(resp2, nil); err != nil {
		t.Fatalf("onResponse#2: %v", err)
	}
	if got := len(pub.Snapshots()); got != 1 {
		t.Fatalf("热换后 OPTIONS 应被拦，snapshots=%d（应仍为 1）", got)
	}

	// 换后：GET 放行，且 body 上限用新值（5 字节截断）。
	resp3 := makeResponse(t, "GET", "http://api.example.com/bar", nil, 200, bytes.Repeat([]byte("A"), 20))
	if err := srv.onResponse(resp3, nil); err != nil {
		t.Fatalf("onResponse#3: %v", err)
	}
	snaps := pub.Snapshots()
	if len(snaps) != 2 {
		t.Fatalf("热换后 GET 应放行，snapshots=%d（应为 2）", len(snaps))
	}
	if got := len(snaps[1].ResponseBody); got != 5 {
		t.Fatalf("热换后 body 上限应为新值 5，实际截断到 %d", got)
	}
}

func TestServer_RunCancellation(t *testing.T) {
	tmp := t.TempDir()
	srv, _ := newTestServer(t, "127.0.0.1:0", tmp, defaultCfg())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
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
