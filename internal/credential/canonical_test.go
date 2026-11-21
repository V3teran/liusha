package credential

import (
	"strings"
	"testing"
)

func TestCanonicalHost(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"111.229.193.40", "111.229.193.40"},
		// 端口保留：不同端口是不同应用（不同源），凭证不跨端口共享
		{"111.229.193.40:34280", "111.229.193.40:34280"},
		{"http://111.229.193.40:34280", "111.229.193.40:34280"},
		{"https://111.229.193.40:34280/login.php", "111.229.193.40:34280"},
		{"http://Example.COM:8080/path?q=1", "example.com:8080"},
		{"  example.com  ", "example.com"},
		{"example.com.", "example.com"},
		{"user@example.com:8080", "example.com:8080"},
		{"[::1]:8080", "::1:8080"},
		{"[2001:db8::1]", "2001:db8::1"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := CanonicalHost(c.in); got != c.want {
			t.Errorf("CanonicalHost(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 主机校验：规范化后的主机必须是合法的域名或 IP，端口可选且在 1–65535——
// 凭证按主机键匹配目标，非法主机写进去的条目永远匹配不上任何目标。
func TestValidateHost(t *testing.T) {
	for _, ok := range []string{
		"example.com", "a.example.com:8443", "192.168.71.36:8099", "localhost", "localhost:5173",
		"internal_host", "xn--fiqs8s.cn", "::1", "fe80::1", "2001:db8::1:8080", "10.0.0.1",
	} {
		if err := ValidateHost(CanonicalHost(ok)); err != nil {
			t.Errorf("%q 应合法: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "bad host!", "a b", "%20", "host:99999", "host:0", "http:x", "-lead.example.com",
		"trail-.example.com", "a..b", "host:", ":8080", strings.Repeat("a", 64) + ".com",
	} {
		if err := ValidateHost(CanonicalHost(bad)); err == nil {
			t.Errorf("%q 应被拒绝", bad)
		}
	}
}
