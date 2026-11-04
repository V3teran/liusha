package workspace

import (
	"archive/zip"
	"bytes"
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
	_ = os.Chtimes(stamp, past, past)

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
	_ = f.Close()
}

func TestUnicodeFileNamesAllowed(t *testing.T) {
	s := newTestStore(t)
	// 中文笔记名是真实需求（agent/用户都可能建），黑名单校验下必须合法
	if err := s.Write("任务-1/work/渗透笔记.md", []byte("# 笔记")); err != nil {
		t.Fatalf("中文文件名写入失败: %v", err)
	}
	fc, err := s.Read("任务-1/work/渗透笔记.md")
	if err != nil || fc.Binary || fc.Content != "# 笔记" {
		t.Fatalf("read: %+v err=%v", fc, err)
	}
	if err := s.Mkdir("任务-1/报告/最终版"); err != nil {
		t.Fatalf("中文目录创建失败: %v", err)
	}
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

// 会话状态子树（state/）整体排除出文件面：树不显示、读写下载归档全拒绝
// （chromium 登录态等 $HOME 落盘是沙盒内部状态，不是操作员工件；数据保留在盘）。
func TestStateDirExcludedFromFileSurface(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Write("70bbf3bb/out/poc.py", []byte("print(1)")); err != nil {
		t.Fatal(err)
	}
	// 直接写盘模拟沙盒 $HOME 落盘（不经过文件 API）
	if err := os.MkdirAll(filepath.Join(store.Root(), "70bbf3bb/state/.cache/chromium"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Root(), "70bbf3bb/state/.cache/chromium/x"), []byte("junk"), 0o640); err != nil {
		t.Fatal(err)
	}

	tree, err := store.Tree()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range tree {
		if strings.HasPrefix(e.Path, "70bbf3bb/state") {
			t.Fatalf("树不应包含 state 子树: %s", e.Path)
		}
	}
	if _, err := store.Read("70bbf3bb/state/x"); err == nil {
		t.Fatal("Read 应拒绝 state 路径")
	}
	if err := store.Write("70bbf3bb/state/x", []byte("x")); err == nil {
		t.Fatal("Write 应拒绝 state 路径")
	}
	if err := store.Delete("70bbf3bb/state"); err == nil {
		t.Fatal("Delete 应拒绝 state 路径")
	}
	var zipBuf bytes.Buffer
	if err := store.ZipTo("70bbf3bb", &zipBuf); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBuf.Bytes()), int64(zipBuf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if strings.Contains(f.Name, "state") {
			t.Fatalf("归档不应包含 state 子树: %s", f.Name)
		}
	}
	if _, err := store.Read("70bbf3bb/out/poc.py"); err != nil {
		t.Fatalf("state 之外的读写不受影响: %v", err)
	}
}
