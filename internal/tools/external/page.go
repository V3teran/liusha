// Package external 的 page_* tool 家族：browser-use 子命令的 typed JSON 包装。
//
// 设计动机：
//   - LLM 选 tool 时 typed name（如 `page_click`）比 generic `run_command(command="browser-use click 3")`
//     摩擦小——直接命中意图，无需 2 步推理（先选 run_command 再决定 command 字符串）。
//   - typed args（index/url/text）比 raw shell 字符串错率低，无 quote 转义陷阱。
//   - 状态变化操作（page_open/click/input/wait）执行完，wrapper 自动追加 screenshot 到 $OUTPUT_DIR
//     → 视觉结果自然喂回 vision LLM，无需 LLM 额外调 screenshot 多 1 步 ReAct。
//
// 拆细范围（受 vision agent typed-tool 设计理念启发）：
//   - 拆 7 个 high-value 核心动作（覆盖 95% 漏洞挖掘场景）
//   - 其它低频子命令（scroll/back/keys/hover/select/cookies/python 等）仍走 run_command 兜底
//   - 不暴露独立 page_screenshot tool —— 状态变化自动附图后，screenshot 兜底走 run_command
//     避免 LLM 在 tool 列表看到 page_screenshot 习惯性每次调用造成 token 浪费。
//
// 实现：每个 PageX 持有 *RunCommand 指针复用 sandbox/TaskID/timeout 配置，
// Execute 把 typed args 翻译成 `browser-use <subcmd> <args>` 调 RunCommand.Execute 透传。
package external

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	toolfx "github.com/V3teran/liusha/internal/toolruntime"
)

// runBrowserSub 是 page_* 共用辅助：把 subcmd + 多个 args 拼成 shell 命令调 RunCommand 跑。
//
// shell-quote 每个 arg 防注入（LLM 传的 URL/text 含特殊字符也不会逃逸出 sh -c 上下文）。
// tag 用 "page-<subcmd>" 形式便于运维诊断（如 page-open / page-click）。
func runBrowserSub(ctx context.Context, rc *RunCommand, subcmd string, args []string, timeoutSec int) (toolfx.Result, error) {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellSingleQuote(a)
	}
	cmd := strings.TrimSpace(fmt.Sprintf("browser-use %s %s", subcmd, strings.Join(quoted, " ")))
	payload, err := json.Marshal(map[string]any{
		"command":         cmd,
		"timeout_seconds": timeoutSec,
		"tag":             "page-" + subcmd,
	})
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("page_%s marshal: %w", subcmd, err)
	}
	return rc.Execute(ctx, payload)
}

// shellSingleQuote 把字符串用单引号包裹防 shell 注入；内部 ' 用 '\'' 转义。
// 比 fmt.Sprintf("%q", s) 更稳——后者用双引号会让 $var/反引号触发 sh 解析。
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// 默认 timeout（秒）——caller 不传时各 page_* 用各自合理值。
const (
	defaultOpenTimeout   = 60 // 含 chromium cold start
	defaultActionTimeout = 20 // click/input 等交互动作 daemon 复用快
	defaultWaitTimeout   = 30 // wait 本身就是等
	defaultReadTimeout   = 15 // eval/extract/state 读取快
)

// ===== PageOpen — 导航到 URL =====

// PageOpen 包装 `browser-use open <url>`。
// 状态变化操作（DOM 全量替换+ network）→ wrapper 自动附截图。
// chromium 首次冷启 25-30s，timeout 至少 60；session 复用后 15s 够。
type PageOpen struct {
	Run *RunCommand
}

func (a *PageOpen) Name() string { return "page_open" }

func (a *PageOpen) Description() string {
	return "导航 chromium tab 到指定 URL（每 task 独立 tab，cookies 跨 task 共享 daemon）。" +
		"执行完自动附截图——LLM 直接用 vision 看到新页面渲染态。" +
		"首次 open 冷启 25-30s，timeout_seconds 至少 60；后续复用 15s 够。"
}

func (a *PageOpen) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "url":{"type":"string","minLength":1,"description":"目标 URL（含 scheme/host/path/query），如 http://target/login.php?id=1"},
    "timeout_seconds":{"type":"integer","minimum":1,"maximum":300,"description":"硬超时秒（默认 60，首次冷启场景建议 90+）"}
  },
  "required":["url"]
}`)
}

func (a *PageOpen) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		URL            string `json:"url"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 page_open 参数: %w", err)
	}
	if strings.TrimSpace(in.URL) == "" {
		return toolfx.Result{}, fmt.Errorf("page_open: url 必填且非空")
	}
	if in.TimeoutSeconds == 0 {
		in.TimeoutSeconds = defaultOpenTimeout
	}
	return runBrowserSub(ctx, a.Run, "open", []string{in.URL}, in.TimeoutSeconds)
}

// ===== PageClick — 点击索引指向的元素 =====

// PageClick 包装 `browser-use click <index>`。
// 状态变化（触发 DOM event / 可能 navigation / DOM 重写）→ wrapper 自动附截图。
// index 来自 page_state 返回的 DOM 索引树。
type PageClick struct {
	Run *RunCommand
}

func (a *PageClick) Name() string { return "page_click" }

func (a *PageClick) Description() string {
	return "点击 DOM 索引树指定 index 的元素（先调 page_state 拿索引）。" +
		"执行完自动附截图——LLM 看到点击后的页面变化（弹窗/跳转/DOM 重写）。"
}

func (a *PageClick) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "index":{"type":"integer","minimum":0,"description":"DOM 索引（来自 page_state 返回的 tree）"},
    "timeout_seconds":{"type":"integer","minimum":1,"maximum":120,"description":"硬超时秒（默认 20）"}
  },
  "required":["index"]
}`)
}

func (a *PageClick) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		Index          int `json:"index"`
		TimeoutSeconds int `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 page_click 参数: %w", err)
	}
	if in.Index < 0 {
		return toolfx.Result{}, fmt.Errorf("page_click: index 必须 ≥ 0")
	}
	if in.TimeoutSeconds == 0 {
		in.TimeoutSeconds = defaultActionTimeout
	}
	return runBrowserSub(ctx, a.Run, "click", []string{fmt.Sprintf("%d", in.Index)}, in.TimeoutSeconds)
}

// ===== PageInput — 向元素填值 =====

// PageInput 包装 `browser-use input <index> <text>`。
// 状态变化（触发 input/change event + 可能联动渲染）→ wrapper 自动附截图。
type PageInput struct {
	Run *RunCommand
}

func (a *PageInput) Name() string { return "page_input" }

func (a *PageInput) Description() string {
	return "向 DOM 索引指定的 input/textarea 元素填值（触发 input/change event）。" +
		"执行完自动附截图——可看到表单填充后的页面状态（验证消息/联动渲染）。" +
		"text 走 shell 单引号转义防注入。"
}

func (a *PageInput) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "index":{"type":"integer","minimum":0,"description":"输入框 DOM 索引"},
    "text":{"type":"string","description":"要填的文本（可含 payload，shell 自动转义）"},
    "timeout_seconds":{"type":"integer","minimum":1,"maximum":120,"description":"硬超时秒（默认 20）"}
  },
  "required":["index","text"]
}`)
}

func (a *PageInput) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		Index          int    `json:"index"`
		Text           string `json:"text"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 page_input 参数: %w", err)
	}
	if in.Index < 0 {
		return toolfx.Result{}, fmt.Errorf("page_input: index 必须 ≥ 0")
	}
	if in.TimeoutSeconds == 0 {
		in.TimeoutSeconds = defaultActionTimeout
	}
	return runBrowserSub(ctx, a.Run, "input", []string{fmt.Sprintf("%d", in.Index), in.Text}, in.TimeoutSeconds)
}

// ===== PageWait — 等待异步状态变化 =====

// PageWait 包装 `browser-use wait <condition>`。
// 等待本身不改 state，但等结束后页面可能已被异步 JS 改 → wrapper 自动附截图。
// condition 透传给 browser-use-cli：可以是秒数（"3"）或 CSS selector（"#main"）。
type PageWait struct {
	Run *RunCommand
}

func (a *PageWait) Name() string { return "page_wait" }

func (a *PageWait) Description() string {
	return "等待异步状态变化（秒数或 CSS selector 出现）。" +
		"等结束自动附截图——看到 AJAX/SPA 异步渲染后的真实视觉。" +
		"condition 例：'3'（等 3 秒）/ '#login-form'（等元素出现）/ 'networkidle'（等网络空闲）。"
}

func (a *PageWait) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "condition":{"type":"string","minLength":1,"description":"等待条件：秒数 / CSS selector / 'networkidle' 等"},
    "timeout_seconds":{"type":"integer","minimum":1,"maximum":120,"description":"硬超时秒（默认 30）"}
  },
  "required":["condition"]
}`)
}

func (a *PageWait) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		Condition      string `json:"condition"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 page_wait 参数: %w", err)
	}
	if strings.TrimSpace(in.Condition) == "" {
		return toolfx.Result{}, fmt.Errorf("page_wait: condition 必填")
	}
	if in.TimeoutSeconds == 0 {
		in.TimeoutSeconds = defaultWaitTimeout
	}
	return runBrowserSub(ctx, a.Run, "wait", []string{in.Condition}, in.TimeoutSeconds)
}

// ===== PageEval — 执行 JS 返回末值 =====

// PageEval 包装 `browser-use eval <js_code>`。
// 读取为主（也可写入，由 LLM 决定）→ 不附图（避免 token 浪费）。
// LLM 想看视觉变化时显式调 run_command 跑 `browser-use screenshot ...` 兜底。
type PageEval struct {
	Run *RunCommand
}

func (a *PageEval) Name() string { return "page_eval" }

func (a *PageEval) Description() string {
	return "在当前 tab 页面 context 执行 JavaScript，返回末值。" +
		"读取为主（document.title / window.alert hook 等）；不附截图。" +
		"如代码改了 DOM 想看视觉变化，下一步用 page_click/page_open 触发状态变化即自动附图。"
}

func (a *PageEval) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "code":{"type":"string","minLength":1,"description":"完整 JS 代码（页面 context 执行）。例：'document.title' / 'document.cookie' / Array.from(document.querySelectorAll(\"a\")).map(x=>x.href)"},
    "timeout_seconds":{"type":"integer","minimum":1,"maximum":120,"description":"硬超时秒（默认 15）"}
  },
  "required":["code"]
}`)
}

func (a *PageEval) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		Code           string `json:"code"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 page_eval 参数: %w", err)
	}
	if strings.TrimSpace(in.Code) == "" {
		return toolfx.Result{}, fmt.Errorf("page_eval: code 必填")
	}
	if in.TimeoutSeconds == 0 {
		in.TimeoutSeconds = defaultReadTimeout
	}
	return runBrowserSub(ctx, a.Run, "eval", []string{in.Code}, in.TimeoutSeconds)
}

// ===== PageExtract — 从当前页面抽取结构化数据 =====

// PageExtract 包装 `browser-use extract <query>`。
// 读取（LLM 抽数据，不触发 DOM event）→ 不附图。
type PageExtract struct {
	Run *RunCommand
}

func (a *PageExtract) Name() string { return "page_extract" }

func (a *PageExtract) Description() string {
	return "从当前页面 DOM 抽取结构化数据（browser-use extract 包装）。" +
		"query 是自然语言描述要抽的字段（如 'all links with title'、'price of each product'）。" +
		"不附截图——返结果已是 LLM 可消费的结构化文本。"
}

func (a *PageExtract) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "query":{"type":"string","minLength":1,"description":"抽取目标的自然语言描述"},
    "timeout_seconds":{"type":"integer","minimum":1,"maximum":120,"description":"硬超时秒（默认 15）"}
  },
  "required":["query"]
}`)
}

func (a *PageExtract) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		Query          string `json:"query"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 page_extract 参数: %w", err)
	}
	if strings.TrimSpace(in.Query) == "" {
		return toolfx.Result{}, fmt.Errorf("page_extract: query 必填")
	}
	if in.TimeoutSeconds == 0 {
		in.TimeoutSeconds = defaultReadTimeout
	}
	return runBrowserSub(ctx, a.Run, "extract", []string{in.Query}, in.TimeoutSeconds)
}

// ===== PageState — 获取 DOM 索引树 =====

// PageState 包装 `browser-use state`（无参）。
// 返当前 tab 的 DOM 索引树文本（含每个可交互元素的 index/tag/text）→ 不附图。
// LLM 拿到 index 后调 page_click / page_input。
type PageState struct {
	Run *RunCommand
}

func (a *PageState) Name() string { return "page_state" }

func (a *PageState) Description() string {
	return "获取当前 tab 的 DOM 索引树（可交互元素的 index + tag + text 等）。" +
		"用于后续 page_click / page_input 定位 index。不附截图（已有结构化 DOM 文本）。"
}

func (a *PageState) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties":{
    "timeout_seconds":{"type":"integer","minimum":1,"maximum":60,"description":"硬超时秒（默认 15）"}
  }
}`)
}

func (a *PageState) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		TimeoutSeconds int `json:"timeout_seconds"`
	}
	// args 可能为 "{}" 或空——忽略解析错误，按 default 跑
	_ = json.Unmarshal(args, &in)
	if in.TimeoutSeconds == 0 {
		in.TimeoutSeconds = defaultReadTimeout
	}
	return runBrowserSub(ctx, a.Run, "state", nil, in.TimeoutSeconds)
}
