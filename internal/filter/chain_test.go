package filter

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/config"
)

// mkReq 构造测试请求；method/host/path 任一可空。
func mkReq(method, host, path string, headers map[string]string, contentLen int64) *http.Request {
	u, _ := url.Parse("http://" + host + path)
	req := &http.Request{
		Method:        method,
		URL:           u,
		Host:          host,
		Header:        http.Header{},
		ContentLength: contentLen,
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func mkResp(status int, contentType string, contentLen int64) *http.Response {
	h := http.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode:    status,
		Header:        h,
		ContentLength: contentLen,
	}
}

// ---------- 单 filter 表驱动 ----------

func TestMethodFilter(t *testing.T) {
	f := NewMethodFilter([]string{"OPTIONS", "HEAD"})
	cases := []struct {
		name   string
		method string
		want   bool
	}{
		{"GET 通过", "GET", true},
		{"POST 通过", "POST", true},
		{"OPTIONS 拦截", "OPTIONS", false},
		{"head 大小写不敏感拦截", "head", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := f.ShouldProcess(mkReq(tc.method, "x.com", "/", nil, 0), nil)
			if ok != tc.want {
				t.Fatalf("got %v want %v reason=%q", ok, tc.want, reason)
			}
			if !ok && reason == "" {
				t.Fatalf("拒绝必须带 reason")
			}
		})
	}
}

func TestProtocolFilter(t *testing.T) {
	f := NewProtocolFilter([]string{"websocket"})
	t.Run("Upgrade websocket 拦截", func(t *testing.T) {
		req := mkReq("GET", "x.com", "/", map[string]string{"Upgrade": "websocket"}, 0)
		ok, reason := f.ShouldProcess(req, nil)
		if ok || !strings.Contains(reason, "websocket") {
			t.Fatalf("应拦截 websocket; got ok=%v reason=%q", ok, reason)
		}
	})
	t.Run("101 响应拦截", func(t *testing.T) {
		req := mkReq("GET", "x.com", "/", nil, 0)
		ok, _ := f.ShouldProcess(req, mkResp(101, "", 0))
		if ok {
			t.Fatalf("101 应被拦")
		}
	})
	t.Run("普通 GET 通过", func(t *testing.T) {
		ok, _ := f.ShouldProcess(mkReq("GET", "x.com", "/", nil, 0), mkResp(200, "text/html", 100))
		if !ok {
			t.Fatalf("普通 GET 应通过")
		}
	})
}

func TestHostFilter(t *testing.T) {
	t.Run("白名单+精确匹配", func(t *testing.T) {
		f := NewHostFilter([]string{"vulnapp"}, nil)
		ok, _ := f.ShouldProcess(mkReq("GET", "vulnapp", "/", nil, 0), nil)
		if !ok {
			t.Fatalf("白名单内应放行")
		}
		ok, reason := f.ShouldProcess(mkReq("GET", "evil.com", "/", nil, 0), nil)
		if ok || !strings.Contains(reason, "whitelist") {
			t.Fatalf("白名单外应拦; reason=%q", reason)
		}
	})
	t.Run("通配符 *.example.com", func(t *testing.T) {
		f := NewHostFilter([]string{"*.example.com"}, nil)
		ok, _ := f.ShouldProcess(mkReq("GET", "api.example.com", "/", nil, 0), nil)
		if !ok {
			t.Fatalf("子域应放行")
		}
		ok, _ = f.ShouldProcess(mkReq("GET", "example.com", "/", nil, 0), nil)
		if !ok {
			t.Fatalf("根域应放行")
		}
		ok, _ = f.ShouldProcess(mkReq("GET", "evil.com", "/", nil, 0), nil)
		if ok {
			t.Fatalf("非子域应拦")
		}
	})
	t.Run("黑名单优先", func(t *testing.T) {
		f := NewHostFilter([]string{"x.com"}, []string{"x.com"})
		ok, reason := f.ShouldProcess(mkReq("GET", "x.com", "/", nil, 0), nil)
		if ok || !strings.Contains(reason, "excluded host") {
			t.Fatalf("黑名单应优先于白名单")
		}
	})
	t.Run("白名单空全放行", func(t *testing.T) {
		f := NewHostFilter(nil, nil)
		ok, _ := f.ShouldProcess(mkReq("GET", "any.com", "/", nil, 0), nil)
		if !ok {
			t.Fatalf("无白名单时应全放行")
		}
	})
	t.Run("Host 含端口", func(t *testing.T) {
		f := NewHostFilter([]string{"vulnapp"}, nil)
		ok, _ := f.ShouldProcess(mkReq("GET", "vulnapp:8080", "/", nil, 0), nil)
		if !ok {
			t.Fatalf("端口应被剥离")
		}
	})
}

func TestSuffixFilter(t *testing.T) {
	f := NewSuffixFilter([]string{".css", ".png"})
	cases := []struct {
		path string
		want bool
	}{
		{"/api/users", true},
		{"/static/app.css", false},
		{"/img/logo.PNG", false}, // 大小写不敏感
		{"/foo.html", true},
	}
	for _, tc := range cases {
		ok, _ := f.ShouldProcess(mkReq("GET", "x.com", tc.path, nil, 0), nil)
		if ok != tc.want {
			t.Errorf("path=%s got %v want %v", tc.path, ok, tc.want)
		}
	}
}

func TestContentTypeFilter(t *testing.T) {
	f := NewContentTypeFilter([]string{"image/*", "application/octet-stream"})
	cases := []struct {
		ct   string
		want bool
	}{
		{"text/html; charset=utf-8", true},
		{"application/json", true},
		{"image/png", false},
		{"image/jpeg; charset=utf-8", false},
		{"application/octet-stream", false},
	}
	for _, tc := range cases {
		ok, _ := f.ShouldProcess(nil, mkResp(200, tc.ct, 0))
		if ok != tc.want {
			t.Errorf("ct=%s got %v want %v", tc.ct, ok, tc.want)
		}
	}
	if ok, _ := f.ShouldProcess(nil, nil); !ok {
		t.Errorf("nil resp 应放行")
	}
}

func TestStatusCodeFilter(t *testing.T) {
	t.Run("空黑名单全放行", func(t *testing.T) {
		f := NewStatusCodeFilter(nil)
		ok, _ := f.ShouldProcess(nil, mkResp(500, "", 0))
		if !ok {
			t.Fatalf("空黑名单应全放行")
		}
	})
	t.Run("黑名单含 404/500", func(t *testing.T) {
		f := NewStatusCodeFilter([]int{404, 500})
		if ok, _ := f.ShouldProcess(nil, mkResp(200, "", 0)); !ok {
			t.Errorf("200 不在黑名单应放行")
		}
		if ok, reason := f.ShouldProcess(nil, mkResp(404, "", 0)); ok || !strings.Contains(reason, "404") {
			t.Errorf("404 应被拦且 reason 含状态码; reason=%q", reason)
		}
		if ok, reason := f.ShouldProcess(nil, mkResp(500, "", 0)); ok || !strings.Contains(reason, "500") {
			t.Errorf("500 应被拦; reason=%q", reason)
		}
	})
	t.Run("nil resp 放行", func(t *testing.T) {
		f := NewStatusCodeFilter([]int{500})
		ok, _ := f.ShouldProcess(nil, nil)
		if !ok {
			t.Fatalf("nil resp 应放行")
		}
	})
}

func TestSizeFilter(t *testing.T) {
	f := NewSizeFilter(100, 200)
	cases := []struct {
		name    string
		reqLen  int64
		respLen int64
		want    bool
	}{
		{"都在限内", 50, 100, true},
		{"req 超限", 200, 50, false},
		{"resp 超限", 50, 300, false},
		{"零=不计入", 0, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := mkReq("POST", "x.com", "/", nil, tc.reqLen)
			resp := mkResp(200, "", tc.respLen)
			ok, _ := f.ShouldProcess(req, resp)
			if ok != tc.want {
				t.Fatalf("got %v want %v", ok, tc.want)
			}
		})
	}
	t.Run("max=0 不限", func(t *testing.T) {
		f0 := NewSizeFilter(0, 0)
		ok, _ := f0.ShouldProcess(mkReq("POST", "x.com", "/", nil, 999999), mkResp(200, "", 999999))
		if !ok {
			t.Fatalf("max=0 应不限")
		}
	})
}

// ---------- Chain 整体 ----------

func TestChain_FirstRejectShortCircuit(t *testing.T) {
	c := NewChain(
		NewMethodFilter([]string{"OPTIONS"}),
		NewHostFilter([]string{"vulnapp"}, nil),
	)
	ok, reason := c.ShouldProcess(mkReq("OPTIONS", "evil.com", "/", nil, 0), nil)
	if ok || !strings.Contains(reason, "method") {
		t.Fatalf("Method 应优先拒绝；reason=%q", reason)
	}
}

func TestChain_AllPass(t *testing.T) {
	c := NewChain(
		NewMethodFilter([]string{"OPTIONS"}),
		NewHostFilter([]string{"vulnapp"}, nil),
		NewSuffixFilter([]string{".css"}),
	)
	ok, _ := c.ShouldProcess(mkReq("GET", "vulnapp", "/api/x", nil, 0), mkResp(200, "application/json", 100))
	if !ok {
		t.Fatalf("正常请求应通过")
	}
}

func TestChain_AddBuilder(t *testing.T) {
	c := NewChain().Add(NewMethodFilter([]string{"HEAD"}))
	ok, _ := c.ShouldProcess(mkReq("HEAD", "x.com", "/", nil, 0), nil)
	if ok {
		t.Fatalf("HEAD 应被拦")
	}
}

// ---------- TrafficFilter 门面 ----------

func TestTrafficFilter_Construction(t *testing.T) {
	cfg := config.ProxyConfig{
		AllowHosts:              []string{"vulnapp"},
		ExcludeMethods:          []string{"OPTIONS", "HEAD"},
		ExcludeUpgradeProtocols: []string{"websocket"},
		ExcludeSuffixes:         []string{".css", ".js"},
		ExcludeContentTypes:     []string{"image/*"},
		ExcludeStatusCodes:      []int{404, 500},
		MaxRequestBodySize:      1024,
		MaxResponseBodySize:     2048,
	}
	tf := NewTrafficFilter(cfg)

	cases := []struct {
		name string
		req  *http.Request
		resp *http.Response
		want bool
	}{
		{"happy", mkReq("GET", "vulnapp", "/api/users", nil, 0), mkResp(200, "application/json", 100), true},
		{"OPTIONS 被 method 拦", mkReq("OPTIONS", "vulnapp", "/api/users", nil, 0), mkResp(200, "application/json", 100), false},
		{"非白名单 host 拦", mkReq("GET", "evil.com", "/", nil, 0), mkResp(200, "application/json", 0), false},
		{".css 后缀拦", mkReq("GET", "vulnapp", "/static/app.css", nil, 0), mkResp(200, "text/css", 0), false},
		{"image 拦", mkReq("GET", "vulnapp", "/img/logo", nil, 0), mkResp(200, "image/png", 100), false},
		{"状态码 404 在黑名单被拦", mkReq("GET", "vulnapp", "/", nil, 0), mkResp(404, "application/json", 0), false},
		{"状态码 200 不在黑名单放行", mkReq("GET", "vulnapp", "/api/x", nil, 0), mkResp(200, "application/json", 0), true},
		{"req body 过大", mkReq("POST", "vulnapp", "/upload", nil, 5000), mkResp(200, "application/json", 0), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := tf.ShouldProcess(tc.req, tc.resp)
			if ok != tc.want {
				t.Fatalf("got %v want %v reason=%q", ok, tc.want, reason)
			}
			if !ok && reason == "" {
				t.Fatalf("拒绝必须带 reason")
			}
		})
	}
}

func TestTrafficFilter_EmptyConfig_PassesByDefault(t *testing.T) {
	tf := NewTrafficFilter(config.ProxyConfig{})
	ok, _ := tf.ShouldProcess(
		mkReq("GET", "any.com", "/", nil, 0),
		mkResp(200, "application/json", 0),
	)
	if !ok {
		t.Fatalf("空配置应放行普通流量")
	}
	ok, _ = tf.ShouldProcess(
		mkReq("GET", "any.com", "/", map[string]string{"Upgrade": "websocket"}, 0),
		nil,
	)
	if ok {
		t.Fatalf("websocket 应始终被拦")
	}
}
