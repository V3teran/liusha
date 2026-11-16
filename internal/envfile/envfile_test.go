package envfile

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

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

// 仓库根的 .env.example（三种部署方式共用）能被 -env-file 原样读入；默认生效（不注释）的只有各部署
// 方式都要填的密钥——按部署方式才用的项默认注释，二进制复制后不会带上容器专用的值。
func TestRead_EnvExample(t *testing.T) {
	vars, err := Read(filepath.Join("..", "..", ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(vars))
	for _, v := range vars {
		got = append(got, v.Key)
	}
	want := []string{"LIUSHA_SANDBOX_TOKEN", "LIUSHA_LLM_ENCRYPTION_KEY", "LIUSHA_ADMIN_INITIAL_PASSWORD"}
	if !slices.Equal(got, want) {
		t.Fatalf(".env.example 默认生效的变量 = %v，期望 %v", got, want)
	}
}
