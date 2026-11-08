package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// collectAttachments 的归属契约：本次 exec 的产物 = 「执行前不存在，或存在但
// mtime 已变化」。锁定旧版 mtime ≥ execStart 过滤的两个时钟依赖缺陷：
//  1. 精度截断——毫秒内完成的命令在 mtime 精度粗于时钟差的文件系统上，刚写的
//     文件被判成历史文件（CI 实测 Files=[]，main CI 失败）；
//  2. 同秒旧文件——mtime 与 execStart 相等的旧文件被误收。
//
// 旧实现对 case 1/3/4 会失败（或行为随时钟精度漂移），新实现确定性。
func TestCollectAttachments_SnapshotDiff(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	write := func(name string, mt time.Time) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}

	// 执行前目录状态：unchanged（历史文件）、modified（本次改写）、gone（已删）
	write("unchanged.txt", now)
	write("modified.txt", now.Add(-time.Minute))
	write("gone.txt", now)
	before := snapshotDir(dir)
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	write("modified.txt", now) // 命令改写：mtime 变化
	// 模拟精度截断：命令启动后才写，但文件系统记的 mtime 早于快照时刻
	write("truncated.txt", now.Add(-2*time.Second))
	// 命令后新增的正常文件
	write("fresh.txt", now.Add(time.Second))

	files, warnings := collectAttachments(dir, before)
	if len(warnings) != 0 {
		t.Fatalf("不应有告警: %v", warnings)
	}
	got := map[string]bool{}
	for _, f := range files {
		got[f.Name] = true
	}
	for _, name := range []string{"fresh.txt", "truncated.txt", "modified.txt"} {
		if !got[name] {
			t.Errorf("%s 应被收集（本次改动面）", name)
		}
	}
	for _, name := range []string{"unchanged.txt", "gone.txt"} {
		if got[name] {
			t.Errorf("%s 不应被收集（历史文件/已删除）", name)
		}
	}
}
