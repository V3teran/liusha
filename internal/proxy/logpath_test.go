package proxy

import "testing"

func TestLogPath(t *testing.T) {
	cases := map[string]string{
		"/api/user?access_token=secret": "/api/user",
		"/reset#token=abc":              "/reset",
		"/plain":                        "/plain",
		"":                              "",
		"http://h/x?k=v":                "http://h/x",
	}
	for in, want := range cases {
		if got := LogPath(in); got != want {
			t.Errorf("LogPath(%q) = %q, want %q", in, got, want)
		}
	}
}
