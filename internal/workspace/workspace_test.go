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
	if err := s.Write("task-1/work/scripts/scan.sh", []byte("#!/bin/sh\necho hi\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	fc, err := s.Read("task-1/work/scripts/scan.sh")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if fc.Binary || fc.Content != "#!/bin/sh\necho hi\n" {
		t.Fatalf("内容不符: %+v", fc)
	}
	tree, err := s.Tree()
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	// task-1/ + task-1/work/ + task-1/work/scripts/ + 脚本文件
	if len(tree) != 4 {
		t.Fatalf("tree 应含 4 节点: %+v", tree)
	}
	if !tree[0].IsDir || tree[0].Path != "task-1/" || tree[0].Depth != 0 {
		t.Fatalf("顶层任务目录应在前: %+v", tree[0])
	}
	if err := s.Delete("task-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Read("task-1/work/scripts/scan.sh"); err == nil {
		t.Fatal("删除后仍可读")
	}
}

func TestTreeNewestTaskFirst(t *testing.T) {
	s := newTestStore(t)
	_ = s.Write("task-old/work/a.txt", []byte("a"))
	_ = s.Write("task-new/out/b.txt", []byte("b"))
	stamp := filepath.Join(s.Root(), "task-old")
	past := time.Now().AddDate(0, 0, -1)
	os.Chtimes(stamp, past, past)

	tree, err := s.Tree()
	if err != nil {
		t.Fatal(err)
	}
	if tree[0].Path != "task-new/" {
		t.Fatalf("最新任务目录应排首: %+v", tree[0])
	}
	// 组内聚合：task-new 的子节点紧随其组
	if !strings.HasPrefix(tree[1].Path, "task-new/") {
		t.Fatalf("组内聚合被打破: %+v", tree[1])
	}
}

func TestReadBinaryFlagged(t *testing.T) {
	s := newTestStore(t)
	if err := s.Write("task-1/out/blob.bin", []byte{0xff, 0xfe, 0x00, 0x01}); err != nil {
		t.Fatal(err)
	}
	fc, err := s.Read("task-1/out/blob.bin")
	if err != nil || !fc.Binary {
		t.Fatalf("二进制应被标记: %+v err=%v", fc, err)
	}
}

func TestZipToDirectory(t *testing.T) {
	s := newTestStore(t)
	_ = s.Write("task-1/work/deep/poc.py", []byte("print(1)"))
	var buf strings.Builder
	if err := s.ZipTo("task-1/work", &zipWriterAdapter{&buf}); err != nil {
		t.Fatalf("zip: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("zip 输出为空")
	}
	// zip 魔数
	if !strings.HasPrefix(buf.String(), "PK") {
		t.Fatal("输出不是 zip 格式")
	}
}

func TestOpenFileRejectsDir(t *testing.T) {
	s := newTestStore(t)
	_ = s.Write("task-1/x.txt", []byte("x"))
	if _, _, err := s.OpenFile("task-1"); err == nil {
		t.Fatal("目录应被 OpenFile 拒绝")
	}
	f, _, err := s.OpenFile("task-1/x.txt")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	f.Close()
}

func TestPathTraversalRejected(t *testing.T) {
	s := newTestStore(t)
	_ = s.Write("task-1/safe.txt", []byte("x"))
	for _, tc := range []struct{ rel string }{
		{"../task-1/safe.txt"},
		{"../../etc/passwd"},
		{"/etc/passwd"},
		{"a/../..\\b.txt"},
		{"sub/\x01ctl"},
	} {
		if _, err := s.Read(tc.rel); err == nil {
			t.Errorf("Read 应拒绝 %q", tc.rel)
		}
		if err := s.Write(tc.rel, []byte("x")); err == nil {
			t.Errorf("Write 应拒绝 %q", tc.rel)
		}
		if err := s.Delete(tc.rel); err == nil {
			t.Errorf("Delete 应拒绝 %q", tc.rel)
		}
	}
	if _, err := s.Read("task-1/safe.txt"); err != nil {
		t.Fatalf("合法路径被误拒: %v", err)
	}
}

func TestDeleteRootDenied(t *testing.T) {
	s := newTestStore(t)
	_ = s.Write("task-1/x.txt", []byte("x"))
	for _, rel := range []string{".", ""} {
		if err := s.Delete(rel); err == nil {
			t.Errorf("工作区根目录删除应被拒绝 (rel=%q)", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(s.Root(), "task-1", "x.txt")); err != nil {
		t.Fatalf("误删任务目录: %v", err)
	}
}

// zipWriterAdapter 把 strings.Builder 适配为 io.Writer（测试内最小依赖）。
type zipWriterAdapter struct{ b *strings.Builder }

func (a *zipWriterAdapter) Write(p []byte) (int, error) { return a.b.Write(p) }
