package httpapi

import (
	"reflect"
	"testing"

	agent "github.com/V3teran/liusha/internal/agent"
	cfgtool "github.com/V3teran/liusha/internal/config/tool"
)

// 全量哨兵 "*" 的装配态语义（与运行时 BuildTools/registry.Allows 对齐）：
// 读=全 involved；写=装配幂等 no-op、卸载物化为「目录全集减一」。

func cliCatalog() []cfgtool.Tool {
	return []cfgtool.Tool{
		{Name: "nmap", Kind: cfgtool.KindCLI},
		{Name: "nuclei", Kind: cfgtool.KindCLI},
		{Name: "sqlmap", Kind: cfgtool.KindCLI},
	}
}

func TestToolArrayContains_Wildcard(t *testing.T) {
	h := agent.Profile{Code: "executor", CliTools: []string{"*"}}
	if !toolArrayContains(h, cfgtool.KindCLI, "nmap") {
		t.Error(`cli_tools=["*"] 应视为已装配任意 CLI 工具`)
	}
	if !toolArrayContains(h, cfgtool.KindCLI, "not-in-catalog") {
		t.Error(`cli_tools=["*"] 对目录外名字同样成立（哨兵语义，不做目录校验）`)
	}
	// function_tools 未含哨兵：不受 cli 的 * 影响。
	if toolArrayContains(h, cfgtool.KindFunction, "http_request") {
		t.Error(`function_tools=[] 应未装配`)
	}
}

func TestApplyAssignment_WildcardAssignIsNoOp(t *testing.T) {
	h := agent.Profile{Code: "executor", CliTools: []string{"*"}}
	_, cli := applyAssignment(h, cfgtool.KindCLI, "nmap", true, cliCatalog())
	if !reflect.DeepEqual(cli, []string{"*"}) {
		t.Errorf(`全量态装配应幂等保留 "*", got %v`, cli)
	}
}

func TestApplyAssignment_WildcardUnassignMaterializes(t *testing.T) {
	h := agent.Profile{Code: "executor", CliTools: []string{"*"}}
	_, cli := applyAssignment(h, cfgtool.KindCLI, "sqlmap", false, cliCatalog())
	want := []string{"nmap", "nuclei"}
	if !reflect.DeepEqual(cli, want) {
		t.Errorf(`全量态卸载 sqlmap 应物化为目录全集减一 %v, got %v`, want, cli)
	}
	// function_tools 不受影响。
	if !reflect.DeepEqual(h.FunctionTools, agent.Profile{}.FunctionTools) && len(h.FunctionTools) != 0 {
		t.Errorf("function_tools 不应被 CLI 工具操作改动, got %v", h.FunctionTools)
	}
}

func TestApplyAssignment_ExplicitListUnchanged(t *testing.T) {
	h := agent.Profile{Code: "executor", CliTools: []string{"nmap"}}
	_, cli := applyAssignment(h, cfgtool.KindCLI, "nuclei", true, cliCatalog())
	if !reflect.DeepEqual(cli, []string{"nmap", "nuclei"}) {
		t.Errorf("显式白名单装配行为不应改变, got %v", cli)
	}
	_, cli = applyAssignment(h, cfgtool.KindCLI, "nmap", false, cliCatalog())
	if !reflect.DeepEqual(cli, []string{}) {
		t.Errorf("显式白名单卸载行为不应改变, got %v", cli)
	}
}
