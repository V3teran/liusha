package sandbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mkFile(t *testing.T, p string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func setDirTimes(t *testing.T, root string, mtime time.Time) {
	t.Helper()
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() {
			_ = os.Chtimes(p, mtime, mtime)
		}
		return nil
	})
}

// 同一批次里：久无活动的任务被清扫，近期续跑写过证据的任务保留（即便批次目录本身很旧）。
func TestSweepExpiredTaskDirs_PerTaskActivity(t *testing.T) {
	root := t.TempDir()
	old := time.Now().AddDate(0, 0, -30)
	recent := time.Now().Add(-time.Hour)

	mkFile(t, filepath.Join(root, "asg-1", "task-old", "out", "report.md"), old)
	mkFile(t, filepath.Join(root, "asg-1", "task-active", "work", "evidence.txt"), recent)
	mkFile(t, filepath.Join(root, "asg-2", "task-gone", "out", "x"), old)
	mkFile(t, filepath.Join(root, ".staging", "pending", "u", "f"), old)
	setDirTimes(t, root, old) // 所有目录 mtime 都很旧：只能靠子树内文件判断活动

	removed := sweepExpiredTaskDirs(root, time.Now().AddDate(0, 0, -7))

	exists := func(rel string) bool { _, err := os.Stat(filepath.Join(root, rel)); return err == nil }
	if exists("asg-1/task-old") {
		t.Error("久无活动的任务应被清扫")
	}
	if !exists("asg-1/task-active/work/evidence.txt") {
		t.Error("近期有写入的任务不应被清扫")
	}
	if !exists("asg-1") {
		t.Error("仍有任务的批次目录应保留")
	}
	if exists("asg-2") {
		t.Error("任务全部清空的批次目录应删除")
	}
	if !exists(".staging/pending/u/f") {
		t.Error("点前缀目录（附件暂存）不归本清扫器处理")
	}
	if len(removed) != 3 {
		t.Errorf("应删除 3 项（task-old、task-gone、asg-2），得 %v", removed)
	}
}
