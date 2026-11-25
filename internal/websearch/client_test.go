package websearch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 请求：GET 接口地址并追加查询词 q（地址里自带的固定参数保留），带 Accept: application/json。
func TestClient_SearchSendsQuery(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	if _, err := New(srv.URL+"/search?language=zh-CN", "", "").Search(context.Background(), "apache 2.4.49 默认页面", 10); err != nil {
		t.Fatal(err)
	}
	if got.Method != http.MethodGet || got.URL.Path != "/search" {
		t.Fatalf("请求 = %s %s", got.Method, got.URL)
	}
	if q := got.URL.Query(); q.Get("language") != "zh-CN" || q.Get("q") != "apache 2.4.49 默认页面" {
		t.Fatalf("查询参数 = %v", q)
	}
	if got.Header.Get("Accept") != "application/json" {
		t.Fatalf("Accept = %q", got.Header.Get("Accept"))
	}
}

// 响应形状自动识别：主流搜索接口有两种事实上的惯例——结果平铺在 results 里、摘要叫 content，
// 或嵌在 web.results 里、摘要叫 description。两种都认，程序不必知道对面是哪一家。
func TestClient_RecognizesResponseShapes(t *testing.T) {
	const flat = `{"query":"x","results":[
		{"title":"标题 一","url":"https://a.test/1","content":"  第一条的\n摘要  "},
		{"title":"标题二","url":"https://b.test/2","content":"第二条"}
	],"response_time":1.2}`
	const nested = `{"web":{"results":[
		{"title":"标题 一","url":"https://a.test/1","description":"  第一条的\n摘要  "},
		{"title":"标题二","url":"https://b.test/2","description":"第二条"}
	]},"query":{"original":"x"}}`
	want := []Result{
		{Title: "标题 一", URL: "https://a.test/1", Snippet: "第一条的 摘要"},
		{Title: "标题二", URL: "https://b.test/2", Snippet: "第二条"},
	}
	for name, body := range map[string]string{"平铺 results + content": flat, "嵌套 web.results + description": nested} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer srv.Close()
			res, err := New(srv.URL, "", "").Search(context.Background(), "q", 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(res) != len(want) {
				t.Fatalf("结果 = %+v，期望 %d 条", res, len(want))
			}
			for i := range want {
				if res[i] != want[i] {
					t.Errorf("结果 %d = %+v，期望 %+v（摘要里的空白折成一个空格）", i, res[i], want[i])
				}
			}
		})
	}
}

// 不成条目的结果去掉：没有地址、地址不是带主机的 http(s)、同一地址重复。
func TestClient_DropsUnusableResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[
			{"url":"https://c.test/没有标题","content":"没有标题也保留"},
			{"title":"没有地址","content":"没有地址的结果不成条目"},
			{"title":"相对地址","url":"/relative","content":"相对路径点不开"},
			{"title":"重复地址","url":"https://c.test/没有标题","content":"同一地址只留第一条"}
		]}`))
	}))
	defer srv.Close()
	res, err := New(srv.URL, "", "").Search(context.Background(), "q", 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []Result{{URL: "https://c.test/没有标题", Snippet: "没有标题也保留"}}
	if len(res) != len(want) || res[0] != want[0] {
		t.Fatalf("结果 = %+v，期望 %+v", res, want)
	}
}

func TestClient_Limit(t *testing.T) {
	var results []string
	for i := 1; i <= MaxLimit+5; i++ {
		results = append(results, fmt.Sprintf(`{"url":"https://a.test/%d"}`, i))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[` + strings.Join(results, ",") + `]}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "", "")
	if res, _ := c.Search(context.Background(), "q", 2); len(res) != 2 || res[1].URL != "https://a.test/2" {
		t.Errorf("limit=2 应返回前两条，得 %+v", res)
	}
	// limit 没给（0 或负数）用默认条数；给了也不超过上限
	for limit, want := range map[int]int{0: DefaultLimit, -1: DefaultLimit, MaxLimit + 100: MaxLimit} {
		if res, _ := c.Search(context.Background(), "q", limit); len(res) != want {
			t.Errorf("limit=%d 应返回 %d 条，得 %d 条", limit, want, len(res))
		}
	}
}

// 摘要过长时截断：摘要只用来判断值不值得打开全文。
func TestClient_TruncatesLongSnippet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"url":"https://a.test/1","content":"` + strings.Repeat("长", MaxSnippetRunes*2) + `"}]}`))
	}))
	defer srv.Close()
	res, err := New(srv.URL, "", "").Search(context.Background(), "q", 5)
	if err != nil || len(res) != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if n := len([]rune(res[0].Snippet)); n > MaxSnippetRunes+len("…") {
		t.Errorf("摘要应截断到 %d 个字符加省略号，得 %d", MaxSnippetRunes, n)
	}
}

// 鉴权：不配密钥就不带鉴权头。缺省头名是 Authorization（标准做法，值是 Bearer <密钥>）；
// 有的搜索服务用自己的头名放裸密钥——头名由部署方配，程序不认服务。
func TestClient_AuthHeader(t *testing.T) {
	cases := []struct {
		name, header, key, wantHeader, wantValue string
	}{
		{"缺省：Authorization 头，Bearer 形式", "", "secret-key", "Authorization", "Bearer secret-key"},
		{"自定义头名：裸密钥", "X-Subscription-Token", "secret-key", "X-Subscription-Token", "secret-key"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got http.Header
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Clone()
				_, _ = w.Write([]byte(`{"results":[]}`))
			}))
			defer srv.Close()
			if _, err := New(srv.URL, c.header, c.key).Search(context.Background(), "q", 5); err != nil {
				t.Fatal(err)
			}
			if got.Get(c.wantHeader) != c.wantValue {
				t.Errorf("%s = %q，期望 %q", c.wantHeader, got.Get(c.wantHeader), c.wantValue)
			}
			// 只带配了的那一个鉴权头：自定义头名时不再带 Authorization，密钥只走一条路
			for other := range got {
				if other != c.wantHeader && (other == "Authorization" || strings.HasPrefix(other, "X-")) && got.Get(other) == c.key {
					t.Errorf("密钥不应出现在别的头里：%s", other)
				}
			}
		})
	}
	t.Run("不配密钥就不带鉴权头", func(t *testing.T) {
		var got http.Header
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Clone()
			_, _ = w.Write([]byte(`{"results":[]}`))
		}))
		defer srv.Close()
		if _, err := New(srv.URL, "", "").Search(context.Background(), "q", 5); err != nil {
			t.Fatal(err)
		}
		if got.Get("Authorization") != "" {
			t.Errorf("没配密钥就不该带 Authorization，得 %q", got.Get("Authorization"))
		}
	})
}

// 密钥不随重定向带离原主机。标准库缺省只会摘掉 Authorization，配了别的头名（X-…）时缺省会
// 照带——所以不能只靠标准库，客户端自己管：带密钥的请求不得跳去别处（中止），不带密钥的
// 后续跳转照常。标准库按主机名判定是否同一处、不看端口，所以落点用另一个主机名。
func TestClient_APIKeyNotLeakedOnCrossHostRedirect(t *testing.T) {
	for _, header := range []string{"", "X-Subscription-Token"} {
		t.Run("header="+header, func(t *testing.T) {
			var foreign, sameHost http.Header
			reached := false
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				foreign = r.Header.Clone()
				reached = true
				_, _ = w.Write([]byte(`{"results":[]}`))
			}))
			defer target.Close()
			foreignURL := strings.Replace(target.URL, "127.0.0.1", "localhost", 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/search":
					sameHost = r.Header.Clone()
					http.Redirect(w, r, "/moved", http.StatusFound)
				case "/moved":
					http.Redirect(w, r, foreignURL+"/landing", http.StatusFound)
				}
			}))
			defer srv.Close()

			_, _ = New(srv.URL+"/search", header, "secret-key").Search(context.Background(), "q", 5)
			if sameHost == nil || !hasSecret(sameHost, "secret-key") {
				t.Fatal("前置：发给搜索接口的请求应带密钥")
			}
			// 安全属性：落点拿不到密钥。至于是「没跳过去」还是「跳过去但摘了头」由实现决定
			if reached && foreign != nil && hasSecret(foreign, "secret-key") {
				t.Errorf("重定向到别的主机时密钥被带了出去，落点收到的头 = %v", foreign)
			}
		})
	}
}

// 同一处的重定向照常走完、密钥照带（如 http → https 的升级、路径的规范化跳转）。
func TestClient_SameHostRedirectKeepsSecret(t *testing.T) {
	for _, header := range []string{"", "X-Subscription-Token"} {
		t.Run("header="+header, func(t *testing.T) {
			name := header
			if name == "" {
				name = "Authorization"
			}
			var landed http.Header
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/search" {
					http.Redirect(w, r, "/v2/search?"+r.URL.RawQuery, http.StatusFound)
					return
				}
				landed = r.Header.Clone()
				_, _ = w.Write([]byte(`{"results":[{"title":"t","url":"https://a.test/1","content":"c"}]}`))
			}))
			defer srv.Close()
			res, err := New(srv.URL+"/search", header, "secret-key").Search(context.Background(), "q", 5)
			if err != nil {
				t.Fatalf("同一处的重定向应照常走完: %v", err)
			}
			if landed == nil || !hasSecret(landed, "secret-key") {
				t.Error("同一处的重定向后请求应照带密钥")
			}
			if len(res) != 1 || res[0].URL != "https://a.test/1" {
				t.Errorf("应拿到落点的结果，得 %+v", res)
			}
		})
	}
}

func hasSecret(h http.Header, secret string) bool {
	for _, values := range h {
		for _, v := range values {
			if strings.Contains(v, secret) {
				return true
			}
		}
	}
	return false
}

// 非 2xx、不是 JSON、两种形状都不是、响应超限：都报出接口侧的原因，不带上地址（地址的参数里可能写着密钥）。
func TestClient_Errors(t *testing.T) {
	t.Run("非 2xx 状态原样报出，不带响应正文", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("a whole page of html"))
		}))
		defer srv.Close()
		_, err := New(srv.URL, "", "").Search(context.Background(), "q", 5)
		var status *StatusError
		if !errors.As(err, &status) || status.Code != http.StatusForbidden {
			t.Fatalf("err = %v，期望 StatusError(403)", err)
		}
		if strings.Contains(err.Error(), "a whole page") {
			t.Errorf("错误里不应带响应正文（可能是一整页 HTML），得 %q", err)
		}
	})
	for name, body := range map[string]string{
		"响应不是 JSON":   `<html>不是 JSON</html>`,
		"两种形状都不是":     `{"data":{"items":[]}}`,
		"响应超限，读到上限即止": `{"results":[{"url":"https://a.test/` + strings.Repeat("x", maxResponseBytes) + `"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
			defer srv.Close()
			if _, err := New(srv.URL, "", "").Search(context.Background(), "q", 5); err == nil {
				t.Fatal("应报错")
			}
		})
	}
	t.Run("连不上：错误里不带接口地址", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		endpoint := srv.URL + "/search?api_key=in-url"
		srv.Close() // 端口已不可达
		_, err := New(endpoint, "", "").Search(context.Background(), "q", 5)
		if err == nil {
			t.Fatal("应报错")
		}
		if strings.Contains(err.Error(), "in-url") || strings.Contains(err.Error(), srv.URL) {
			t.Errorf("错误里不应出现接口地址，得 %q", err)
		}
	})
	t.Run("取消与超时原样交回（调用方据此区分中断与故障）", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
		defer srv.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := New(srv.URL, "", "").Search(ctx, "q", 5)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v，期望 context.Canceled", err)
		}
	})
	t.Run("鉴权头名不合法：请求发不出去时如实报出", func(t *testing.T) {
		_, err := New("http://search.internal/search", "bad header name", "k").Search(context.Background(), "q", 5)
		if err == nil {
			t.Fatal("应报错")
		}
	})
}
