// Package external 提供"在沙箱容器里跑外部工具"的通用执行器。
//
// 设计哲学（agentic 路线）：不为每个工具（sqlmap/curl/nuclei/...）写具名 wrapper，
// 而是开放一个 sh -c 沙箱执行器 + 一组工具手册 SKILL（skills/tooling/*）。
// LLM 通过 SKILL 学每个工具用法 → 拼完整命令 → 由 RunCommand 在容器里跑 → 自读 stdout 取信息。
//
// 取舍说明：
//   - 输出非结构化（LLM 自 grep 找 Title:/Payload:/dbms 等关键词）——决策面交给模型
//   - 工具集易扩（加 nuclei/nikto 只需写 skills/tooling/<tool>/SKILL.md，不动 Go）
//   - 容器化隔离（每次扫描独立 --rm 容器，AutoRemove 退出即清理）
//   - 安全边界：DockerRunner 全局并发 sem + 单次 timeout 钳到 [30, 300]，无网络隔离
//     依赖 caller 用 a.Network 限制（如挂在 liusha_scan_net 限定 scope hosts）
package external

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/toolfx"
	"github.com/V3teran/liusha/internal/tools/runners"
)

// 默认沙箱镜像，caller 经 SubBuilderDeps.PentoolsImage 传入时可覆盖。
// 镜像内预装：sqlmap / curl / sh / python3 / jq + ca-certificates。
const defaultSandboxImage = "liusha/pentools:1.0.0"

// 参数边界。
const (
	runMinTimeoutSeconds = 30
	runMaxTimeoutSeconds = 300
	runDefaultTimeout    = 90 * time.Second
	runDefaultMemMB      = 512
	runDefaultCPUs       = 1.0
	// 1.5 KB × 2 + 元数据 ≈ 3.2 KB，刚好压在 ResultCompress 4KB 阈值之下；
	// 让 LLM 能直接拿到完整 tail 而非压缩后的 400B snippet。
	runTailBytes = 1536
)

// tagAllowedChars 限定 tag 字符集（仅 [a-z0-9-]）；不符即丢弃 tag 走默认 "default"，
// 防止容器名注入或过长。
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
// （sqlmap -u ... / curl -X POST ... / python3 -c "..."），然后调 run_command(command=..., tag=...)。
// 输出原文 stdout/stderr 各 1.5 KB tail 给 LLM，由 LLM 自决策是否命中、写 finding。
//
// 与具名 wrapper（已废弃的 escalate_sqlmap）的对比：
//   - wrapper：代码层固定 CLI 形态 + 正则提结构化字段；LLM 不接触 CLI
//   - RunCommand：LLM 拼完整 CLI；输出由 LLM 自读自解析；代码层不假设任何工具
//
// 跨漏洞复用：SQLi / BAC / SSRF / RCE / XSS 等所有 vuln skill builder 都注册它，
// 工具内部不绑定任何漏洞类型。
type RunCommand struct {
	Runner  *runners.DockerRunner
	Image   string // 沙箱镜像；空时用 defaultSandboxImage
	Network string // 默认空（docker bridge），可挂在 scan-only network 限制 scope
}

// Name 返回动作名 "run_command"。
func (a *RunCommand) Name() string { return "run_command" }

// Description 给 LLM 看的简介。
func (a *RunCommand) Description() string {
	return "在沙箱容器里跑一条 shell 命令（sh -c <command>），用于 LLM 自决策的工具调用。" +
		"command 走 sh 解析（支持 |、&&、>、<、$()）；输出 stdout/stderr 各截 1.5KB tail。" +
		"工具用法见 system prompt 内置的工具手册（sqlmap / curl / python3 / sh）。" +
		"tag 可选（小写字母数字短横，长度 ≤ 32），用作容器名后缀方便运维定位。"
}

// ParametersJSON：command 必填；timeout_seconds / tag 可选。
func (a *RunCommand) ParametersJSON() json.RawMessage {
	return json.RawMessage(`{
  "type":"object",
  "properties": {
    "command":{"type":"string","minLength":1,"description":"完整 shell 命令；走 sh -c 解析（可用管道、重定向）。例如：sqlmap -u 'http://x/y?id=1' -p id --batch --level 5"},
    "timeout_seconds":{"type":"integer","minimum":30,"maximum":300,"default":90,"description":"硬超时（秒），钳到 [30,300]"},
    "tag":{"type":"string","pattern":"^[a-z0-9-]{1,32}$","description":"可选运维标签，作容器名后缀（如 'sqlmap-l5'、'curl-blind'）；不传或非法时用 'default'"}
  },
  "required":["command"]
}`)
}

// runCommandOutput 是 toolfx.Result.Output 的 JSON 结构。
//
// 字段最小化：只给 LLM"它写的命令跑出来怎样"——不假设任何工具的输出形态。
// LLM 自己 grep stdout_tail 找 Title:/Payload:/back-end DBMS 等关键词。
type runCommandOutput struct {
	ExitCode   int    `json:"exit_code"`
	TimedOut   bool   `json:"timed_out"`
	StdoutTail string `json:"stdout_tail,omitempty"`
	StderrTail string `json:"stderr_tail,omitempty"`
}

// Execute 钳超时 → 校验 tag → docker run sh -c <command> → 截 tail → 返结构化结果。
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
	if a.Runner == nil {
		return toolfx.Result{}, fmt.Errorf("run_command: Runner 未注入")
	}

	timeout := runDefaultTimeout
	if in.Timeout > 0 {
		secs := in.Timeout
		if secs < runMinTimeoutSeconds {
			secs = runMinTimeoutSeconds
		}
		if secs > runMaxTimeoutSeconds {
			secs = runMaxTimeoutSeconds
		}
		timeout = time.Duration(secs) * time.Second
	}

	image := a.Image
	if image == "" {
		image = defaultSandboxImage
	}

	tag := sanitizeTag(in.Tag)

	// 容器名：liusha-shell-<tag>-<hex>。"shell" 表"sh -c 沙箱"，与未来可能的具名
	// wrapper（如 liusha-burp-<hex>）形成对比；tag 让 docker ps 一眼区分 LLM 跑的是啥。
	spec := runners.RunSpec{
		Image:         image,
		ContainerName: "liusha-shell-" + tag + "-" + randSandboxHex(),
		Network:       a.Network,
		AutoRemove:    true,
		Timeout:       timeout,
		MemLimit:      int64(runDefaultMemMB) * 1024 * 1024,
		CPULimit:      runDefaultCPUs,
		Cmd:           []string{"sh", "-c", in.Command},
	}

	res, err := a.Runner.RunAndWait(ctx, spec)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("docker run shell tag=%s: %w", tag, err)
	}

	out := runCommandOutput{
		ExitCode:   res.ExitCode,
		TimedOut:   res.TimedOut,
		StdoutTail: tailString(res.Stdout, runTailBytes),
		StderrTail: tailString(res.Stderr, runTailBytes),
	}
	enc, err := json.Marshal(out)
	if err != nil {
		return toolfx.Result{}, fmt.Errorf("marshal output: %w", err)
	}
	summary := fmt.Sprintf("run_command tag=%s exit=%d timeout=%v", tag, out.ExitCode, out.TimedOut)
	return toolfx.Result{Output: enc, Summary: summary}, nil
}

// sanitizeTag 把 LLM 传入的 tag 收紧到 [a-z0-9-]{1,32}；为空 / 含非法字符 → "default"。
// 防止容器名注入或过长。
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
// 改名避免与未来其他 helper 冲突。
func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// randSandboxHex 返回 8 位 hex 随机串，用作容器名唯一后缀。
// 失败兜底用 nano timestamp（极不可能命中，但避免 panic）。
func randSandboxHex() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b[:])
}
