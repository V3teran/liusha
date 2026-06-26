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
// 三份手写曾错位——daemon 无 `source`（用 `get html`）却被 schema 声明、daemon 有 `screenshot`
// 却不在 schema。本测试解析 daemon 实际 dispatch 的子命令，校验 Go 侧每个 action 映射到的子命令
// 都在其中：daemon 删/改 dispatch、或 Go 加 action 映射到 daemon 没有的子命令时，此测试变红，
// 防 source/screenshot 类错位复发。这是「single source 的轻量守护」——比上 MCP 直面真根因且零运行时成本。
func TestBrowserActionsAlignWithDaemon(t *testing.T) {
	// Go action → 实际发给 daemon 的子命令（多数同名；source→get，见 buildBrowserCommand 映射）。
	// 与 browserSubArgs 的 action 集 + buildBrowserCommand 的翻译保持同步。
	actionToSub := map[string]string{
		"open":   "open",
		"state":  "state",
		"click":  "click",
		"input":  "input",
		"wait":   "wait",
		"eval":   "eval",
		"source": "get", // daemon 无 source，等价 get html
		"reset":  "reset",
	}

	daemonSubs := parseDaemonSubcommands(t)
	for action, sub := range actionToSub {
		if !daemonSubs[sub] {
			t.Errorf("Go action %q 映射的 daemon 子命令 %q 不在 browser-svc.py dispatch 中——Go↔daemon 错位（source/screenshot 类 bug 复发）；daemon 现支持：%v",
				action, sub, keys(daemonSubs))
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
