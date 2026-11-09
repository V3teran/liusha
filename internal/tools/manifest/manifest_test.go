package manifest

import "testing"

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

