package sandbox

import "path/filepath"

// AgentPath 把本机路径转成交给 agent 的形式：正斜杠分隔。agent 的命令语言是 sh，Windows 上
// 正斜杠形式（C:/...）sh 与原生程序都认，反斜杠在 sh 里是转义符；其它平台原样返回。
// 命令环境里的目录、附件清单等一切写进命令的路径都经它转换。
func AgentPath(p string) string { return filepath.ToSlash(p) }
