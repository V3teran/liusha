package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/V3teran/liusha/internal/sandbox"
)

func execOnce(t *testing.T, url, command string) sandbox.ExecResult {
	t.Helper()
	body, _ := json.Marshal(sandbox.ExecRequest{AssignmentID: "asg", TaskID: "task", AgentID: "a", Command: command, TimeoutSeconds: 10})
	resp, err := http.Post(url+"/exec", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var res sandbox.ExecResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	return res
}

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
	setupTestRoots(t)
	ts := httptest.NewServer(New().mux)
	t.Cleanup(ts.Close)

	var wg sync.WaitGroup
	var a, b sandbox.ExecResult
	wg.Add(2)
	go func() { defer wg.Done(); a = execOnce(t, ts.URL, `sleep 0.5; echo a > "$OUTPUT_DIR/A.txt"`) }()
	go func() { defer wg.Done(); b = execOnce(t, ts.URL, `sleep 0.1; echo b > "$OUTPUT_DIR/B.txt"`) }()
	wg.Wait()

	if got := fileNames(a); !slices.Equal(got, []string{"A.txt"}) {
		t.Errorf("A 只应归属 A.txt，得 %v", got)
	}
	if got := fileNames(b); !slices.Equal(got, []string{"B.txt"}) {
		t.Errorf("B 只应归属 B.txt，得 %v", got)
	}
	out := filepath.Join(liushaRoot, "asg", "task", "out")
	for _, name := range []string{"A.txt", "B.txt"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("%s 应落在任务产物目录 out/: %v", name, err)
		}
	}
}

// 契约：后续命令经 $OUTPUT_DIR 读取先前产物；原地追加既有产物归属本次。
func TestHandleExec_OutputDirKeepsPriorProducts(t *testing.T) {
	setupTestRoots(t)
	ts := httptest.NewServer(New().mux)
	t.Cleanup(ts.Close)

	if got := fileNames(execOnce(t, ts.URL, `echo first > "$OUTPUT_DIR/p.txt"`)); !slices.Equal(got, []string{"p.txt"}) {
		t.Fatalf("首次写入应归属 p.txt，得 %v", got)
	}
	read := execOnce(t, ts.URL, `cat "$OUTPUT_DIR/p.txt"`)
	if strings.TrimSpace(read.Stdout) != "first" || len(read.Files) != 0 {
		t.Fatalf("应能读到先前产物且不归属任何文件: stdout=%q files=%v", read.Stdout, fileNames(read))
	}
	if got := fileNames(execOnce(t, ts.URL, `echo more >> "$OUTPUT_DIR/p.txt"`)); !slices.Equal(got, []string{"p.txt"}) {
		t.Fatalf("原地追加应归属 p.txt，得 %v", got)
	}
	data, err := os.ReadFile(filepath.Join(liushaRoot, "asg", "task", "out", "p.txt"))
	if err != nil || string(data) != "first\nmore\n" {
		t.Fatalf("out/p.txt 应为追加后的内容，得 %q %v", data, err)
	}
}
