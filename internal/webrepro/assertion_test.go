package webrepro

import (
	"testing"

	"github.com/V3teran/liusha/internal/httpreplay"
)

// 响应头谓词：header_contains 要求头存在且含子串（子串为空即只要求存在）；
// header_not_contains 要求头不存在，或存在但不含子串（子串为空即要求不存在）。
// 后者表达「缺失安全头」「Cookie 缺 HttpOnly」这类配置弱点。
func TestAssertion_HeaderPredicates(t *testing.T) {
	res := httpreplay.Result{ResponseHeaders: map[string]string{
		"set-cookie": "sid=1; Path=/",
		"server":     "BaseHTTP/0.6 Python/3.12",
	}}
	cases := []struct {
		name string
		a    Assertion
		want bool
	}{
		{"头存在且含子串", Assertion{HeaderContains: map[string]string{"Server": "Python"}}, true},
		{"头存在但不含子串", Assertion{HeaderContains: map[string]string{"Server": "nginx"}}, false},
		{"只要求头存在", Assertion{HeaderContains: map[string]string{"Set-Cookie": ""}}, true},
		{"要求存在的头缺失", Assertion{HeaderContains: map[string]string{"Content-Security-Policy": ""}}, false},
		{"要求缺失的头确实缺失", Assertion{HeaderNotContains: map[string]string{"Content-Security-Policy": ""}}, true},
		{"要求缺失的头却存在", Assertion{HeaderNotContains: map[string]string{"Server": ""}}, false},
		{"头存在但不含子串（缺 HttpOnly）", Assertion{HeaderNotContains: map[string]string{"Set-Cookie": "HttpOnly"}}, true},
		{"头存在且含不应含的子串", Assertion{HeaderNotContains: map[string]string{"Set-Cookie": "Path"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.a.empty() {
				t.Fatal("带响应头谓词的断言不是空断言")
			}
			if got, reasons := c.a.eval(res, 0); got != c.want {
				t.Fatalf("eval = %v，期望 %v（%v）", got, c.want, reasons)
			}
		})
	}
}
