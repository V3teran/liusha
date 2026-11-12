package server

import (
	"runtime"
	"slices"
	"testing"
)

func TestEnvName(t *testing.T) {
	for kv, want := range map[string]string{
		"PATH=/bin":    "PATH",
		"A=b=c":        "A",
		"=C:=C:\\work": "=C:",
		"EMPTY=":       "EMPTY",
		"NOEQ":         "NOEQ",
	} {
		if got := envName(kv); got != want {
			t.Errorf("envName(%q) = %q，期望 %q", kv, got, want)
		}
	}
}

// Windows 的变量名不区分大小写：Path 就是 PATH，liusha_x 也是 liusha 的变量。
func TestScrubAndPrepend_FollowPlatformNameRules(t *testing.T) {
	env := []string{"Path=C:\\bin", "liusha_postgres_dsn=x", "Main_Api_Key=k", "KEEP=1"}
	got := prependPath(scrubEnv(env, []string{"MAIN_API_KEY"}), "D:\\browser")
	want := []string{"Path=C:\\bin", "liusha_postgres_dsn=x", "Main_Api_Key=k", "KEEP=1", "PATH=D:\\browser"}
	if runtime.GOOS == "windows" {
		want = []string{"Path=D:\\browser;C:\\bin", "KEEP=1"}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}
