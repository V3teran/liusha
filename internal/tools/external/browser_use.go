// Package external 的 browser_use 工具：browser-use CLI 子命令的 typed JSON 单工具包装。
//
// 设计：单工具 + action 枚举（vs 多个独立工具的取舍 — 单工具 LLM 学一处即可调度全部浏览器动作）。
//   - LLM 看到 "browser_use" 前缀即联想浏览器场景，action 枚举把所有 browser-use 子命令收编一处
//   - 状态变化 action（open/click/input/wait）执行完，wrapper 自动追加 screenshot 到 $OUTPUT_DIR
//     → 视觉结果自然喂回 vision LLM，无需 LLM 额外调 screenshot 多 1 步 ReAct
//   - 单 BrowserUse.Execute 内 switch action 分流；坐标系换算（grounding）+ 视口尺寸（ViewportW/H）
//     只对 click/input 生效；其余 action 透传
//   - 低频 browser 子命令（scroll/back/keys/hover/select 等）仍走 run_command 兜底
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
func runBrowserSub(ctx context.Context, rc *RunCommand, subcmd string, args []string, timeoutSec int) (toolfx.Result, error) {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellSingleQuote(a)
	}
	cmd := strings.TrimSpace(fmt.Sprintf("browser-use %s %s", subcmd, strings.Join(quoted, " ")))
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

// 默认 timeout（秒）——各 action 对应合理值，LLM 不传时走这里。
const (
	defaultOpenTimeout   = 60 // 含 chromium cold start
	defaultActionTimeout = 20 // click/input 等交互动作 daemon 复用快
	defaultWaitTimeout   = 30 // wait 本身就是等
	defaultReadTimeout   = 15 // eval/extract/source 读取快
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
// 避免双份冗余。本描述只讲：用途 + 坐标系 + 自动截图特性 + 禁 run_command 重复包装。
func (a *BrowserUse) Description() string {
	return fmt.Sprintf(
		"用 chromium 真实浏览器操作目标页面。action 字段选具体动作（open/click/input/wait/eval/extract/source），其它字段按 action 要求填（见各字段 description）。"+
			"\n坐标系：%s。视口固定 %dx%d。"+
			"\n状态变化 action（open/click/input/wait）执行完自动附最新截图给 LLM；读取 action 不附图。"+
			"\n本工具是 browser-use CLI 的 typed 包装——**不要**再用 `run_command \"browser-use ...\"` 重复调用。",
		grounding.Describe(a.CoordSystem), a.ViewportW, a.ViewportH,
	)
}

func (a *BrowserUse) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "action":{"type":"string","enum":["open","click","input","wait","eval","extract","source"],"description":"open=导航URL / click=点截图坐标 / input=填表单（含 click 拿焦点+type） / wait=等条件 / eval=跑JS / extract=LLM抽数据 / source=拿HTML"},
    "url":{"type":"string","description":"action=open 必填：目标 URL（含 http:// 或 https://）"},
    "x":{"type":"integer","description":"action=click/input 必填：元素中心 x（坐标系见 Description）"},
    "y":{"type":"integer","description":"action=click/input 必填：元素中心 y"},
    "text":{"type":"string","description":"action=input 必填：要键入的文本"},
    "condition":{"type":"string","description":"action=wait 必填：秒数（如 '3'）或 CSS selector（如 '#main'）或 'networkidle'"},
    "code":{"type":"string","description":"action=eval 必填：JS 代码，最后表达式作为返回值"},
    "query":{"type":"string","description":"action=extract 必填：自然语言描述要抽什么"},
    "timeout_seconds":{"type":"integer","minimum":1,"maximum":180,"description":"硬超时秒；缺省 open=60 / click/input=20 / wait=30 / eval/extract/source=15"}
  },
  "required":["action"]
}`)
}

// Execute 按 action 分流到对应底层 browser-use 子命令。
func (a *BrowserUse) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		Action         string `json:"action"`
		URL            string `json:"url"`
		X              int    `json:"x"`
		Y              int    `json:"y"`
		Text           string `json:"text"`
		Condition      string `json:"condition"`
		Code           string `json:"code"`
		Query          string `json:"query"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 browser_use 参数: %w", err)
	}
	switch in.Action {
	case "open":
		if in.URL == "" {
			return toolfx.Result{}, fmt.Errorf("browser_use open: url 必填且非空")
		}
		if in.TimeoutSeconds == 0 {
			in.TimeoutSeconds = defaultOpenTimeout
		}
		return runBrowserSub(ctx, a.Run, "open", []string{in.URL}, in.TimeoutSeconds)

	case "click":
		realX, realY, err := grounding.ToRealPixels(in.X, in.Y, a.CoordSystem, a.ViewportW, a.ViewportH)
		if err != nil {
			return toolfx.Result{}, fmt.Errorf("browser_use click 坐标换算: %w", err)
		}
		if in.TimeoutSeconds == 0 {
			in.TimeoutSeconds = defaultActionTimeout
		}
		return runBrowserSub(ctx, a.Run, "click", []string{fmt.Sprintf("%d", realX), fmt.Sprintf("%d", realY)}, in.TimeoutSeconds)

	case "input":
		realX, realY, err := grounding.ToRealPixels(in.X, in.Y, a.CoordSystem, a.ViewportW, a.ViewportH)
		if err != nil {
			return toolfx.Result{}, fmt.Errorf("browser_use input 坐标换算: %w", err)
		}
		if in.TimeoutSeconds == 0 {
			in.TimeoutSeconds = defaultActionTimeout
		}
		// 1) click(x, y) 拿焦点——不附图（中间步噪声），err 透传
		if _, err := runBrowserSub(ctx, a.Run, "click", []string{fmt.Sprintf("%d", realX), fmt.Sprintf("%d", realY)}, in.TimeoutSeconds); err != nil {
			return toolfx.Result{}, fmt.Errorf("browser_use input click 拿焦点: %w", err)
		}
		// 2) type <text>——wrapper 自动附图给 LLM 看填充效果
		return runBrowserSub(ctx, a.Run, "type", []string{in.Text}, in.TimeoutSeconds)

	case "wait":
		if in.Condition == "" {
			return toolfx.Result{}, fmt.Errorf("browser_use wait: condition 必填（秒数 / CSS selector / 'networkidle'）")
		}
		if in.TimeoutSeconds == 0 {
			in.TimeoutSeconds = defaultWaitTimeout
		}
		return runBrowserSub(ctx, a.Run, "wait", []string{in.Condition}, in.TimeoutSeconds)

	case "eval":
		if in.Code == "" {
			return toolfx.Result{}, fmt.Errorf("browser_use eval: code 必填")
		}
		if in.TimeoutSeconds == 0 {
			in.TimeoutSeconds = defaultReadTimeout
		}
		return runBrowserSub(ctx, a.Run, "eval", []string{in.Code}, in.TimeoutSeconds)

	case "extract":
		if in.Query == "" {
			return toolfx.Result{}, fmt.Errorf("browser_use extract: query 必填")
		}
		if in.TimeoutSeconds == 0 {
			in.TimeoutSeconds = defaultReadTimeout
		}
		return runBrowserSub(ctx, a.Run, "extract", []string{in.Query}, in.TimeoutSeconds)

	case "source":
		if in.TimeoutSeconds == 0 {
			in.TimeoutSeconds = defaultReadTimeout
		}
		// eval document.documentElement.outerHTML 拿全 HTML
		return runBrowserSub(ctx, a.Run, "eval", []string{"document.documentElement.outerHTML"}, in.TimeoutSeconds)

	default:
		return toolfx.Result{}, fmt.Errorf("browser_use: action 非法 %q（支持 open/click/input/wait/eval/extract/source）", in.Action)
	}
}
