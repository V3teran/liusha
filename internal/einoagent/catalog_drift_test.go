package einoagent

import (
	"sort"
	"testing"

	"github.com/V3teran/liusha/internal/einotools"
)

// TestFunctionToolCatalogMatchesRegistry 防漂移：einotools.FunctionToolCatalog 的名字集合
// 必须与工具注册表 KnownToolNames() 严格双向一致。新增/删除内置工具而漏改元数据目录即失败。
func TestFunctionToolCatalogMatchesRegistry(t *testing.T) {
	registry := KnownToolNames()
	sort.Strings(registry)

	catalog := make([]string, 0, len(einotools.FunctionToolCatalog))
	seen := map[string]bool{}
	for _, m := range einotools.FunctionToolCatalog {
		if seen[m.Name] {
			t.Fatalf("元数据目录出现重复工具名: %q", m.Name)
		}
		seen[m.Name] = true
		if m.Category == "" || m.Description == "" {
			t.Fatalf("工具 %q 缺少 Category 或 Description", m.Name)
		}
		catalog = append(catalog, m.Name)
	}
	sort.Strings(catalog)

	regSet := map[string]bool{}
	for _, n := range registry {
		regSet[n] = true
	}
	for _, n := range catalog {
		if !regSet[n] {
			t.Errorf("元数据目录含未注册工具: %q（注册表无此工具）", n)
		}
	}
	for _, n := range registry {
		if !seen[n] {
			t.Errorf("注册表工具缺少元数据: %q（请补 einotools.FunctionToolCatalog）", n)
		}
	}
}
