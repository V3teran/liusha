// Package external 的 browser_use 工具：browser-use CLI 子命令的 typed JSON 单工具包装。
//
// 设计：单工具 + action 枚举（vs 多个独立工具的取舍 — 单工具 LLM 学一处即可调度全部浏览器动作）。
//   - LLM 看到 "browser_use" 前缀即联想浏览器场景，action 枚举把所有 browser-use 子命令收编一处
//   - 状态变化 action（open/click/input/wait）执行完，wrapper 自动追加 screenshot 到 $OUTPUT_DIR
//     → 视觉结果自然喂回 vision LLM，无需 LLM 额外调 screenshot 多 1 步 ReAct
//   - 单 BrowserUse.Execute 内 switch action 分流；坐标系换算（grounding）+ 视口尺寸（ViewportW/H）
//     只对 click/input 生效；其余 action 透传
//   - 低频 browser 子命令（scroll/back/keys/hover/select 等）仍走 run_command 兜底
//   - identity 字段：身份 = 浏览器 session（cookie jar）。同一身份所有 orchestrator/exploitation 共用
//     一个浏览器（tab 隔离），不同身份各自独立浏览器——用于越权/BAC 多账号对比。缺省 "default"。
package external

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/grounding"
	toolfx "github.com/V3teran/liusha/internal/toolruntime"
)

// runBrowserSub 把 subcmd + 多个 args 拼成 shell 命令调 RunCommand 跑。
// shell-quote 每个 arg 防注入；tag 用 "browser-<subcmd>" 形式便于运维诊断。
//
// identity 非空时以 IDENTITY env 前缀注入（wrapper 据此选 --session = 身份/cookie jar）；
// identity 已在 Execute 入口校验为 path-safe，这里再 shell-quote 一层防注入。
func runBrowserSub(ctx context.Context, rc *RunCommand, subcmd string, args []string, timeoutSec int, identity string) (toolfx.Result, error) {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellSingleQuote(a)
	}
	cmd := strings.TrimSpace(fmt.Sprintf("browser-use %s %s", subcmd, strings.Join(quoted, " ")))
	if identity != "" {
		cmd = fmt.Sprintf("IDENTITY=%s %s", shellSingleQuote(identity), cmd)
	}
	payload, err := json.Marshal(map[string]any{
		"command":         cmd,
		"timeout_seconds": timeoutSec,
		"tag":             "browser-" + subcmd,
	})
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("browser_use %s marshal: %w", subcmd, err)
	}
	return rc.Execute(ctx, payload)
}

// shellSingleQuote 把字符串用单引号包裹防 shell 注入；内部 ' 用 '\'' 转义。
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// isSafeIdentity 校验 identity 仅含 path-safe 字符（[A-Za-z0-9._-]，≤64）。
//
// identity 会进 wrapper 的 --session 名 + /tmp/browser-tab-<identity>-*.idx 文件路径，
// 必须防 path traversal / 注入。空串合法（= 缺省 "default" 身份）。
func isSafeIdentity(s string) bool {
	if len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == '-':
		default:
			return false
		}
	}
	return true
}

// 默认 timeout（秒）——各 action 对应合理值，LLM 不传时走这里。
const (
	defaultOpenTimeout   = 120 // 含 chromium cold start（远程目标 + 冷启实测 >60s，留足头避免误杀）
	defaultActionTimeout = 20  // click/input 等交互动作 daemon 复用快
	defaultWaitTimeout   = 30  // wait 本身就是等
	defaultReadTimeout   = 15  // eval/extract/source 读取快
)

// BrowserUse 单工具：所有 browser-use 操作经 action 枚举分流。
//
// 字段说明：
//   - Run         : sandbox 容器命令执行入口
//   - CoordSystem : 当前 hunter 路由到的 vision provider 坐标系（vision provider 必填）
//   - ViewportW/H : sandbox chromium 视口尺寸，仅 click/input action 用于 grounding 换算
type BrowserUse struct {
	Run         *RunCommand
	CoordSystem grounding.CoordSystem
	ViewportW   int
	ViewportH   int
}

// Name 返回工具名 "browser_use"。跟底层 lib `browser-use` 1:1 对应（下划线 vs 横线避命名冲突）。
func (a *BrowserUse) Name() string { return "browser_use" }

// Description 描述工具核心语义 — 详细 action / 参数说明放 ParametersJSON description 字段，
// 避免双份冗余。本描述只讲：用途 + 坐标系 + 自动截图特性 + 身份共享 + 禁 run_command 重复包装。
func (a *BrowserUse) Description() string {
	return fmt.Sprintf(
		"用 chromium 真实浏览器操作目标页面。action 字段选具体动作（open/click/input/wait/eval/extract/source），其它字段按 action 要求填（见各字段 description）。"+
			"\n坐标系：%s。视口固定 %dx%d。"+
			"\n状态变化 action（open/click/input/wait）执行完自动附最新截图给 LLM；读取 action 不附图。"+
			"\n身份共享：同一 identity 下所有 orchestrator/exploitation 共用一个浏览器（cookies/登录态共享），各自页面互不干扰；越权/BAC 测多账号时给不同 identity 各开一个独立浏览器（见 identity 字段）。"+
			"\n本工具是 browser-use CLI 的 typed 包装——**不要**再用 `run_command \"browser-use ...\"` 重复调用。",
		grounding.Describe(a.CoordSystem), a.ViewportW, a.ViewportH,
	)
}

func (a *BrowserUse) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "action":{"type":"string","enum":["open","state","click","input","wait","eval","extract","source","reset"],"description":"open=导航URL / state=拿当前页 numbered 元素清单（[1]<a>X</a> [2]<button>Login</button>... 含 viewport 尺寸 + DOM 结构，LLM 据此选 index） / click=点击元素（优先 index 准确，x/y 是兜底） / input=给元素键入文本（优先 index） / wait=等条件 / eval=在当前页执行任意JS（如 document.querySelector('button').click() / 取 DOM 数据 / 调试 fetch） / extract=LLM抽数据 / source=拿当前页渲染后完整 HTML（可用于看 selector 或 DOM 结构） / reset=强杀本身份 chromium 会话重启（用于反复 timeout / 卡死场景；副作用：本身份所有 tab 丢失含登录态需重新 open+登录）"},
    "url":{"type":"string","description":"action=open 必填：目标 URL（含 http:// 或 https://）"},
    "index":{"type":"integer","minimum":0,"description":"action=click/input 推荐：state 返回清单里的元素编号（[N] 的 N），比 x/y 稳。注意 index 是 state 那一刻的临时快照编号：紧接 state 后立即用，中间别插会改页面的动作（导航/点击触发重渲染会让编号失效）。若返回 'Element index N not found' = 页面已变，**重新 state 拿新编号再操作（别复用旧 N）**；同一元素反复 index 失效时改用 action=eval 的 document.querySelector('selector').click() / .value=... 走稳定 CSS selector。"},
    "x":{"type":"integer","description":"action=click/input 兜底：元素中心 x（坐标系见 Description）— 仅在没有 index 时使用，vision 给坐标偏差大易 timeout"},
    "y":{"type":"integer","description":"action=click/input 兜底：元素中心 y — 仅在没有 index 时使用"},
    "text":{"type":"string","description":"action=input 必填：要键入的文本"},
    "condition":{"type":"string","description":"action=wait 必填：要等的目标——CSS selector（wait_type=selector，如 '#main'）或页面文本（wait_type=text）。注意：不支持等秒数/networkidle，纯延时请用 run_command 'sleep N'"},
    "wait_type":{"type":"string","enum":["selector","text"],"description":"action=wait 可选：condition 是 CSS selector（默认）还是页面文本"},
    "code":{"type":"string","description":"action=eval 必填：JS 代码，最后表达式作为返回值"},
    "query":{"type":"string","description":"action=extract 必填：自然语言描述要抽什么"},
    "identity":{"type":"string","description":"身份/账号名（= 浏览器 session，cookie jar 边界）。**传你要扮演的账号用户名**（与 read_credentials 的凭证 name 对齐，如 'admin' / 'gordonb'）——同名=同 jar，orchestrator/exploitation 跨 hunter 自动复用同一登录态；不同 identity 各自独立浏览器+登录态、互不污染（越权/BAC 多账号即靠此各开一个）。只有无账号的匿名浏览才留空走默认身份。"},
    "timeout_seconds":{"type":"integer","minimum":1,"maximum":180,"description":"硬超时秒；缺省 open=120 / click/input=20 / wait=30 / state/eval/extract/source=15"}
  },
  "required":["action"]
}`)
}

// Execute 按 action 分流到对应底层 browser-use 子命令。
func (a *BrowserUse) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	// Index 用 *int 区分"没传"和"传了 0"；JSON 缺字段 → 指针 nil。
	var in struct {
		Action         string `json:"action"`
		URL            string `json:"url"`
		Index          *int   `json:"index"`
		X              int    `json:"x"`
		Y              int    `json:"y"`
		Text           string `json:"text"`
		Condition      string `json:"condition"`
		WaitType       string `json:"wait_type"`
		Code           string `json:"code"`
		Query          string `json:"query"`
		Identity       string `json:"identity"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 browser_use 参数: %w", err)
	}
	// identity 进 --session 名 + 文件路径，校验 path-safe 防注入/traversal（空 = 默认身份）。
	if !isSafeIdentity(in.Identity) {
		return toolfx.Result{}, fmt.Errorf("browser_use identity 非法 %q（仅允许 [A-Za-z0-9._-]，≤64 字符）", in.Identity)
	}
	switch in.Action {
	case "open":
		if in.URL == "" {
			return toolfx.Result{}, fmt.Errorf("browser_use open: url 必填且非空")
		}
		// open 含 chromium cold start（实测 >60s），defaultOpenTimeout 当**地板**而非零默认：
		// LLM 传的小值（如 30）会在冷启 + 登录中途被 sandbox-server SIGKILL，
		// 导致共享 cookie jar 没种上 → 后续 exploitation open 受保护页被 302 回 login → 各自重登。
		// timeout 是 SIGKILL 上限不是 sleep，热 open 命中即秒回，抬地板对热路径零延迟代价。
		if in.TimeoutSeconds < defaultOpenTimeout {
			in.TimeoutSeconds = defaultOpenTimeout
		}
		return runBrowserSub(ctx, a.Run, "open", []string{in.URL}, in.TimeoutSeconds, in.Identity)

	case "state":
		// numbered DOM 清单（[1]<tag>text</tag> ...）+ viewport 尺寸，
		// LLM 据此选 element index 给后续 click/input 用。比 vision 猜坐标稳得多。
		if in.TimeoutSeconds == 0 {
			in.TimeoutSeconds = defaultReadTimeout
		}
		return runBrowserSub(ctx, a.Run, "state", nil, in.TimeoutSeconds, in.Identity)

	case "click":
		if in.TimeoutSeconds == 0 {
			in.TimeoutSeconds = defaultActionTimeout
		}
		// 优先 index 路径（稳）；fallback 到 x/y 坐标（vision 不准易 timeout）
		if in.Index != nil {
			return runBrowserSub(ctx, a.Run, "click", []string{fmt.Sprintf("%d", *in.Index)}, in.TimeoutSeconds, in.Identity)
		}
		realX, realY, err := grounding.ToRealPixels(in.X, in.Y, a.CoordSystem, a.ViewportW, a.ViewportH)
		if err != nil {
			return toolfx.Result{}, fmt.Errorf("browser_use click 坐标换算: %w", err)
		}
		return runBrowserSub(ctx, a.Run, "click", []string{fmt.Sprintf("%d", realX), fmt.Sprintf("%d", realY)}, in.TimeoutSeconds, in.Identity)

	case "input":
		if in.TimeoutSeconds == 0 {
			in.TimeoutSeconds = defaultActionTimeout
		}
		// 优先 index 路径：browser-use input <index> <text> 一步到位（cli 内部处理拿焦点 + type）
		if in.Index != nil {
			return runBrowserSub(ctx, a.Run, "input", []string{fmt.Sprintf("%d", *in.Index), in.Text}, in.TimeoutSeconds, in.Identity)
		}
		// fallback x/y 坐标：click 拿焦点 + type 两步
		realX, realY, err := grounding.ToRealPixels(in.X, in.Y, a.CoordSystem, a.ViewportW, a.ViewportH)
		if err != nil {
			return toolfx.Result{}, fmt.Errorf("browser_use input 坐标换算: %w", err)
		}
		if _, err := runBrowserSub(ctx, a.Run, "click", []string{fmt.Sprintf("%d", realX), fmt.Sprintf("%d", realY)}, in.TimeoutSeconds, in.Identity); err != nil {
			return toolfx.Result{}, fmt.Errorf("browser_use input click 拿焦点: %w", err)
		}
		return runBrowserSub(ctx, a.Run, "type", []string{in.Text}, in.TimeoutSeconds, in.Identity)

	case "wait":
		// browse-use-cli wait 只支持 `wait selector <css>` / `wait text <str>`（带 --timeout ms）；
		// 不支持等秒数 / networkidle（纯延时用 run_command sleep）。
		if in.Condition == "" {
			return toolfx.Result{}, fmt.Errorf("browser_use wait: condition 必填（CSS selector 或 文本，配合 wait_type）")
		}
		if in.TimeoutSeconds == 0 {
			in.TimeoutSeconds = defaultWaitTimeout
		}
		waitType := in.WaitType
		if waitType == "" {
			waitType = "selector" // 缺省等元素出现（最常见）
		}
		if waitType != "selector" && waitType != "text" {
			return toolfx.Result{}, fmt.Errorf("browser_use wait: wait_type 只支持 'selector' 或 'text'（得到 %q）", waitType)
		}
		// --timeout 用毫秒，对齐外层 timeout_seconds（元素早出现则提前返回）。
		return runBrowserSub(ctx, a.Run, "wait",
			[]string{waitType, in.Condition, "--timeout", fmt.Sprintf("%d", in.TimeoutSeconds*1000)},
			in.TimeoutSeconds, in.Identity)

	case "eval":
		if in.Code == "" {
			return toolfx.Result{}, fmt.Errorf("browser_use eval: code 必填")
		}
		if in.TimeoutSeconds == 0 {
			in.TimeoutSeconds = defaultReadTimeout
		}
		return runBrowserSub(ctx, a.Run, "eval", []string{in.Code}, in.TimeoutSeconds, in.Identity)

	case "extract":
		if in.Query == "" {
			return toolfx.Result{}, fmt.Errorf("browser_use extract: query 必填")
		}
		if in.TimeoutSeconds == 0 {
			in.TimeoutSeconds = defaultReadTimeout
		}
		return runBrowserSub(ctx, a.Run, "extract", []string{in.Query}, in.TimeoutSeconds, in.Identity)

	case "source":
		if in.TimeoutSeconds == 0 {
			in.TimeoutSeconds = defaultReadTimeout
		}
		// browser-use-cli 0.12.9 原生 `get html`，比 eval outerHTML 更直接且无 JS 编码风险。
		return runBrowserSub(ctx, a.Run, "get", []string{"html"}, in.TimeoutSeconds, in.Identity)

	case "reset":
		// daemon 异常恢复：wrapper reset 子命令停本身份 browse-use session daemon + 清本身份 tab-idx。
		// 下次该身份任意 browser_use 操作自动重启。用于反复 timeout / page hung 场景。
		if in.TimeoutSeconds == 0 {
			in.TimeoutSeconds = defaultActionTimeout
		}
		return runBrowserSub(ctx, a.Run, "reset", nil, in.TimeoutSeconds, in.Identity)

	default:
		return toolfx.Result{}, fmt.Errorf("browser_use: action 非法 %q（支持 open/state/click/input/wait/eval/extract/source/reset）", in.Action)
	}
}
