package einotools

import (
	"os"
	"regexp"
	"testing"
)

// daemonSubcommandRe 抽 browser-svc.py dispatch 的子命令名（`sub == "xxx"` / `elif sub == "xxx"`）。
var daemonSubcommandRe = regexp.MustCompile(`sub == "([a-z-]+)"`)

// TestBrowserActionsAlignWithDaemon 守护 Go 工具 action ↔ browser-svc daemon 子命令一致。
//
// 背景：browser_use 是「Go schema(action enum) → wrapper → browser-svc daemon(真实现)」三层。
// 三份手写曾错位——daemon 无 `source`（用 `get`）却被 schema 声明、daemon 有 `screenshot` 却不在 schema。
// 已统一为 action 与 daemon 子命令 **严格 1:1 同名**（零翻译）。本测试解析 daemon 实际 dispatch 的子命令，
// 校验 Go 暴露的每个 action 都在其中：daemon 删/改 dispatch、或 Go 加 action 到 daemon 没有的子命令时变红，
// 防 source/screenshot 类错位复发。这是「single source 的轻量守护」——比上 MCP 直面真根因且零运行时成本。
func TestBrowserActionsAlignWithDaemon(t *testing.T) {
	// Go 暴露的 action（与 browserSubArgs 的 case 集同步）——全部与 daemon 子命令同名 1:1。
	exposedActions := []string{"open", "state", "click", "input", "wait", "eval", "get", "reset"}

	daemonSubs := parseDaemonSubcommands(t)
	for _, action := range exposedActions {
		if !daemonSubs[action] {
			t.Errorf("Go action %q 不在 browser-svc.py daemon dispatch 中——Go↔daemon 错位（source/screenshot 类 bug 复发）；daemon 现支持：%v",
				action, keys(daemonSubs))
		}
	}
}

// parseDaemonSubcommands 从 browser-svc.py 抽出 daemon dispatch 的子命令集（真相源）。
func parseDaemonSubcommands(t *testing.T) map[string]bool {
	t.Helper()
	const path = "../../deployments/tool-images/pentools/browser-svc.py"
	src, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("读 browser-svc.py 失败（跳过 Go↔daemon 对齐校验）: %v", err)
	}
	subs := map[string]bool{}
	for _, m := range daemonSubcommandRe.FindAllStringSubmatch(string(src), -1) {
		subs[m[1]] = true
	}
	if len(subs) == 0 {
		t.Fatalf("browser-svc.py 未解析到任何 `sub == \"...\"` 子命令——解析正则或文件结构变了，校验失效")
	}
	return subs
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
