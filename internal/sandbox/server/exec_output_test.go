package server

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/V3teran/liusha/internal/sandbox"
)

func fileNames(res sandbox.ExecResult) []string {
	var out []string
	for _, f := range res.Files {
		out = append(out, f.Name)
	}
	slices.Sort(out)
	return out
}

// 同任务并发执行：每次执行只归属自己写出的产物。共享产物目录上的前后快照差分无法区分
// 并发执行期间别人写的文件，会把 B 的产物也报成 A 的。
func TestHandleExec_ConcurrentExecsAttributeOwnOutputs(t *testing.T) {
	h := newHarness(t)

	var wg sync.WaitGroup
	var a, b sandbox.ExecResult
	wg.Add(2)
	go func() { defer wg.Done(); a = h.exec(t, `sleep 0.5; echo a > "$OUTPUT_DIR/A.txt"`) }()
	go func() { defer wg.Done(); b = h.exec(t, `sleep 0.1; echo b > "$OUTPUT_DIR/B.txt"`) }()
	wg.Wait()

	if got := fileNames(a); !slices.Equal(got, []string{"A.txt"}) {
		t.Errorf("A 只应归属 A.txt，得 %v", got)
	}
	if got := fileNames(b); !slices.Equal(got, []string{"B.txt"}) {
		t.Errorf("B 只应归属 B.txt，得 %v", got)
	}
	out := filepath.Join(h.root, "asg", "task", "out")
	for _, name := range []string{"A.txt", "B.txt"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("%s 应落在任务产物目录 out/: %v", name, err)
		}
	}
}

// 契约：后续命令经 $OUTPUT_DIR 读取先前产物；原地追加既有产物归属本次。
func TestHandleExec_OutputDirKeepsPriorProducts(t *testing.T) {
	h := newHarness(t)

	if got := fileNames(h.exec(t, `echo first > "$OUTPUT_DIR/p.txt"`)); !slices.Equal(got, []string{"p.txt"}) {
		t.Fatalf("首次写入应归属 p.txt，得 %v", got)
	}
	read := h.exec(t, `cat "$OUTPUT_DIR/p.txt"`)
	if strings.TrimSpace(read.Stdout) != "first" || len(read.Files) != 0 {
		t.Fatalf("应能读到先前产物且不归属任何文件: stdout=%q files=%v", read.Stdout, fileNames(read))
	}
	if got := fileNames(h.exec(t, `echo more >> "$OUTPUT_DIR/p.txt"`)); !slices.Equal(got, []string{"p.txt"}) {
		t.Fatalf("原地追加应归属 p.txt，得 %v", got)
	}
	data, err := os.ReadFile(filepath.Join(h.root, "asg", "task", "out", "p.txt"))
	if err != nil || string(data) != "first\nmore\n" {
		t.Fatalf("out/p.txt 应为追加后的内容，得 %q %v", data, err)
	}
}

// 并发执行 A、B 都预置了同一个既有产物：A 以「写新文件再改名」替换它并先发布，B 没动它、
// 后发布——B 不得把旧版写回 out/，也不得把它当作自己的产物。
func TestPublish_UntouchedPrelinkDoesNotRevertConcurrentReplacement(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "asg", "task", "out")
	if err := os.MkdirAll(shared, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "evidence.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := stageOutput(root, shared)
	if err != nil {
		t.Fatal(err)
	}
	b, err := stageOutput(root, shared)
	if err != nil {
		t.Fatal(err)
	}

	// A：写新文件再改名覆盖自己的暂存项（常见的原子写法）。
	tmp := filepath.Join(a.dir, ".evidence.tmp")
	if err := os.WriteFile(tmp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(a.dir, "evidence.txt")); err != nil {
		t.Fatal(err)
	}
	if files, _ := a.publish(); len(files) != 1 || files[0].Name != "evidence.txt" {
		t.Fatalf("A 应发布新版 evidence.txt, got %v", files)
	}

	files, _ := b.publish()
	if len(files) != 0 {
		t.Fatalf("B 没有改动它，不应归属任何产物, got %v", files)
	}
	if got, _ := os.ReadFile(filepath.Join(shared, "evidence.txt")); string(got) != "new" {
		t.Fatalf("out/evidence.txt 被回滚为 %q", got)
	}
}

// 并发执行中 B 原地改写了预置的旧版，而 A 已替换了 out/ 里的同名文件：B 的改动作为它自己的
// 附件返回并给出警告，不覆盖 A 的新版。
func TestPublish_InPlaceEditOfReplacedFileDoesNotClobber(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "asg", "task", "out")
	if err := os.MkdirAll(shared, 0o750); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(shared, "evidence.txt")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := stageOutput(root, shared)
	if err != nil {
		t.Fatal(err)
	}
	// A 在别处替换了 out/ 里的文件。
	tmp := filepath.Join(shared, ".evidence.tmp")
	if err := os.WriteFile(tmp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, target); err != nil {
		t.Fatal(err)
	}
	// B 原地追加它看到的（旧的）那份。
	f, err := os.OpenFile(filepath.Join(b.dir, "evidence.txt"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("+b")
	_ = f.Close()

	files, warnings := b.publish()
	if len(files) != 1 || files[0].Name != "evidence.txt" {
		t.Fatalf("B 的改动应作为附件返回, got %v", files)
	}
	if data, _ := base64.StdEncoding.DecodeString(files[0].B64); string(data) != "old+b" {
		t.Fatalf("附件应是 B 改写后的内容, got %q", data)
	}
	if len(warnings) == 0 {
		t.Fatal("应警告 out/ 中同名文件已被并发执行替换")
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Fatalf("A 的新版不应被覆盖, got %q", got)
	}
}
