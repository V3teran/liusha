// Package external 提供"在沙箱容器里跑外部工具"的通用执行器。
//
// 设计哲学（agentic 路线）：不为每个工具（sqlmap/curl/nuclei/...）写具名 wrapper，
// 而是开放一个 sh -c 沙箱执行器 + 一组工具手册 SKILL（skills/tooling/*）。
// LLM 通过 SKILL 学每个工具用法 → 拼完整命令 → 由 RunCommand 在容器里跑 → 自读 stdout 取信息。
//
// 取舍说明：
//   - 输出非结构化（LLM 自 grep 找 Title:/Payload:/dbms 等关键词）——决策面交给模型
//   - 工具集易扩（加 nuclei/nikto 只需写 skills/tooling/<tool>/SKILL.md，不动 Go）
//   - 容器化隔离：通过 sandbox-server HTTP RPC 跑命令——每个 agent run 一个独立长会话容器
//     （由 internal/sandbox.Launcher 管理生命周期），单次 /exec 内部独立 OUTPUT_DIR 临时目录
//   - 安全边界：sandbox-server 端单工具 timeout 钳到 MaxTimeoutSeconds；附件上限
//     200KB/file, 1MB 总, 5 文件（避免 LLM context 爆炸）
//
// 见 docs/superpowers/specs/2026-05-16-sandbox-server-design.md
package external

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/sandbox"
	toolfx "github.com/V3teran/liusha/internal/toolruntime"
)

// fallbackRunTailBytes 是 caller 未通过 TailBytes 注入时的默认值——
// 8 KB × 2 + 元数据 ≈ 17 KB，让 sqlmap level=5+tamper 等长输出的 Title/Payload
// 关键字段能完整保留。与 yaml sandbox.run_tail_bytes 同步。
const fallbackRunTailBytes = 8192

// tagAllowedChars 限定 tag 字符集（仅 [a-z0-9-]）；不符即丢弃 tag 走默认 "default"，
// 防止容器内进程命名注入或过长。
var tagAllowedChars = func() map[byte]bool {
	m := make(map[byte]bool, 64)
	for c := byte('a'); c <= 'z'; c++ {
		m[c] = true
	}
	for c := byte('0'); c <= '9'; c++ {
		m[c] = true
	}
	m['-'] = true
	return m
}()

// RunCommand 是跨漏洞类型共享的容器化命令执行器。
//
// 设计意图：LLM 看 skills/tooling/<tool>/SKILL.md 学到工具用法，自己拼 shell 命令
// （sqlmap -u ... / curl -X POST ... / browser-use open ... / python3 -c "..."），
// 然后调 run_command(command=..., tag=...)。
// 输出原文 stdout/stderr 各 TailBytes tail + 附件（写到 $OUTPUT_DIR 的文件）给 LLM。
//
// 与具名 wrapper（已废弃的 escalate_sqlmap）的对比：
//   - wrapper：代码层固定 CLI 形态 + 正则提结构化字段；LLM 不接触 CLI
//   - RunCommand：LLM 拼完整 CLI；输出由 LLM 自读自解析；代码层不假设任何工具
//
// 跨漏洞复用：SQLi / BAC / SSRF / RCE / XSS 等所有 vuln skill builder 都注册它，
// 工具内部不绑定任何漏洞类型。浏览器交互通过 `browser-use ...` CLI 命令调用，
// 不另起具名工具。
type RunCommand struct {
	// Sandbox 是当前 agent run 绑定的 sandbox-server HTTP RPC client。
	// 由 hunter Builder 闭包从 skill.BuilderParams.Sandbox 注入。
	Sandbox sandbox.Client

	// TaskID 是本次 agent_run 的 id（必填，builder 从 BuilderParams.TaskID 注入）。
	// 透传到 ExecRequest.TaskID 让 sandbox-server 按 task 切 cwd / OUTPUT_DIR
	// 防 subtask swarm commander / striker 共享容器时的文件互串扰。
	TaskID string

	// MaxTimeoutSeconds 是 LLM 传入 timeout_seconds 的钳上限（秒）；
	// 正常路径由 cmd/scanner 注入 cfg.Toolruntime.StepToolTimeoutSeconds（1800）。
	// LLM 传更大值时直接钳到 MaxTimeoutSeconds。
	MaxTimeoutSeconds int

	// TailBytes 是 stdout/stderr 截尾字节数（零值即 fallbackRunTailBytes）。
	// 截尾在主进程层做，sandbox-server 返回完整 stdout——LLM context 管理贴近主进程更直接。
	TailBytes int
}

func (a *RunCommand) effectiveTailBytes() int {
	if a.TailBytes > 0 {
		return a.TailBytes
	}
	return fallbackRunTailBytes
}

// Name 返回工具名 "run_command"。
func (a *RunCommand) Name() string { return "run_command" }

func (a *RunCommand) Description() string {
	return "在沙箱容器里跑一条 shell 命令（sh -c <command>），用于 LLM 自决策的工具调用。" +
		"command 走 sh 解析（支持 |、&&、>、<、$()）；输出 stdout/stderr 各截 ~8KB tail。" +
		"工具用法见 system prompt 内置的工具手册（sqlmap / curl / nuclei / browser-use / python3 / sh 等）。" +
		"tag 必填（小写字母数字短横，长度 ≤ 32），用作运维诊断标签。" +
		"环境变量 $OUTPUT_DIR：写到 $OUTPUT_DIR/xxx 的二进制/大文件会作为 base64 附件返回" +
		"（上限 200KB/文件, 1MB 总量, 5 文件）。文本类输出直接走 stdout 即可，不要重复写文件。" +
		"典型用法：浏览器截图 `browser-use screenshot $OUTPUT_DIR/shot.png`；" +
		"抓包 `tcpdump -w $OUTPUT_DIR/x.pcap`；下载 `wget -O $OUTPUT_DIR/x.bin URL`。"
}

// ParametersJSON：command / timeout_seconds / tag 均必填。
//
// timeout_seconds 必传：每个工具的合理超时差异大（curl 15s vs sqlmap 600s），
// 没有一个 default 能适配所有场景。LLM 必须根据 command 自决合理值。
// 上限 = MaxTimeoutSeconds（从 cfg.Toolruntime.StepToolTimeoutSeconds 注入，1800s）。
// 超上限 → Execute 钳到 MaxTimeoutSeconds；< 1 → Execute 报错。
func (a *RunCommand) ParametersJSON() json.RawMessage {
	maxSec := a.MaxTimeoutSeconds
	if maxSec <= 0 {
		maxSec = 1800 // 兜底：caller 未注入时仍可工作
	}
	return json.RawMessage(fmt.Sprintf(`{
  "type":"object",
  "properties": {
    "command":{"type":"string","minLength":1,"description":"完整 shell 命令；走 sh -c 解析（可用管道、重定向、$env）。例如：sqlmap -u 'http://x/y?id=1' -p id --batch --level 5；或 browser-use open https://x.com"},
    "timeout_seconds":{"type":"integer","minimum":1,"maximum":%d,"description":"本次命令硬超时（秒）。短命令(curl/cat/echo) 15s 够；中等(httpx/nuclei 轻扫) 60-180s；长跑(sqlmap/hydra) 300-900s；上限 %ds。错传过短会被 timed_out 终止，过长会被钳到上限"},
    "tag":{"type":"string","pattern":"^[a-z0-9-]{1,32}$","description":"运维标签（如 'sqlmap-l5'、'browser-nav'），仅用于诊断"}
  },
  "required":["command","timeout_seconds","tag"]
}`, maxSec, maxSec))
}

// fileMeta 是 runCommandOutput.Files 的轻量元信息——只 name + 原始字节数 + 是否图。
//
// 关键：不含 b64！图片真 base64 仅走 toolfx.Result.Images → react.runtime 拼 ContentParts
// 的 image_url block 进 multimodal message。文本里只列 name/bytes 让 LLM 知道有这个产物，
// 避免 b64 文本在 Output JSON 里重复消化（与 strix `[Image data extracted - see attached]` 同款）。
type fileMeta struct {
	Name  string `json:"name"`
	Bytes int    `json:"bytes,omitempty"`
	Image bool   `json:"image,omitempty"` // true → 已作为 multimodal image_url 附件回传给 LLM
}

// runCommandOutput 是 toolfx.Result.Output 的 JSON 结构。
//
// 字段最小化：只给 LLM"它写的命令跑出来怎样"——不假设任何工具的输出形态。
// LLM 自己 grep stdout_tail 找 Title:/Payload:/back-end DBMS 等关键词；
// 二进制产物（截图等）在 files 数组里只列元信息，真内容图走 image_url 文本走 stdout。
type runCommandOutput struct {
	ExitCode   int        `json:"exit_code"`
	TimedOut   bool       `json:"timed_out"`
	StdoutTail string     `json:"stdout_tail,omitempty"`
	StderrTail string     `json:"stderr_tail,omitempty"`
	Files      []fileMeta `json:"files,omitempty"`
	Warnings   []string   `json:"warnings,omitempty"`
}

// toFileMetas 把 sandbox 附件转成轻量元信息——剥离 b64，按扩展名标记 image。
// b64 原大小约 = len(b64) * 3 / 4（精确点要扣 padding，估算够 LLM 决策用）。
func toFileMetas(files []sandbox.Attachment) []fileMeta {
	if len(files) == 0 {
		return nil
	}
	out := make([]fileMeta, len(files))
	for i, f := range files {
		out[i] = fileMeta{
			Name:  f.Name,
			Bytes: len(f.B64) * 3 / 4,
			Image: imageMediaTypeFromName(f.Name) != "",
		}
	}
	return out
}

// Execute 校验参数 → 钳 timeout → 调 Sandbox.Exec → 截 tail → 返结构化结果。
func (a *RunCommand) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
	var in struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout_seconds"`
		Tag     string `json:"tag"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return toolfx.Result{}, fmt.Errorf("解析 run_command 参数失败: %w", err)
	}
	if strings.TrimSpace(in.Command) == "" {
		return toolfx.Result{}, fmt.Errorf("command 必填且非空")
	}
	if a.Sandbox == nil {
		return toolfx.Result{}, fmt.Errorf("run_command: Sandbox 未注入")
	}
	if a.TaskID == "" {
		return toolfx.Result{}, fmt.Errorf("run_command: TaskID 未注入（builder 装配缺漏）")
	}
	if in.Timeout <= 0 {
		return toolfx.Result{}, fmt.Errorf("timeout_seconds 必传且 > 0（每个工具合理 timeout 差异大，无统一 default）")
	}
	if a.MaxTimeoutSeconds > 0 && in.Timeout > a.MaxTimeoutSeconds {
		in.Timeout = a.MaxTimeoutSeconds
	}

	tag := sanitizeTag(in.Tag)

	// HTTP 同步调用——sandbox-server 端用 r.Context() 接 ctx，超时由 sandbox-server
	// 内部 exec.CommandContext 钳到 in.Timeout，主进程层 ctx 只是兜底（如 agent abort）。
	res, err := a.Sandbox.Exec(ctx, sandbox.ExecRequest{
		TaskID:         a.TaskID,
		Command:        in.Command,
		TimeoutSeconds: in.Timeout,
		Tag:            tag,
	})
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("sandbox exec tag=%s: %w", tag, err)
	}

	tailN := a.effectiveTailBytes()
	out := runCommandOutput{
		ExitCode:   res.ExitCode,
		TimedOut:   res.TimedOut,
		StdoutTail: tailString(res.Stdout, tailN),
		StderrTail: tailString(res.Stderr, tailN),
		Files:      toFileMetas(res.Files),
		Warnings:   res.Warnings,
	}
	enc, err := json.Marshal(out)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("marshal output: %w", err)
	}
	summary := fmt.Sprintf("run_command tag=%s exit=%d timed_out=%v files=%d",
		tag, out.ExitCode, out.TimedOut, len(out.Files))
	// 自动从附件提取图片转成 llm.ImageContent——sandbox-server 已经把 $OUTPUT_DIR/* base64 化，
	// 这里只做扩展名识别 + MediaType 推断。Result.Images 非空 → react.runtime 走 multimodal 路径。
	// 非图片附件继续走 Output.files（让 LLM 在文本里看到附件清单）。
	return toolfx.Result{Output: enc, Summary: summary, Images: extractImagesFromFiles(res.Files)}, nil
}

// extractImagesFromFiles 从 sandbox 附件中筛出图片文件（按扩展名），转成 llm.ImageContent。
// 仅识别 png/jpg/jpeg/gif/webp 五种 — 与 Anthropic Base64ImageSourceMediaType 支持集对齐。
func extractImagesFromFiles(files []sandbox.Attachment) []llm.ImageContent {
	if len(files) == 0 {
		return nil
	}
	var out []llm.ImageContent
	for _, f := range files {
		mt := imageMediaTypeFromName(f.Name)
		if mt == "" || f.B64 == "" {
			continue
		}
		out = append(out, llm.ImageContent{
			MediaType:  mt,
			Base64Data: f.B64,
		})
	}
	return out
}

// imageMediaTypeFromName 按扩展名（大小写不敏感）返回 MIME；非图片扩展返 ""。
func imageMediaTypeFromName(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return ""
}

// sanitizeTag 把 LLM 传入的 tag 收紧到 [a-z0-9-]{1,32}；为空 / 含非法字符 → "default"。
func sanitizeTag(s string) string {
	if s == "" {
		return "default"
	}
	if len(s) > 32 {
		s = s[:32]
	}
	for i := 0; i < len(s); i++ {
		if !tagAllowedChars[s[i]] {
			return "default"
		}
	}
	return s
}

// tailString 返回 s 末尾 n 字符（防止超长输出撑爆 LLM context）。
func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
