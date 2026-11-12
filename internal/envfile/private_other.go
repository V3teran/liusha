//go:build !windows

package envfile

// restrictToOwner 在 POSIX 上无需额外动作：文件以 0600 创建。
func restrictToOwner(string) error { return nil }
