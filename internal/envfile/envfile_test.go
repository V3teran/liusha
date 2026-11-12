package envfile

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var tricky = []Var{
	{Key: "LIUSHA_POSTGRES_DSN", Value: `postgres://u:p%24ss@h:5432/db?sslmode=disable&x=1 #notcomment`},
	{Key: "PLAIN", Value: "abc123"},
	{Key: "EMPTY", Value: ""},
	{Key: "TOKEN", Value: "Zm9v-YmFy_YmF6"},
}

// 三方解析可能不一致的字符一律拒绝写入，且不创建文件。
func TestCreate_RejectsAmbiguousValues(t *testing.T) {
	for _, v := range []string{`a"b`, `a'b`, `a\b`, "a$b", "a`b", "a\nb", "a\rb"} {
		path := filepath.Join(t.TempDir(), "x.env")
		if err := Create(path, []Section{{Vars: []Var{{Key: "K", Value: v}}}}); err == nil {
			t.Errorf("%q 应被拒绝", v)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%q 被拒绝时不应创建文件", v)
		}
	}
}

// 写出的文件能被本包读回同样的值，且权限为 0600；已存在时拒绝覆盖。
func TestCreateRead_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "liusha.env")
	if err := Create(path, []Section{{Comments: []string{"说明"}, Vars: tricky}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("权限 = %v，期望 0600", info.Mode().Perm())
	}
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(tricky) {
		t.Fatalf("got %v", got)
	}
	for i := range tricky {
		if got[i] != tricky[i] {
			t.Errorf("第 %d 项 = %+v，期望 %+v", i, got[i], tricky[i])
		}
	}
	if err := Create(path, nil); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("已存在应拒绝覆盖, got %v", err)
	}
}

// 生成的文件被 sh source 后值不变（运维常用 set -a; . liusha.env）。
func TestCreate_ShellCompatible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "liusha.env")
	if err := Create(path, []Section{{Vars: tricky}}); err != nil {
		t.Fatal(err)
	}
	for _, v := range tricky {
		out, err := exec.Command("sh", "-c", `. "$1" && printf %s "$`+v.Key+`"`, "sh", path).Output() // #nosec G204 // 测试内固定变量名
		if err != nil {
			t.Fatal(err)
		}
		if string(out) != v.Value {
			t.Errorf("%s: sh 读到 %q，期望 %q", v.Key, out, v.Value)
		}
	}
}

func TestRead_Syntax(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.env")
	body := "# 注释\n\nexport A=1\nB = ' x '\nC=\"y\\\"z\"\nD= plain value \n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []Var{{"A", "1"}, {"B", " x "}, {"C", `y"z`}, {"D", "plain value"}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %+v want %+v", got[i], want[i])
		}
	}

	for _, bad := range []string{"NOEQUALS\n", "1BAD=x\n", "Q=\"unterminated\n"} {
		if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Read(path); err == nil {
			t.Errorf("%q 应报错", strings.TrimSpace(bad))
		}
	}
}

// 显式设置的环境变量优先于文件。
func TestLoad_DoesNotOverrideEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.env")
	if err := os.WriteFile(path, []byte("LIUSHA_TEST_A=file\nLIUSHA_TEST_B=file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIUSHA_TEST_A", "env")
	t.Setenv("LIUSHA_TEST_B", "")
	_ = os.Unsetenv("LIUSHA_TEST_B")
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("LIUSHA_TEST_A") != "env" || os.Getenv("LIUSHA_TEST_B") != "file" {
		t.Fatalf("A=%q B=%q", os.Getenv("LIUSHA_TEST_A"), os.Getenv("LIUSHA_TEST_B"))
	}
}
