package einotools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

// browser_use 工具：把 browser-use CLI 子命令包装成单一 typed 工具（action 枚举收编所有浏览器动作）。
//
// 为什么是独立工具而非让 LLM 裸 run_command：核心是 identity——LLM 传 identity 字段，工具自动拼成
// `IDENTITY=<identity> browser-use <action> ...` 行内 env（实测 sh -c 嵌套不丢 env）。wrapper 据此选
// /tmp/browser-svc-<identity>.sock = 独立 chromium cookie jar。同名 identity 跨 orchestrator/exploitation
// 复用同一浏览器登录态（见 system_prompt.md「identity 命名铁律」）——根治「子代理各自重登」。
//
// 为什么手写 BaseTool 而非 utils.InferTool：browser 动作要回灌截图（image part），InferTool 的 func 只返
// string 无多模态通道（同 run_command）。故用 GoStruct2ToolInfo 从 struct tag 生成 schema + 手写
// InvokableRun，复用 run_command 的 execInSandbox（截 tail + 抽图 + vision 适配一套加工共享）。
// 不做 react 的 grounding 坐标换算（liusha 已删该包；click/input 走 state 返回的 index，比 x/y 稳）。

const (
	browserOpenTimeout = 120 // open 含 chromium 冷启（实测 >60s），当地板而非默认——超时太短会种不上登录态
	browserWaitTimeout = 30
	browserReadTimeout = 15 // state/eval/source 读取快
	browserActTimeout  = 20 // click/input 交互复用 daemon 快
)

// browserUseArgs 是 browser_use 入参；jsonschema tag → GoStruct2ToolInfo 自动生成 schema。
// 可选字段带 ,omitempty 避免被误标 required（见 findings.go 详注）。
type browserUseArgs struct {
	Action string `json:"action"             jsonschema:"required,enum=open,enum=state,enum=click,enum=input,enum=wait,enum=eval,enum=source,enum=reset,description=动作：open=导航URL / state=拿当前页 numbered 元素清单（[1]<a> [2]<button>… LLM 据此选 index）/ click=点元素（用 index）/ input=给元素键入（用 index+text）/ wait=等元素出现 / eval=在页面跑 JS / source=拿渲染后完整 HTML / reset=强杀本身份 chromium 重启（反复卡死时用，副作用：本身份登录态丢失需重 open+登录）"`
	URL    string `json:"url,omitempty"      jsonschema:"description=action=open 必填：目标 URL（含 http:// 或 https://）"`
	Index  *int   `json:"index,omitempty"    jsonschema:"description=action=click/input 必填：state 返回清单里的元素编号（[N] 的 N）。index 是 state 那一刻的快照编号——紧接 state 后立即用，中间别插会改页面的动作；报 'not found' 就重新 state 拿新编号"`
	Text   string `json:"text,omitempty"     jsonschema:"description=action=input 必填：要键入的文本"`
	Cond   string `json:"condition,omitempty" jsonschema:"description=action=wait 必填：要等的 CSS selector（如 #main）或页面文本"`
	Code   string `json:"code,omitempty"     jsonschema:"description=action=eval 必填：JS 代码，最后表达式作为返回值（如 document.querySelector('button').click()）"`
	// identity = 浏览器身份（cookie jar）= 账号用户名。同名跨 orchestrator/exploitation 复用登录态。
	Identity string `json:"identity,omitempty" jsonschema:"description=浏览器身份=账号用户名（admin→admin / gordonb→gordonb），同名复用登录态不必重登；常规挖洞留空（默认 default）；只有越权/BAC 多账号对比才传不同名各开独立浏览器"`
	Timeout  int    `json:"timeout_seconds,omitempty" jsonschema:"description=硬超时秒（缺省 open=120 / click/input=20 / wait=30 / state/eval/source=15）"`
}

// BuildBrowserUse 造 browser_use 工具。executor/hunterID/tailBytes 闭包注入（与 run_command 同款）。
func BuildBrowserUse(executor SandboxExecutor, hunterID string, tailBytes int) (tool.BaseTool, error) {
	if executor == nil {
		return nil, fmt.Errorf("browser_use: Sandbox 未注入")
	}
	if hunterID == "" {
		return nil, fmt.Errorf("browser_use: HunterID 未注入")
	}
	info, err := utils.GoStruct2ToolInfo[browserUseArgs](
		"browser_use",
		"用 chromium 真实浏览器操作目标页面（登录/点击/读 DOM/跑 JS）。action 选动作，其它字段按 action 要求填。"+
			"典型流程：open 受保护页→若落登录页则 state 看元素→input 填账密→click 提交→重新 open 验证带态。"+
			"状态变化 action（open/click/input/wait）执行完自动附最新截图给 LLM；读取 action（state/eval/source）不附图。"+
			"identity=账号名做登录态复用，见 system prompt 的 identity 命名铁律。",
	)
	if err != nil {
		return nil, fmt.Errorf("browser_use: 生成 schema: %w", err)
	}
	tn := tailBytes
	if tn <= 0 {
		tn = fallbackRunTailBytes
	}
	return &browserUseTool{executor: executor, hunterID: hunterID, tailBytes: tn, info: info}, nil
}

// browserUseTool 实现 tool.BaseTool + EnhancedInvokableTool（与 run_command 一样要回灌截图 image part）。
type browserUseTool struct {
	executor  SandboxExecutor
	hunterID  string
	tailBytes int
	info      *schema.ToolInfo
}

func (t *browserUseTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

// InvokableRun 解析参数 → 按 action 校验必填 + 选默认 timeout → 拼 `IDENTITY=x browser-use ...` → execInSandbox。
func (t *browserUseTool) InvokableRun(ctx context.Context, arg *schema.ToolArgument, _ ...tool.Option) (*schema.ToolResult, error) {
	var in browserUseArgs
	if arg != nil && arg.Text != "" {
		if err := json.Unmarshal([]byte(arg.Text), &in); err != nil {
			return nil, fmt.Errorf("解析 browser_use 参数失败: %w", err)
		}
	}
	subArgs, err := browserSubArgs(&in)
	if err != nil {
		return nil, err
	}
	timeout := in.Timeout
	if timeout <= 0 {
		timeout = browserDefaultTimeout(in.Action)
	}
	cmd, err := buildBrowserCommand(in.Action, subArgs, in.Identity)
	if err != nil {
		return nil, err
	}
	return execInSandbox(ctx, t.executor, t.hunterID, cmd, timeout, "browser-"+in.Action, t.tailBytes)
}

// browserSubArgs 按 action 校验必填字段，返回拼到 `browser-use <action>` 后面的参数列表。
func browserSubArgs(in *browserUseArgs) ([]string, error) {
	switch in.Action {
	case "open":
		if strings.TrimSpace(in.URL) == "" {
			return nil, fmt.Errorf("browser_use open: url 必填")
		}
		return []string{in.URL}, nil
	case "state", "reset":
		return nil, nil
	case "source":
		// daemon 无 source 子命令，等价 `get html`（拿渲染后完整 HTML）；Go 侧翻译子命令（见 buildBrowserCommand），
		// 对 LLM 保留直观的 source 名。返回 args=["html"] 供拼成 `get html`。
		return []string{"html"}, nil
	case "click":
		if in.Index == nil {
			return nil, fmt.Errorf("browser_use click: index 必填（先 state 拿元素编号）")
		}
		return []string{fmt.Sprintf("%d", *in.Index)}, nil
	case "input":
		if in.Index == nil || in.Text == "" {
			return nil, fmt.Errorf("browser_use input: index + text 必填")
		}
		return []string{fmt.Sprintf("%d", *in.Index), in.Text}, nil
	case "wait":
		if strings.TrimSpace(in.Cond) == "" {
			return nil, fmt.Errorf("browser_use wait: condition 必填")
		}
		return []string{in.Cond}, nil
	case "eval":
		if strings.TrimSpace(in.Code) == "" {
			return nil, fmt.Errorf("browser_use eval: code 必填")
		}
		return []string{in.Code}, nil
	case "screenshot":
		// daemon 支持 screenshot，但状态变化动作（open/click/input/wait）后已自动附最新截图给 LLM，无需手调；
		// 明确告知避免 LLM 因不知道自动附图而反复瞎调 screenshot（实测踩过）。
		return nil, fmt.Errorf("browser_use: 无需手动 screenshot——open/click/input/wait 等动作后已自动附最新截图给你；要导出 HTML 用 source、跑 JS 用 eval")
	default:
		return nil, fmt.Errorf("browser_use: 未知 action %q（仅支持 open/state/click/input/wait/eval/source/reset）", in.Action)
	}
}

func browserDefaultTimeout(action string) int {
	switch action {
	case "open":
		return browserOpenTimeout
	case "wait":
		return browserWaitTimeout
	case "click", "input":
		return browserActTimeout
	default:
		return browserReadTimeout
	}
}

// buildBrowserCommand 拼 `[IDENTITY=<id>] browser-use <action> <shell-quoted args...>`。
// identity 校验 path-safe（进 wrapper 的 socket 路径，防 path traversal / 注入）；每个 arg 单引号包裹防注入。
func buildBrowserCommand(action string, args []string, identity string) (string, error) {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellSingleQuote(a)
	}
	// action → daemon 子命令：多数同名；source 翻译为 daemon 的 `get`（配合 args=["html"] = `get html` 拿渲染后 HTML）。
	sub := action
	if action == "source" {
		sub = "get"
	}
	cmd := strings.TrimSpace(fmt.Sprintf("browser-use %s %s", sub, strings.Join(quoted, " ")))
	if id := strings.TrimSpace(identity); id != "" {
		if !isSafeIdentity(id) {
			return "", fmt.Errorf("browser_use: identity 仅允许 [A-Za-z0-9._-]、≤64 字符，收到 %q", identity)
		}
		cmd = fmt.Sprintf("IDENTITY=%s %s", shellSingleQuote(id), cmd)
	}
	return cmd, nil
}

// shellSingleQuote 单引号包裹防 shell 注入；内部 ' 用 '\” 转义。
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// isSafeIdentity 校验 identity 仅含 path-safe 字符（[A-Za-z0-9._-]，≤64）。空串调用方已先排除。
func isSafeIdentity(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}
