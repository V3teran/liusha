package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture 覆盖多个 category，供 ByCategory / Names / FilterByNames 断言。
func fixture() *ToolManifest {
	return &ToolManifest{Tools: []Tool{
		{Name: "nuclei", Category: "vulnscan"},
		{Name: "trivy", Category: "vulnscan"},
		{Name: "curl", Category: "utility"},
	}}
}

// TestFilterByNames_EmptyReturnsNone 验证：空白名单 = 空集（严格白名单，不再是"全部可见"魔法）。
func TestFilterByNames_EmptyReturnsNone(t *testing.T) {
	got := fixture().FilterByNames(nil).Names()
	if len(got) != 0 {
		t.Fatalf("空白名单期望空集，得 %v", got)
	}
}

// TestFilterByNames_KeepsWhitelisted 验证：非空白名单只保留其中的工具。
func TestFilterByNames_KeepsWhitelisted(t *testing.T) {
	got := fixture().FilterByNames([]string{"curl", "nuclei", "absent"}).Names()
	want := []string{"curl", "nuclei"} // absent 不在目录，静默忽略
	if !equal(got, want) {
		t.Fatalf("白名单期望 %v，得 %v", want, got)
	}
}

// TestFilterByNames_WildcardKeepsAll 验证：通配 "*"（全部，含以后新增）= 保留目录全集。
// executor / evaluator 的出厂名单就是 ["*"]，生效值未取消任何项时原样下发到这里。
func TestFilterByNames_WildcardKeepsAll(t *testing.T) {
	got := fixture().FilterByNames([]string{"*"}).Names()
	want := []string{"curl", "nuclei", "trivy"}
	if !equal(got, want) {
		t.Fatalf("通配期望全集 %v，得 %v", want, got)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestLoad_RejectsInvalidNames 验证：工具名是 cli_tools 名单与 run_command 的寻址键，
// 重名或缺名都让清单加载失败（重名会在提示词的工具目录里重复出现，且名单只能指代其一）。
func TestLoad_RejectsInvalidNames(t *testing.T) {
	cases := map[string]string{
		"重名": "tools:\n  - name: trufflehog\n    category: vulnscan\n  - name: trufflehog\n    category: sast\n",
		"缺名": "tools:\n  - name: \"\"\n    category: recon\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tools.yaml")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("期望加载失败")
			} else if name == "重名" && !strings.Contains(err.Error(), "trufflehog") {
				t.Fatalf("错误信息应指出重名工具，得 %v", err)
			}
		})
	}
}
