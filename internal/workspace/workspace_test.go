package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestWriteReadDeleteRoundtrip(t *testing.T) {
	s := newTestStore(t)
	if err := s.Write("task-1", "work/scripts/scan.sh", []byte("#!/bin/sh\necho hi\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	fc, err := s.Read("task-1", "work/scripts/scan.sh")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if fc.Binary || fc.Content != "#!/bin/sh\necho hi\n" {
		t.Fatalf("内容不符: %+v", fc)
	}
	tree, err := s.Tree("task-1")
	if err != nil || len(tree) != 3 { // work/ + work/scripts/ + 脚本文件
		t.Fatalf("tree: %v %+v", err, tree)
	}
	if !tree[0].IsDir || tree[0].Path != "work/" {
		t.Fatalf("目录应在前且带尾斜杠: %+v", tree[0])
	}
	if err := s.Delete("task-1", "work"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Read("task-1", "work/scripts/scan.sh"); err == nil {
		t.Fatal("删除后仍可读")
	}
}

func TestReadBinaryFlagged(t *testing.T) {
	s := newTestStore(t)
	if err := s.Write("task-1", "out/blob.bin", []byte{0xff, 0xfe, 0x00, 0x01}); err != nil {
		t.Fatal(err)
	}
	fc, err := s.Read("task-1", "out/blob.bin")
	if err != nil || !fc.Binary {
		t.Fatalf("二进制应被标记: %+v err=%v", fc, err)
	}
	if fc.Content == string([]byte{0xff, 0xfe, 0x00, 0x01}) {
		t.Fatal("二进制内容应 base64 编码而非原样")
	}
}

func TestPathTraversalRejected(t *testing.T) {
	s := newTestStore(t)
	_ = s.Write("task-1", "safe.txt", []byte("x"))
	for _, tc := range []struct{ taskID, rel string }{
		{"task-1", "../task-1/safe.txt"},
		{"task-1", "../../etc/passwd"},
		{"task-1", "/etc/passwd"},
		{"../escape", "x.txt"},
		{"task-1", "a/../..\\b.txt"},
		{"task-1", "sub/\x01ctl"},
	} {
		if _, err := s.Read(tc.taskID, tc.rel); err == nil {
			t.Errorf("Read 应拒绝 %q/%q", tc.taskID, tc.rel)
		}
		if err := s.Write(tc.taskID, tc.rel, []byte("x")); err == nil {
			t.Errorf("Write 应拒绝 %q/%q", tc.taskID, tc.rel)
		}
		if err := s.Delete(tc.taskID, tc.rel); err == nil {
			t.Errorf("Delete 应拒绝 %q/%q", tc.taskID, tc.rel)
		}
	}
	// 合法内容未被误伤
	if _, err := s.Read("task-1", "safe.txt"); err != nil {
		t.Fatalf("合法路径被误拒: %v", err)
	}
}

func TestDeleteRootDenied(t *testing.T) {
	s := newTestStore(t)
	_ = s.Write("task-1", "x.txt", []byte("x"))
	for _, rel := range []string{".", ""} {
		if err := s.Delete("task-1", rel); err == nil {
			t.Errorf("任务根目录删除应被拒绝 (rel=%q)", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "task-1", "x.txt")); err != nil {
		t.Fatalf("误删任务目录: %v", err)
	}
}

func TestTasksOrdering(t *testing.T) {
	s := newTestStore(t)
	_ = s.Write("task-old", "a.txt", []byte("a"))
	_ = s.Write("task-new", "b.txt", []byte("b"))
	// 触碰 old 让其 mtime 早于 new（同秒创建时序不稳定，显式拨回）
	past := filepath.Join(s.Root(), "task-old", "a.txt")
	stamp := time.Now().AddDate(0, 0, -1)
	os.Chtimes(past, stamp, stamp)
	tasks, err := s.Tasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].Name != "task-new" {
		t.Fatalf("应按活动时间倒序: %+v", tasks)
	}
	if !strings.HasPrefix(s.Root(), "/") {
		t.Fatal("Root 应为绝对路径")
	}
}
