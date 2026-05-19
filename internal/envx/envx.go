// Package envx 是环境变量读取的小工具集——cmd/* 各 main 共用，避免在每个
// 可执行入口复制 envOr 实现。
package envx

import "os"

// OrDefault 读 env key；空（未设或显式空串）时返 fallback。
func OrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
