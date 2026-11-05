// Package sandbox 定义主进程 ↔ sandbox 容器的协议类型。
//
// sandbox-server 端实现：见 internal/sandbox/server/
// 主进程端 client：见 client.go / launcher.go（后续任务）
package sandbox

// ExecRequest 是 POST /exec 的请求体。
//
// 字段语义：
//   - AssignmentID：批次 ID，工作区一级分组（/liusha/<AssignmentID>/<TaskID>/）；必填
//   - TaskID：任务 ID，二级分组——会话状态（state/，浏览器登录态与 $HOME 落盘）、
//     工作目录（work/）与产物（out/）都在其下
//   - AgentID：Agent 运行实例 ID，随请求透传（进程树标注/审计），不参与目录分层；必填，server 端校验空值 400
//   - Command：sh -c 解析的完整命令（支持管道 / 重定向 / $env）
//   - TimeoutSeconds：本次命令硬超时（秒），超时被 SIGKILL
//   - Tag：运维标签（如 "sqlmap-l5"），仅用于日志/诊断，不影响执行
type ExecRequest struct {
	AssignmentID   string             `json:"assignment_id"`   // 批次 ID（工作区一级分组，必填）
	TaskID         string `json:"task_id"`
	AgentID        string `json:"agent_id"`
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Tag            string `json:"tag,omitempty"`
}

// ExecResult 是 POST /exec 的响应体。
//
// stdout/stderr 由 sandbox-server 完整返回不截尾——截尾在主进程 RunCommand 层做
// （沿用现有 tailString 逻辑，LLM context 管理贴近主进程更直接）。
type ExecResult struct {
	ExitCode int          `json:"exit_code"`
	Stdout   string       `json:"stdout"`
	Stderr   string       `json:"stderr"`
	TimedOut bool         `json:"timed_out"`
	Files    []Attachment `json:"files,omitempty"`
	Warnings []string     `json:"warnings,omitempty"`
}

// Attachment 是命令产物（写到 $OUTPUT_DIR 的文件）的 b64 编码。
//
// 只含 Name（不带容器内路径）+ B64 内容——容器内路径对主进程和 LLM 无意义。
type Attachment struct {
	Name string `json:"name"`
	B64  string `json:"b64"`
}
