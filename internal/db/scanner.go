// Package db 提供跨 store 共享的 postgres 底层工具。
package db

// RowScanner 是 pgx.Row / pgx.Rows 的公共子集——各 store 的统一 scan 帮手
// （colsSelect 列序 → scan() 反序列化）都面向它编程，免于逐包复制接口定义。
type RowScanner interface {
	Scan(dest ...any) error
}
