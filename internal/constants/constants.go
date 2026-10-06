// Package constants 定义项目级别的常量。
//
// 原则：只收录真实被引用的共享常量；单一调用点使用的数值就地声明
// （集中清单若无人消费只是另一种魔法数字）。
package constants

import "time"

// 时长类常量。
const (
	// DBQueryTimeout 数据库查询超时（简单查询）
	DBQueryTimeout = 5 * time.Second

	// ToolTimeoutQuick 工具执行超时（快速工具：读配置/查列表）
	ToolTimeoutQuick = 5 * time.Second

	// ToolTimeoutMedium 工具执行超时（中等工具：insight 查询等）
	ToolTimeoutMedium = 10 * time.Second

	// ToolTimeoutLong 工具执行超时（长工具：图查询/重放）
	ToolTimeoutLong = 30 * time.Second

	// ToolTimeoutCommand 工具执行超时（沙箱命令）
	ToolTimeoutCommand = 120 * time.Second

	// ToolTimeoutBrowser 工具执行超时（浏览器自动化）
	ToolTimeoutBrowser = 180 * time.Second

	// MonitorInterval Monitor Agent 全局评估周期
	MonitorInterval = 6 * time.Minute

	// LogFlushInterval 日志刷新周期
	LogFlushInterval = 1 * time.Second
)

// 容量类常量。
const (
	// ChannelBufferLarge 通道缓冲（事件总线订阅/操作队列）
	ChannelBufferLarge = 32

	// ChannelBufferVeryLarge 通道缓冲（事件总线发布队列）
	ChannelBufferVeryLarge = 100
)
