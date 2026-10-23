package executor

import (
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/httpreplay"
)

func TestAuthBounced(t *testing.T) {
	cases := []struct {
		name string
		res  httpreplay.Result
		want bool
	}{
		{"401 未认证", httpreplay.Result{StatusCode: 401, URL: "http://t/app"}, true},
		{"302 跳登录页", httpreplay.Result{StatusCode: 302, URL: "http://t/app", ResponseHeaders: map[string]string{"location": "/login.php"}}, true},
		{"302 跳非认证页", httpreplay.Result{StatusCode: 302, URL: "http://t/app", ResponseHeaders: map[string]string{"location": "/home"}}, false},
		{"200 登录表单", httpreplay.Result{StatusCode: 200, URL: "http://t/app", ResponseBody: []byte(`<form><input type="password" name="pass"></form>`)}, true},
		{"配方即登录页不算弹跳", httpreplay.Result{StatusCode: 200, URL: "http://t/login.php", ResponseBody: []byte(`<input type="password">`)}, false},
		{"403 是越权信号非弹跳", httpreplay.Result{StatusCode: 403, URL: "http://t/admin"}, false},
		{"200 无登录形态", httpreplay.Result{StatusCode: 200, URL: "http://t/app", ResponseBody: []byte(`<html>ok</html>`)}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := authBounced(c.res); got != c.want {
				t.Errorf("authBounced(%d %s) = %v, want %v", c.res.StatusCode, c.res.URL, got, c.want)
			}
		})
	}
}

func TestReplaceSessionHeaders(t *testing.T) {
	recipe := json.RawMessage(`{
		"request": {
			"method": "POST",
			"url": "http://t/xss",
			"headers": {"Cookie": "PHPSESSID=stale; security=impossible", "Authorization": "Bearer old", "X-Custom": "keep"},
			"body": "a=1"
		},
		"baseline": {
			"method": "GET",
			"url": "http://t/xss",
			"headers": {"Cookie": "PHPSESSID=stale"}
		}
	}`)
	fresh := map[string]string{"cookie": "PHPSESSID=fresh; security=low"}

	out := replaceSessionHeaders(recipe, fresh)

	var got struct {
		Request struct {
			Headers map[string]string `json:"headers"`
		} `json:"request"`
		Baseline struct {
			Headers map[string]string `json:"headers"`
		} `json:"baseline"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal replaced recipe: %v", err)
	}
	if got.Request.Headers["cookie"] != "PHPSESSID=fresh; security=low" {
		t.Errorf("request cookie 未替换: %q", got.Request.Headers["cookie"])
	}
	if v, ok := got.Request.Headers["authorization"]; ok {
		t.Errorf("authorization 应被移除（候选未提供新值），得 %q", v)
	}
	if got.Request.Headers["X-Custom"] != "keep" {
		t.Errorf("非会话头应保留: %q", got.Request.Headers["X-Custom"])
	}
	if got.Baseline.Headers["cookie"] != "PHPSESSID=fresh; security=low" {
		t.Errorf("baseline cookie 未同步替换: %q", got.Baseline.Headers["cookie"])
	}
}

func TestReplaceSessionHeaders_NoSessionKeys(t *testing.T) {
	// recipe 无任何会话头且候选也无会话头 → 原样返回（无变化不加轮次）
	recipe := json.RawMessage(`{"request": {"method": "GET", "url": "http://t/a", "headers": {"Accept": "text/html"}}}`)
	out := replaceSessionHeaders(recipe, map[string]string{"x-api-key": "v"})
	if string(out) != string(recipe) {
		t.Errorf("无会话键时不应改写 recipe")
	}
}
