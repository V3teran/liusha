//go:build !linux

package server

// killEscaped 在非 Linux 平台上不可用（没有 /proc）：只靠登记的命令进程组与浏览器 reset 释放。
// 沙盒的部署目标是 Linux，这里只为开发机上能编译、运行单测。
func killEscaped(string) int { return 0 }
