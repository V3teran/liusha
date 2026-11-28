// Package sandbox 是 runner / api 与沙盒服务（liusha sandbox）之间的协议：请求响应类型与 HTTP 客户端。
//
// 沙盒服务端实现见 internal/sandbox/server。除 GET /healthz 外，全部接口要求
// Authorization: Bearer <sandbox.token>。
package sandbox

// TrafficIngestPath 是 runner 接收沙盒浏览器回灌流量的路由（反方向的协议：沙盒带令牌调用
// runner）。runner 注册它，沙盒以 runner.url 加它拼出回灌地址。
const TrafficIngestPath = "/internal/v1/traffic/ingest"

// ExecRequest 是 POST /exec 的请求体。
//
// 字段语义：
//   - AssignmentID：批次 ID，工作区一级目录（<workspace>/<AssignmentID>/<TaskID>/）；必填
//   - TaskID：任务 ID，工作区二级目录，也是进程归属与按任务释放的单位；必填
//   - AgentRunID：本次 run 的 ID，注入命令环境（LIUSHA_AGENT_RUN_ID）：命令产生的回灌流量据此
//     归属到 run；不参与目录分层；必填
//   - Command：sh -c 解析的完整命令（支持管道 / 重定向 / $env）
//   - TimeoutSeconds：本次命令硬超时（秒），超时连同进程组一起被杀
//   - Tag：运维标签（如 "sqlmap-l5"），仅用于日志 / 诊断，不影响执行
//   - SaveOutputOverBytes：落盘阈值，见字段说明
type ExecRequest struct {
	AssignmentID   string `json:"assignment_id"`
	TaskID         string `json:"task_id"`
	AgentRunID     string `json:"agent_run_id"`
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Tag            string `json:"tag,omitempty"`
	// SaveOutputOverBytes 是落盘阈值：stdout 与 stderr 合计超过这个字节数时，两个流的完整内容落盘到
	// 任务工作区的 console/ 目录，路径随响应给出——调用方只把输出的一段交给模型，被省掉的部分
	// 由此留档，命令可以分段读它。输出本身照常返回，沙盒不按它裁剪；它应当远小于沙盒的响应上限
	// （否则超过上限的那部分开头不在文件里）。0 = 从不落盘。
	SaveOutputOverBytes int `json:"save_output_over_bytes,omitempty"`
}

// ExecResult 是 POST /exec 的响应体。
//
// stdout / stderr 原样返回，沙盒不按调用方的需要裁剪（上下文的管理贴近调用方）；只有输出大到
// 异常、超过沙盒的响应上限时才只带末尾。完整字节数随响应给出；StdoutFile / StderrFile 是完整
// 输出的落盘路径（交给 agent 的形式），只在请求带了落盘阈值、输出合计确实超过它、且这个流有
// 内容时才有。
type ExecResult struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	// StdoutBytes / StderrBytes 是两个流的完整字节数。
	StdoutBytes int64 `json:"stdout_bytes"`
	StderrBytes int64 `json:"stderr_bytes"`
	// StdoutFile / StderrFile 是完整输出的落盘路径；没有落盘时为空。
	StdoutFile string       `json:"stdout_file,omitempty"`
	StderrFile string       `json:"stderr_file,omitempty"`
	TimedOut   bool         `json:"timed_out"`
	Files      []Attachment `json:"files,omitempty"`
	Warnings   []string     `json:"warnings,omitempty"`
}

// Attachment 是命令产物（写到 $OUTPUT_DIR 的文件）的 base64 编码。
// 只含文件名不含沙盒内路径——沙盒内路径对调用方和 LLM 无意义。
type Attachment struct {
	Name string `json:"name"`
	B64  string `json:"b64"`
}

// Inventory 是 GET /tools 的响应：沙盒实际具备的执行能力。
type Inventory struct {
	// Tools 是工具清单中每个 CLI 工具是否已安装（在沙盒的 PATH 中可执行）。
	Tools map[string]bool `json:"tools"`
	// Browser 是浏览器能力（browser_drive 依赖）。
	Browser BrowserStatus `json:"browser"`
}

// BrowserStatus 是浏览器能力的探测结果。
type BrowserStatus struct {
	Installed bool `json:"installed"`
	// Detail 在未安装时说明缺什么。
	Detail string `json:"detail,omitempty"`
}

// ReleaseResult 是 POST /tasks/{id}/release 的响应：本次释放清理掉的资源数。
type ReleaseResult struct {
	Processes int `json:"processes"` // 结束的进程数
	Browsers  int `json:"browsers"`  // 关闭的浏览器会话数
}
