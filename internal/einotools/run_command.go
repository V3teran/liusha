package einotools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/V3teran/liusha/internal/sandbox"
)

// run_command 是 P3b 唯一非 InferTool 工具：它要回灌图片（截图等）给多模态 LLM，
// eino 的 InvokableTool 只返 string（无图片通道），故实现 EnhancedInvokableTool ——
// 返 *schema.ToolResult{Parts}，原生携带 text + image part（ToolsNode 优先用增强接口）。
// 这替代旧 toolfx.Result{Output, Images} + react.runtime 手工拼 multimodal 的路径，
// 也顺带丢掉 internal/llm.ImageContent 依赖（eino 全面迁移目标之一）。

const (
	fallbackRunTailBytes  = 8192 // caller 未注入 TailBytes 时的默认（与 yaml run_tail_bytes 同步）
	defaultMaxTimeoutSecs = 1800 // caller 未注入 MaxTimeoutSeconds 时的兜底上限
	maxTagLen             = 32
)

// SandboxExecutor 是 run_command 依赖的最小接口（sandbox.Client 自动满足）。
type SandboxExecutor interface {
	Exec(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error)
}

// runCommandTool 实现 tool.BaseTool + tool.EnhancedInvokableTool。
// sandbox/hunterID 闭包式注入（非 LLM 参数），与旧 Action 语义一致。
type runCommandTool struct {
	executor          SandboxExecutor
	hunterID          string
	maxTimeoutSeconds int
	tailBytes         int
}

// BuildRunCommand 造原生 eino run_command 工具。
func BuildRunCommand(executor SandboxExecutor, hunterID string, maxTimeoutSeconds, tailBytes int) (tool.BaseTool, error) {
	if executor == nil {
		return nil, fmt.Errorf("run_command: Sandbox 未注入")
	}
	if hunterID == "" {
		return nil, fmt.Errorf("run_command: HunterID 未注入")
	}
	return &runCommandTool{
		executor:          executor,
		hunterID:          hunterID,
		maxTimeoutSeconds: maxTimeoutSeconds,
		tailBytes:         tailBytes,
	}, nil
}

func (t *runCommandTool) effectiveTailBytes() int {
	if t.tailBytes > 0 {
		return t.tailBytes
	}
	return fallbackRunTailBytes
}

func (t *runCommandTool) maxTimeout() int {
	if t.maxTimeoutSeconds > 0 {
		return t.maxTimeoutSeconds
	}
	return defaultMaxTimeoutSecs
}

// Info 手工构造 schema（timeout 上限是运行期动态值，故不走 InferTool 静态推断）。
func (t *runCommandTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	maxSec := t.maxTimeout()
	desc := "在沙箱容器里跑一条 shell 命令（sh -c <command>），用于 LLM 自决策的工具调用。" +
		"command 走 sh 解析（支持 |、&&、>、<、$()）；输出 stdout/stderr 各截 ~8KB tail。" +
		"沙箱预装工具集见 user prompt 的『可用外部工具索引』；详细手册按需调 `read_tooling_skill` 拉取。" +
		"tag 必填，用作运维诊断标签（仅 [a-z0-9-]，超 32 或含非法字符会归为 default）。" +
		"环境变量 $OUTPUT_DIR：写到 $OUTPUT_DIR/xxx 的二进制/大文件会作为附件返回" +
		"（上限 200KB/文件, 1MB 总量, 5 文件）；图片附件直接作为多模态 image 回传。" +
		"文本类输出直接走 stdout 即可，不要重复写文件。" +
		"典型用法：浏览器截图 `browser-use screenshot $OUTPUT_DIR/shot.png`；下载 `wget -O $OUTPUT_DIR/x.bin URL`。"

	return &schema.ToolInfo{
		Name: "run_command",
		Desc: desc,
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"command": {
				Type:     schema.String,
				Required: true,
				Desc:     "完整 shell 命令；走 sh -c 解析（可用管道、重定向、$env）。例如：sqlmap -u 'http://x/y?id=1' -p id --batch --level 5；或 browser-use open https://x.com",
			},
			"timeout_seconds": {
				Type:     schema.Integer,
				Required: true,
				Desc:     fmt.Sprintf("本次命令硬超时（秒），1..%d。短命令(curl/cat) 15s 够；中等(nuclei 轻扫) 60-180s；长跑(sqlmap/hydra) 300-900s。过短会被 timed_out 终止，过长钳到上限 %ds", maxSec, maxSec),
			},
			"tag": {
				Type:     schema.String,
				Required: true,
				Desc:     "运维标签（如 sqlmap-l5、browser-nav），仅 [a-z0-9-]{1,32}，仅用于诊断",
			},
		}),
	}, nil
}

// InvokableRun 增强接口：解析参数 → 钳 timeout → sandbox.Exec → 截 tail + 抽图 → *ToolResult。
func (t *runCommandTool) InvokableRun(ctx context.Context, arg *schema.ToolArgument, _ ...tool.Option) (*schema.ToolResult, error) {
	var in struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout_seconds"`
		Tag     string `json:"tag"`
	}
	if arg != nil && arg.Text != "" {
		if err := json.Unmarshal([]byte(arg.Text), &in); err != nil {
			return nil, fmt.Errorf("解析 run_command 参数失败: %w", err)
		}
	}
	if strings.TrimSpace(in.Command) == "" {
		return nil, fmt.Errorf("command 必填且非空")
	}
	if in.Timeout <= 0 {
		return nil, fmt.Errorf("timeout_seconds 必填且 > 0（每个工具合理 timeout 差异大，无统一 default）")
	}
	if mx := t.maxTimeout(); in.Timeout > mx {
		in.Timeout = mx
	}
	tag := sanitizeTag(in.Tag)

	res, err := t.executor.Exec(ctx, sandbox.ExecRequest{
		HunterID:       t.hunterID,
		Command:        in.Command,
		TimeoutSeconds: in.Timeout,
		Tag:            tag,
	})
	if err != nil {
		return nil, fmt.Errorf("sandbox exec tag=%s: %w", tag, err)
	}

	tailN := t.effectiveTailBytes()
	textOut := runCommandOutput{
		ExitCode:   res.ExitCode,
		TimedOut:   res.TimedOut,
		StdoutTail: clampMiddle(res.Stdout, tailN),
		StderrTail: clampMiddle(res.Stderr, tailN),
		Files:      toFileMetas(res.Files),
		Warnings:   res.Warnings,
	}
	enc, err := json.Marshal(textOut)
	if err != nil {
		return nil, fmt.Errorf("marshal output: %w", err)
	}

	parts := []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: string(enc)}}
	parts = append(parts, imagePartsFromFiles(res.Files)...)
	return &schema.ToolResult{Parts: parts}, nil
}

// fileMeta 是文本输出里的轻量附件元信息（不含 b64；图片真内容走 image part）。
type fileMeta struct {
	Name  string `json:"name"`
	Bytes int    `json:"bytes,omitempty"`
	Image bool   `json:"image,omitempty"`
}

// runCommandOutput 是 text part 的 JSON 结构（LLM 自 grep stdout_tail 找关键词）。
type runCommandOutput struct {
	ExitCode   int        `json:"exit_code"`
	TimedOut   bool       `json:"timed_out"`
	StdoutTail string     `json:"stdout_tail,omitempty"`
	StderrTail string     `json:"stderr_tail,omitempty"`
	Files      []fileMeta `json:"files,omitempty"`
	Warnings   []string   `json:"warnings,omitempty"`
}

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

// imagePartsFromFiles 把图片附件转成 eino ToolOutputPart（base64 + mime），其余文件不进多模态。
func imagePartsFromFiles(files []sandbox.Attachment) []schema.ToolOutputPart {
	var out []schema.ToolOutputPart
	for _, f := range files {
		mt := imageMediaTypeFromName(f.Name)
		if mt == "" || f.B64 == "" {
			continue
		}
		b64 := f.B64
		out = append(out, schema.ToolOutputPart{
			Type: schema.ToolPartTypeImage,
			Image: &schema.ToolOutputImage{
				MessagePartCommon: schema.MessagePartCommon{Base64Data: &b64, MIMEType: mt},
			},
		})
	}
	return out
}

// imageMediaTypeFromName 按扩展名（大小写不敏感）返回 MIME；非图片返 ""。
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
	if len(s) > maxTagLen {
		s = s[:maxTagLen]
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return "default"
		}
	}
	return s
}

// clampMiddle 把超长输出收窄到「头 + 尾」共 n 字节，中间插省略标记（防撑爆 LLM context）。
// 头略多于尾（3:2）：status line + headers 在顶部，信息密度更高。
func clampMiddle(s string, n int) string {
	if len(s) <= n {
		return s
	}
	head := n * 3 / 5
	tail := n - head
	elided := len(s) - head - tail
	return s[:head] +
		fmt.Sprintf("\n…[run_command 输出截断：中间省略 %d 字节，仅保留首 %d + 尾 %d 字节；"+
			"需完整输出请把命令结果重定向到 $OUTPUT_DIR 文件后分段读]…\n", elided, head, tail) +
		s[len(s)-tail:]
}
