package agent

import (
	"errors"
	"slices"
	"testing"

	"github.com/V3teran/liusha/internal/builtin"
	"github.com/V3teran/liusha/internal/override"
)

func catalog() Catalog {
	return Catalog{
		FunctionTools: func() []string { return []string{"a", "b"} },
		CliTools:      func() []string { return []string{"nmap", "sqlmap", "nuclei"} },
		Skills:        func() []string { return []string{"bac", "browser-use"} },
	}
}

func TestResolve_DefaultsWithoutOverrides(t *testing.T) {
	b, _ := builtin.AgentByCode("executor")
	p, err := Resolve(b, nil, catalog())
	if err != nil {
		t.Fatal(err)
	}
	if p.Code != "executor" || p.Charter != b.Charter || !slices.Equal(p.CliTools, []string{"*"}) {
		t.Fatalf("无自定义时生效值即出厂值: %+v", p.Config)
	}
}

// 出厂为「全部」时取消某 CLI 工具：按全集展开再减去。
func TestResolve_WildcardMinusOne(t *testing.T) {
	b, _ := builtin.AgentByCode("executor")
	ov := override.Overrides{FieldCliTools: {Removed: override.Strings([]string{"sqlmap"})}}
	p, err := Resolve(b, ov, catalog())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.CliTools, []string{"nmap", "nuclei"}) {
		t.Fatalf("生效 CLI 工具 = %v", p.CliTools)
	}
}

func TestValidate(t *testing.T) {
	ok := Config{Charter: "c", Description: "d", Complexity: "medium", MaxIterations: 10}
	if err := Validate(ok); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Config{
		{Charter: " ", Description: "d", Complexity: "medium", MaxIterations: 10},
		{Charter: "c", Description: "", Complexity: "medium", MaxIterations: 10},
		{Charter: "c", Description: "d", Complexity: "moderate", MaxIterations: 10},
		{Charter: "c", Description: "d", Complexity: "medium", MaxIterations: MaxIterationsLimit + 1},
	} {
		if err := Validate(bad); !errors.Is(err, override.ErrInvalid) {
			t.Errorf("%+v 应被拒，得 %v", bad, err)
		}
	}
}

// 字段表与 Config 的 json 字段一一对应：漏登记的字段无法自定义，多登记的字段无出厂值。
func TestSchemaCoversConfig(t *testing.T) {
	doc, _ := override.ToDocument(Config{})
	schema := Schema(catalog())
	for name := range doc {
		if _, ok := schema[name]; !ok {
			t.Errorf("Config 字段 %s 未登记到 Schema", name)
		}
	}
	for name := range schema {
		if _, ok := doc[name]; !ok {
			t.Errorf("Schema 字段 %s 不在 Config 中", name)
		}
	}
}
