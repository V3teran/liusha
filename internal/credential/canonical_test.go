package credential

import "testing"

func TestCanonicalHost(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"111.229.193.40", "111.229.193.40"},
		{"111.229.193.40:34280", "111.229.193.40"},
		{"http://111.229.193.40:34280", "111.229.193.40"},
		{"https://111.229.193.40:34280/login.php", "111.229.193.40"},
		{"http://Example.COM:8080/path?q=1", "example.com"},
		{"  example.com  ", "example.com"},
		{"example.com.", "example.com"},
		{"user@example.com:8080", "example.com"},
		{"[::1]:8080", "::1"},
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
