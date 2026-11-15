package tools

import (
	"testing"

	"github.com/V3teran/liusha/internal/sandbox"
)

func TestInstalled(t *testing.T) {
	inv := sandbox.Inventory{Tools: map[string]bool{"nmap": true, "sqlmap": false}}
	if !CLIInstalled(inv, "nmap") || CLIInstalled(inv, "sqlmap") || CLIInstalled(inv, "unreported") {
		t.Fatal("CLI 工具按沙盒上报判定，未上报视为未安装")
	}
	if FunctionInstalled(inv, DriveBrowser) {
		t.Fatal("浏览器不可用时 drive_browser 不可用")
	}
	inv.Browser.Installed = true
	if !FunctionInstalled(inv, DriveBrowser) || !FunctionInstalled(inv, "http_request") {
		t.Fatal("浏览器可用时 drive_browser 可用；其余函数工具恒可用")
	}
}
