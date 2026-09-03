// Package sandbox 定义主进程 ↔ sandbox 容器的协议类型。
//
// sandbox-server 端实现：见 internal/sandbox/server/
// 主进程端 client：见 client.go / launcher.go（后续任务）
//
// 见 docs/superpowers/specs/2026-05-16-sandbox-server-design.md
package sandbox

// ExecRequest 是 POST /exec 的请求体。
//
// 字段语义：
//   - TaskID：任务 ID，用于 profile 共享（/liusha/<TaskID>/profile/ 存放浏览器登录态）
//   - AgentID：Agent 运行实例 ID，用于工作目录隔离（/liusha/<TaskID>/<AgentID>/workspace/、
//     /liusha/<TaskID>/<AgentID>/output/），防止并发 Agent 文件互串扰；必填，server 端校验空值 400
//   - Command：sh -c 解析的完整命令（支持管道 / 重定向 / $env）
//   - TimeoutSeconds：本次命令硬超时（秒），超时被 SIGKILL
//   - Tag：运维标签（如 "sqlmap-l5"），仅用于日志/诊断，不影响执行
type ExecRequest struct {
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
