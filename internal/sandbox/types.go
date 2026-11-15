// Package sandbox 是 runner / api 与沙盒服务（liusha sandbox）之间的协议：请求响应类型与 HTTP 客户端。
//
// 沙盒服务端实现见 internal/sandbox/server。除 GET /healthz 外，全部接口要求
// Authorization: Bearer <sandbox.token>。
package sandbox

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
type ExecRequest struct {
	AssignmentID   string `json:"assignment_id"`
	TaskID         string `json:"task_id"`
	AgentRunID     string `json:"agent_run_id"`
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Tag            string `json:"tag,omitempty"`
}

// ExecResult 是 POST /exec 的响应体。
//
// stdout/stderr 完整返回不截尾——截尾在 run_command 工具层做（LLM 上下文管理贴近调用方）。
type ExecResult struct {
	ExitCode int          `json:"exit_code"`
	Stdout   string       `json:"stdout"`
	Stderr   string       `json:"stderr"`
	TimedOut bool         `json:"timed_out"`
	Files    []Attachment `json:"files,omitempty"`
	Warnings []string     `json:"warnings,omitempty"`
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
	// Browser 是浏览器能力（drive_browser 依赖）。
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
