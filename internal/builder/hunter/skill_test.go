package hunter

import (
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/tools/manifest"
)

func TestBuildToolingCatalog(t *testing.T) {
	t.Run("nil manifest 返回空串", func(t *testing.T) {
		if got := buildToolingCatalog(nil); got != "" {
			t.Errorf("want empty, got %q", got)
		}
	})

	t.Run("空 tools 返回空串", func(t *testing.T) {
		m := &manifest.Manifest{Tools: nil}
		if got := buildToolingCatalog(m); got != "" {
			t.Errorf("want empty, got %q", got)
		}
	})

	t.Run("已知 category 按 toolingCategoryOrder 渲染", func(t *testing.T) {
		// 故意乱序构造，验证渲染输出按 toolingCategoryOrder 固定顺序。
		m := &manifest.Manifest{Tools: []manifest.Tool{
			{Name: "sqlmap", Category: "injection", Description: "SQLi 自动化"},
			{Name: "httpx", Category: "recon", Description: "HTTP 探活"},
			{Name: "subfinder", Category: "recon", Description: "子域枚举"},
			{Name: "curl", Category: "utility", Description: "原生 HTTP"},
		}}
		got := buildToolingCatalog(m)

		mustContain(t, got, "## 可用外部工具索引")
		mustContain(t, got, "subfinder")
		mustContain(t, got, "sqlmap")
		mustContain(t, got, "curl")

		// recon 必须出现在 injection 之前（toolingCategoryOrder 顺序）。
		mustOrder(t, got, "recon", "injection")
		mustOrder(t, got, "injection", "utility")
		// 同 category 内按 name 字典序：httpx < subfinder
		mustOrder(t, got, "httpx", "subfinder")
	})

	t.Run("未知 category 落入未分类组", func(t *testing.T) {
		m := &manifest.Manifest{Tools: []manifest.Tool{
			{Name: "nuclei", Category: "vulnscan", Description: "漏扫"},
			{Name: "myweirdtool", Category: "magic", Description: "未来类别"},
		}}
		got := buildToolingCatalog(m)
		mustContain(t, got, "未分类")
		mustContain(t, got, "myweirdtool")
		// 已知 category 应排在未分类之前。
		mustOrder(t, got, "vulnscan", "未分类")
	})

	t.Run("空 category 字段也归未分类", func(t *testing.T) {
		m := &manifest.Manifest{Tools: []manifest.Tool{
			{Name: "orphan", Category: "", Description: "缺 category"},
		}}
		got := buildToolingCatalog(m)
		mustContain(t, got, "未分类")
		mustContain(t, got, "orphan")
	})
}

func TestBuildVulnCatalog(t *testing.T) {
	t.Run("nil loader 返回空串", func(t *testing.T) {
		if got := buildVulnCatalog(nil); got != "" {
			t.Errorf("want empty, got %q", got)
		}
	})
}

func TestSortHelpers(t *testing.T) {
	t.Run("sortStrings 升序", func(t *testing.T) {
		in := []string{"c", "a", "b"}
		sortStrings(in)
		want := []string{"a", "b", "c"}
		for i := range in {
			if in[i] != want[i] {
				t.Fatalf("idx %d: want %q, got %q", i, want[i], in[i])
			}
		}
	})

	t.Run("sortCardsByName 按 Name 升序", func(t *testing.T) {
		in := []*skill.Card{
			{Name: "zeta"},
			{Name: "alpha"},
			{Name: "mu"},
		}
		sortCardsByName(in)
		want := []string{"alpha", "mu", "zeta"}
		for i := range in {
			if in[i].Name != want[i] {
				t.Fatalf("idx %d: want %q, got %q", i, want[i], in[i].Name)
			}
		}
	})

	t.Run("空切片不 panic", func(t *testing.T) {
		sortStrings(nil)
		sortCardsByName(nil)
	})
}

func TestFirstLine(t *testing.T) {
	tests := []struct {
		in   string
		max  int
		want string
	}{
		{"single line", 100, "single line"},
		{"first\nsecond", 100, "first"},
		{"verylongsingleline", 4, "very"},
		{"", 10, ""},
		{"first\nsecond", 0, "first"}, // max=0 不截断
	}
	for _, tc := range tests {
		if got := firstLine(tc.in, tc.max); got != tc.want {
			t.Errorf("firstLine(%q, %d): want %q, got %q", tc.in, tc.max, tc.want, got)
		}
	}
}

// mustContain 断言 haystack 含有 needle，否则 fail。
func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("want %q in output, missing.\noutput:\n%s", needle, haystack)
	}
}

// mustOrder 断言 first 在 haystack 中先于 second 出现。
func mustOrder(t *testing.T, haystack, first, second string) {
	t.Helper()
	i := strings.Index(haystack, first)
	j := strings.Index(haystack, second)
	if i < 0 || j < 0 {
		t.Fatalf("一个或两个 marker 不在输出中: first=%q(idx=%d) second=%q(idx=%d)", first, i, second, j)
	}
	if i >= j {
		t.Errorf("want %q before %q, got reversed (idx %d vs %d)", first, second, i, j)
	}
}
