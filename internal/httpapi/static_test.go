package httpapi

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

// testSite 是一份最小的前端构建产物：入口页、带哈希的大脚本（可压缩）、小图标。
func testSite() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>liusha</title>")},
		"assets/index-abc.js":  {Data: []byte(strings.Repeat("console.info('liusha');\n", 200))},
		"assets/style-def.css": {Data: []byte("body{}")},
		"favicon.svg":          {Data: []byte("<svg/>")},
	}
}

func get(t *testing.T, url string, header map[string]string, cookies ...*http.Cookie) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	// 关闭 Transport 的自动解压，断言服务端真实返回的编码。
	resp, err := (&http.Transport{DisableCompression: true}).RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

// 入口页无需登录（登录页本身就在其中），每次协商；history 路由的任意前端路径回落入口页。
func TestStatic_IndexAndHistoryFallback(t *testing.T) {
	srv := newTestServer(t, Deps{WebUI: testSite()})
	defer srv.Close()

	for _, p := range []string{"/", "/index.html", "/tasks/0b6c1f9e-1f7c-4c55-9d35-1e6a8a4b9c10", "/tools/nuclei"} {
		resp, body := get(t, srv.URL+p, nil)
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "<title>liusha</title>") {
			t.Fatalf("%s: status=%d body=%q", p, resp.StatusCode, body)
		}
		if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("%s: 入口页须每次协商，Cache-Control=%q", p, got)
		}
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
			t.Fatalf("%s: Content-Type=%q", p, resp.Header.Get("Content-Type"))
		}
	}
}

// 带哈希的构建产物长缓存；缺失的构建产物是 404，不回落入口页（否则浏览器拿 HTML 当脚本）。
func TestStatic_HashedAssets(t *testing.T) {
	srv := newTestServer(t, Deps{WebUI: testSite()})
	defer srv.Close()

	resp, _ := get(t, srv.URL+"/assets/style-def.css", nil)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("status=%d Cache-Control=%q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	if resp, _ := get(t, srv.URL+"/assets/gone-123.js", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("缺失的构建产物应 404，got %d", resp.StatusCode)
	}
	if resp, _ := get(t, srv.URL+"/favicon.svg", nil); resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("非哈希文件须每次协商，got %q", resp.Header.Get("Cache-Control"))
	}
}

// 可压缩且足够大的文件按 Accept-Encoding 返回 gzip，两种表示的 ETag 不同；小文件不压缩。
func TestStatic_Gzip(t *testing.T) {
	site := testSite()
	srv := newTestServer(t, Deps{WebUI: site})
	defer srv.Close()

	plain, plainBody := get(t, srv.URL+"/assets/index-abc.js", nil)
	if plain.Header.Get("Content-Encoding") != "" || !bytes.Equal(plainBody, site["assets/index-abc.js"].Data) {
		t.Fatalf("未声明 gzip 时应返回原文, Content-Encoding=%q", plain.Header.Get("Content-Encoding"))
	}
	zipped, zippedBody := get(t, srv.URL+"/assets/index-abc.js", map[string]string{"Accept-Encoding": "br, gzip"})
	if zipped.Header.Get("Content-Encoding") != "gzip" || zipped.Header.Get("Vary") != "Accept-Encoding" {
		t.Fatalf("应返回 gzip 并声明 Vary, headers=%v", zipped.Header)
	}
	zr, err := gzip.NewReader(bytes.NewReader(zippedBody))
	if err != nil {
		t.Fatal(err)
	}
	unzipped, _ := io.ReadAll(zr)
	if !bytes.Equal(unzipped, site["assets/index-abc.js"].Data) {
		t.Fatal("gzip 解压后应与原文一致")
	}
	if plain.Header.Get("ETag") == zipped.Header.Get("ETag") {
		t.Fatal("原文与 gzip 两种表示的 ETag 必须不同")
	}
	if r, _ := get(t, srv.URL+"/assets/index-abc.js", map[string]string{"Accept-Encoding": "gzip;q=0"}); r.Header.Get("Content-Encoding") != "" {
		t.Fatal("q=0 表示拒绝 gzip")
	}
	if r, _ := get(t, srv.URL+"/favicon.svg", map[string]string{"Accept-Encoding": "gzip"}); r.Header.Get("Content-Encoding") != "" {
		t.Fatal("小于阈值的文件不压缩")
	}
}

// 携带匹配的 If-None-Match 返回 304，无响应体。
func TestStatic_ConditionalGet(t *testing.T) {
	srv := newTestServer(t, Deps{WebUI: testSite()})
	defer srv.Close()

	first, _ := get(t, srv.URL+"/", nil)
	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("应返回 ETag")
	}
	resp, body := get(t, srv.URL+"/", map[string]string{"If-None-Match": etag})
	if resp.StatusCode != http.StatusNotModified || len(body) != 0 {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
}

// /api 不被前端遮挡：未登录 401；已登录访问未注册接口得 JSON 404 而非入口页；已注册路由照常。
func TestStatic_APINamespaceNotShadowed(t *testing.T) {
	srv := newTestServer(t, Deps{WebUI: testSite()})
	defer srv.Close()

	if resp, _ := get(t, srv.URL+"/api/nope", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录访问 /api 应 401，got %d", resp.StatusCode)
	}
	resp, body := get(t, srv.URL+"/api/nope", nil, testSessionCookie())
	var e struct {
		Error string `json:"error"`
	}
	if resp.StatusCode != http.StatusNotFound || json.Unmarshal(body, &e) != nil || e.Error == "" {
		t.Fatalf("未注册接口应 JSON 404, status=%d body=%q", resp.StatusCode, body)
	}
	if resp, body := get(t, srv.URL+"/healthz", nil); resp.StatusCode != http.StatusOK || strings.Contains(string(body), "<title>") {
		t.Fatalf("已注册路由优先, status=%d body=%q", resp.StatusCode, body)
	}
}

// 不提供前端时：非 /api 路径 404，/api 下仍是 JSON 404。
func TestStatic_NoWebUI(t *testing.T) {
	srv := newTestServer(t, Deps{})
	defer srv.Close()

	if resp, _ := get(t, srv.URL+"/", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got %d", resp.StatusCode)
	}
	resp, body := get(t, srv.URL+"/api/nope", nil, testSessionCookie())
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(string(body), "error") {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
}
