//go:build !windows

package sandbox

// shellCandidates：POSIX 系统自带 sh，PATH 之外没有候选。
func shellCandidates() []string { return nil }
