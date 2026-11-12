package tools

import "github.com/V3teran/liusha/internal/sandbox"

// DriveBrowser 是浏览器操作工具的名称：它在沙盒里驱动浏览器，可用性取决于沙盒的浏览器能力。
const DriveBrowser = "drive_browser"

// CLIInstalled 判定 CLI 工具是否已装在沙盒上（沙盒未上报的视为未安装）。
func CLIInstalled(inv sandbox.Inventory, name string) bool {
	return inv.Tools[name]
}

// FunctionInstalled 判定函数工具在沙盒上是否具备运行条件：drive_browser 取决于浏览器能力，
// 其余函数工具在 runner 进程内执行、恒可用。
func FunctionInstalled(inv sandbox.Inventory, name string) bool {
	return name != DriveBrowser || inv.Browser.Installed
}
