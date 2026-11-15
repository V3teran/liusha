package attachment

import (
	"errors"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/workspace"
)

func newStores(t *testing.T) (*Store, *workspace.Store) {
	t.Helper()
	ws, err := workspace.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() }) // Windows 上打开着的目录句柄会让临时目录删不掉
	return NewStore(ws), ws
}

// TestSaveAndMaterialize 走通主链：pending 上传 → 随下发搬进 inbox；同名冲突
// 落 -1 后缀不互顶；已落位的引用（随续接重发）原样沿用。
func TestSaveAndMaterialize(t *testing.T) {
	s, _ := newStores(t)

	ref, err := s.SaveUpload(Dest{UploadID: "upload-1"}, strings.NewReader("hello targets"), "targets.txt", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ref.Path, ".staging/pending/upload-1/") {
		t.Fatalf("pending 落点不符: %s", ref.Path)
	}

	mat, err := s.Materialize(Dest{ConversationID: "conv-1", AssignmentID: "asg-1", TaskID: "task-1"}, []Ref{ref})
	if err != nil {
		t.Fatal(err)
	}
	if len(mat) != 1 || mat[0].Path != path.Join("asg-1", "task-1", "inbox", "targets.txt") {
		t.Fatalf("落位路径不符: %+v", mat)
	}
	if _, err := s.ws.Stat(ref.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("暂存原件应已搬走: %v", err)
	}

	// 任务会话直落 inbox
	direct, err := s.SaveUpload(Dest{AssignmentID: "asg-1", TaskID: "task-1"}, strings.NewReader("dict"), "rockyou.txt", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(direct.Path, path.Join("asg-1", "task-1", "inbox/")) {
		t.Fatalf("任务直落路径不符: %s", direct.Path)
	}

	// 再传同名 targets.txt（随续接）→ -1 后缀，原件保留
	if _, err := s.SaveUpload(Dest{UploadID: "upload-2"}, strings.NewReader("v2"), "targets.txt", 1<<20); err != nil {
		t.Fatal(err)
	}
	mat2, err := s.Materialize(Dest{ConversationID: "conv-1", AssignmentID: "asg-1", TaskID: "task-1"}, []Ref{{Name: "targets.txt", Path: ".staging/pending/upload-2/targets.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if mat2[0].Path != path.Join("asg-1", "task-1", "inbox", "targets-1.txt") {
		t.Fatalf("重名应落 -1 后缀: %+v", mat2)
	}
	// 原件与后缀件并存
	for _, p := range []string{mat[0].Path, mat2[0].Path} {
		if _, err := s.ws.Stat(p); err != nil {
			t.Fatalf("落位件应并存: %s: %v", p, err)
		}
	}
}

// TestMaterializeRejectsForeignPath：引用防伪造——跨任务 inbox、会话外暂存、
// 路径穿越一律整批拒绝。
func TestMaterializeRejectsForeignPath(t *testing.T) {
	s, ws := newStores(t)
	// 造一个别人的 inbox 件
	if err := ws.Write("asg-x/task-x/inbox/secret.txt", []byte("x")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"asg-x/task-x/inbox/secret.txt",  // 其他任务 inbox
		".staging/conv/other-conv/a.txt", // 其他会话暂存
		"../../etc/passwd",               // 穿越（resolve 拒绝）
		"no-such-file.txt",               // 根外不存在
	} {
		if _, err := s.Materialize(Dest{ConversationID: "conv-1", AssignmentID: "asg-1", TaskID: "task-1"}, []Ref{{Name: "a", Path: p}}); err == nil {
			t.Fatalf("非法引用应拒绝: %s", p)
		}
	}

	// 合法根内但文件已失踪（pending 超期被清扫后旧草稿才发送）：过期语义，
	// 带文件名供重新上传，不泄漏内部路径
	_, err := s.Materialize(Dest{ConversationID: "conv-1", AssignmentID: "asg-1", TaskID: "task-1"},
		[]Ref{{Name: "one.txt", Path: ".staging/pending/swept/one.txt"}})
	if !errors.Is(err, ErrRefExpired) {
		t.Fatalf("合法根内失踪应报过期，得 %v", err)
	}
	if !strings.Contains(err.Error(), "one.txt") || strings.Contains(err.Error(), ".staging") {
		t.Fatalf("过期错误应带文件名且不泄漏内部路径: %v", err)
	}
}

// TestRemoveStaging：会话删除收口——本会话暂存整目录删除，他者不受波及。
func TestRemoveStaging(t *testing.T) {
	s, _ := newStores(t)
	if _, err := s.SaveUpload(Dest{ConversationID: "conv-1"}, strings.NewReader("x"), "a.txt", 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveUpload(Dest{ConversationID: "conv-2"}, strings.NewReader("y"), "b.txt", 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveStaging("conv-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ws.Stat(convStagingRel("conv-1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("conv-1 暂存应已删除: %v", err)
	}
	if _, err := s.ws.Stat(convStagingRel("conv-2") + "/b.txt"); err != nil {
		t.Fatalf("conv-2 暂存不应波及: %v", err)
	}
	// 不存在的会话：no-op
	if err := s.RemoveStaging("conv-none"); err != nil {
		t.Fatalf("无暂存应视为清理成功: %v", err)
	}
}

// TestSweepStalePending：超期 pending 目录整目录清扫，新鲜目录保留。
func TestSweepStalePending(t *testing.T) {
	s, _ := newStores(t)
	if _, err := s.SaveUpload(Dest{UploadID: "old"}, strings.NewReader("x"), "a.txt", 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveUpload(Dest{UploadID: "fresh"}, strings.NewReader("y"), "b.txt", 1<<20); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(path.Join(s.ws.Root(), ".staging", "pending", "old"), old, old); err != nil {
		t.Fatal(err)
	}
	s.SweepStalePending(24 * time.Hour)
	if _, err := s.ws.Stat(pendingStagingRel("old")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("超期 pending 应被清扫: %v", err)
	}
	if _, err := s.ws.Stat(pendingStagingRel("fresh") + "/b.txt"); err != nil {
		t.Fatalf("新鲜 pending 应保留: %v", err)
	}
}

// TestInlineTexts：小文本内联原文；二进制占位；越权/失踪引用不进内容、给可见占位。
func TestInlineTexts(t *testing.T) {
	s, _ := newStores(t)
	if _, err := s.SaveUpload(Dest{ConversationID: "conv-1"}, strings.NewReader("192.168.1.1\n192.168.1.2"), "targets.txt", 1<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveUpload(Dest{ConversationID: "conv-1"}, strings.NewReader(string([]byte{0x00, 0x01, 0x02})), "shell.bin", 1<<20); err != nil {
		t.Fatal(err)
	}
	dest := Dest{ConversationID: "conv-1"}
	out, err := s.InlineTexts(dest, []Ref{
		{Name: "targets.txt", Path: ".staging/conv/conv-1/targets.txt"},
		{Name: "shell.bin", Path: ".staging/conv/conv-1/shell.bin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "192.168.1.1") || !strings.Contains(out, "附件 1：targets.txt") {
		t.Fatalf("小文本应内联: %s", out)
	}
	if !strings.Contains(out, "shell.bin") || strings.Contains(out, "\x00") {
		t.Fatalf("二进制应占位说明且不进内容: %s", out)
	}
	if empty, _ := s.InlineTexts(dest, nil); empty != "" {
		t.Fatalf("空引用应返回空串: %q", empty)
	}

	// 引用防伪造与落位同源：越权路径不落原文，合法根内失踪给过期语义
	leak, err := s.InlineTexts(dest, []Ref{
		{Name: "secret", Path: "asg-x/task-x/inbox/secret.txt"},
		{Name: "ghost", Path: ".staging/conv/conv-1/gone.txt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(leak, "asg-x") {
		t.Fatalf("越权路径不得出现在 prompt 形态里: %s", leak)
	}
	if !strings.Contains(leak, "引用非法") || !strings.Contains(leak, "已过期或不存在") {
		t.Fatalf("越权/失踪应分别给可见占位: %s", leak)
	}
}

// TestManifest：清单携带 agent 可读的绝对路径（工作区根 + 相对路径）与尺寸。
func TestManifest(t *testing.T) {
	s, ws := newStores(t)
	m := s.Manifest([]Ref{{Name: "targets.txt", Size: 2048, Path: "asg-1/task-1/inbox/targets.txt"}})
	if !strings.Contains(m, sandbox.AgentPath(ws.Root())+"/asg-1/task-1/inbox/targets.txt") {
		t.Fatalf("清单应含工作区内绝对路径: %s", m)
	}
	if !strings.Contains(m, "2.0 KB") {
		t.Fatalf("清单应含人类可读尺寸: %s", m)
	}
	if s.Manifest(nil) != "" {
		t.Fatal("空引用清单应为空串")
	}
}

// TestSanitizeName：目录成分、控制字符、前后空白点号全剥；空名回退 attachment。
func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"../../etc/passwd":     "passwd",
		"C:\\evil\\path\\x.sh": "x.sh",
		"  .hidden.txt ":       "hidden.txt",
		"报\u0007告.txt":         "报告.txt",
		"":                     "attachment",
		"...":                  "attachment",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

// 并发同名上传：每次上传都得到独立落点、内容互不覆盖。
func TestSaveUploadConcurrentSameNameNoClobber(t *testing.T) {
	s, ws := newStores(t)
	const n = 8
	type result struct {
		ref Ref
		err error
	}
	results := make(chan result, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			ref, err := s.SaveUpload(Dest{UploadID: "u"}, strings.NewReader(strings.Repeat(string(rune('a'+i)), 64)), "same.txt", 1<<20)
			results <- result{ref, err}
		}(i)
	}
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		r := <-results
		if r.err != nil {
			t.Fatalf("上传失败: %v", r.err)
		}
		if seen[r.ref.Path] {
			t.Fatalf("两次上传落到同一路径: %s", r.ref.Path)
		}
		seen[r.ref.Path] = true
		got, err := ws.Read(r.ref.Path)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Content) != 64 || strings.Count(got.Content, got.Content[:1]) != 64 {
			t.Fatalf("文件内容被交错写坏: %q", got.Content)
		}
	}
}

// 批内有一条已过期引用：整批拒绝，且有效引用原地不动（重试时仍可用）。
func TestMaterializeAllOrNothing(t *testing.T) {
	s, ws := newStores(t)
	good, err := s.SaveUpload(Dest{ConversationID: "conv-1"}, strings.NewReader("ok"), "a.txt", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	expired := Ref{Name: "gone.txt", Path: path.Join(path.Dir(good.Path), "gone.txt")}

	_, err = s.Materialize(Dest{ConversationID: "conv-1", AssignmentID: "asg", TaskID: "t"}, []Ref{good, expired})
	if !errors.Is(err, ErrRefExpired) {
		t.Fatalf("应因过期引用整批拒绝，得 %v", err)
	}
	if _, err := ws.Stat(good.Path); err != nil {
		t.Fatalf("有效引用不应被搬走: %v", err)
	}
	if _, err := ws.Stat(path.Join("asg", "t", "inbox", "a.txt")); err == nil {
		t.Fatal("拒绝的批次不应在 inbox 留下文件")
	}
}
