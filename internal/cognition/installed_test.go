package cognition

import (
	"slices"
	"testing"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/tools"
	"github.com/V3teran/liusha/internal/tools/manifest"
)

func TestRestrictToInstalled(t *testing.T) {
	d := &Deps{
		Logger: zerolog.Nop(),
		ToolsManifest: &manifest.ToolManifest{Tools: []manifest.Tool{
			{Name: "nmap"}, {Name: "sqlmap"}, {Name: "ffuf"},
		}},
		Installed: sandbox.Inventory{Tools: map[string]bool{"nmap": true, "sqlmap": false, "ffuf": true}},
	}

	// 「全部」展开为工具全集再剔除未安装；浏览器不可用时 drive_browser 出局，其余函数工具保留。
	got := d.restrictToInstalled(agent.Agent{Config: agent.Config{FunctionTools: []string{"*"}, CliTools: []string{"*"}}})
	if slices.Contains(got.FunctionTools, tools.DriveBrowser) || !slices.Contains(got.FunctionTools, "http_request") {
		t.Fatalf("函数工具 = %v", got.FunctionTools)
	}
	if !slices.Equal(got.CliTools, []string{"ffuf", "nmap"}) {
		t.Fatalf("CLI 工具 = %v", got.CliTools)
	}

	// 显式名单：只保留允许且已安装的；拒绝（nil）保持拒绝，空集保持空集。
	got = d.restrictToInstalled(agent.Agent{Config: agent.Config{FunctionTools: nil, CliTools: []string{"sqlmap", "ffuf"}}})
	if got.FunctionTools != nil || !slices.Equal(got.CliTools, []string{"ffuf"}) {
		t.Fatalf("got fn=%v cli=%v", got.FunctionTools, got.CliTools)
	}
	got = d.restrictToInstalled(agent.Agent{Config: agent.Config{FunctionTools: []string{}, CliTools: []string{}}})
	if got.FunctionTools == nil || len(got.FunctionTools) != 0 || len(got.CliTools) != 0 {
		t.Fatalf("空集应保持空集: fn=%v cli=%v", got.FunctionTools, got.CliTools)
	}

	// 浏览器可用时 drive_browser 回到名单。
	d.Installed.Browser.Installed = true
	got = d.restrictToInstalled(agent.Agent{Config: agent.Config{FunctionTools: []string{tools.DriveBrowser}}})
	if !slices.Equal(got.FunctionTools, []string{tools.DriveBrowser}) {
		t.Fatalf("函数工具 = %v", got.FunctionTools)
	}
}
