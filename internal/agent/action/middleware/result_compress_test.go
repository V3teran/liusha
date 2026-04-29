package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/agent/action"
)

// makeBigOutput 构造指定大小的可读字节（重复 ASCII 模式，便于 hash 比对）。
func makeBigOutput(size int) []byte {
	b := make([]byte, size)
	for i := range b {
		b[i] = byte('A' + (i % 26))
	}
	return b
}

// stubExecutor 返回固定 Result 的 base executor，用于驱动中间件测试。
func stubExecutor(out []byte, summary string) action.ActionExecutor {
	return func(_ context.Context, _ string, _ json.RawMessage) (action.Result, error) {
		return action.Result{Output: out, Summary: summary}, nil
	}
}

func TestResultCompress_SmallNotCompressed(t *testing.T) {
	dir := t.TempDir()
	mw := ResultCompress("eng-small", dir)

	original := makeBigOutput(1024) // 1KB <= 2KB 阈值
	exec := mw(stubExecutor(original, "原 summary"))

	res, err := exec(context.Background(), "scan", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("不应该出错: %v", err)
	}
	if string(res.Output) != string(original) {
		t.Fatalf("小 Output 应该原样透传")
	}
	if res.Summary != "原 summary" {
		t.Fatalf("Summary 应该保留原值, 实际: %q", res.Summary)
	}
	// 确认未落盘
	entries, _ := os.ReadDir(filepath.Join(dir, "eng-small"))
	if len(entries) != 0 {
		t.Fatalf("小结果不应该落盘, 实际有 %d 个文件", len(entries))
	}
}

func TestResultCompress_LargeCompressed(t *testing.T) {
	dir := t.TempDir()
	mw := ResultCompress("eng-large", dir)

	original := makeBigOutput(5 * 1024) // 5KB > 2KB
	exec := mw(stubExecutor(original, ""))

	res, err := exec(context.Background(), "leak", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("不应该出错: %v", err)
	}

	// 1) Output 应该是 JSON {path,size,sha256,snippet}
	var meta struct {
		Path    string `json:"path"`
		Size    int    `json:"size"`
		SHA256  string `json:"sha256"`
		Snippet string `json:"snippet"`
	}
	if err := json.Unmarshal(res.Output, &meta); err != nil {
		t.Fatalf("Output 应该是 JSON 元数据, 解析失败: %v, 内容: %s", err, string(res.Output))
	}
	if meta.Size != len(original) {
		t.Fatalf("size 应该 = %d, 实际 %d", len(original), meta.Size)
	}
	wantHash := sha256.Sum256(original)
	if meta.SHA256 != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("sha256 不匹配")
	}
	if len(meta.Snippet) != 400 {
		t.Fatalf("snippet 应该是 400 字节, 实际 %d", len(meta.Snippet))
	}

	// 2) 落盘文件存在 + 内容一致
	disk, err := os.ReadFile(meta.Path)
	if err != nil {
		t.Fatalf("落盘文件应该存在: %v", err)
	}
	if string(disk) != string(original) {
		t.Fatal("落盘内容应该等于 original")
	}
	if !strings.HasPrefix(meta.Path, filepath.Join(dir, "eng-large")) {
		t.Fatalf("路径应该在 baseDir/engagementID 下, 实际: %s", meta.Path)
	}

	// 3) Summary = snippet[:200]
	if len(res.Summary) != 200 {
		t.Fatalf("Summary 应该是 200 字节, 实际 %d", len(res.Summary))
	}
	if res.Summary != meta.Snippet[:200] {
		t.Fatal("Summary 应该等于 snippet 前 200 字节")
	}
}

func TestResultCompress_DiskFailure(t *testing.T) {
	// 用一个被文件占据的路径作为 baseDir → mkdir 必然失败。
	tmp := t.TempDir()
	conflict := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(conflict, []byte("x"), 0o644); err != nil {
		t.Fatalf("准备测试失败: %v", err)
	}

	mw := ResultCompress("any", conflict)
	original := makeBigOutput(5 * 1024)
	exec := mw(stubExecutor(original, "fallback summary"))

	res, err := exec(context.Background(), "scan", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("落盘失败时不应该向上抛错: %v", err)
	}
	if string(res.Output) != string(original) {
		t.Fatal("落盘失败时应该原样返回 Output")
	}
	if res.Summary != "fallback summary" {
		t.Fatalf("落盘失败时 Summary 不应被修改, 实际: %q", res.Summary)
	}
}

func TestResultCompress_SequentialNumbering(t *testing.T) {
	dir := t.TempDir()
	mw := ResultCompress("eng-seq", dir)
	exec := mw(stubExecutor(makeBigOutput(3*1024), ""))

	paths := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		res, err := exec(context.Background(), "scan", nil)
		if err != nil {
			t.Fatalf("call %d 失败: %v", i, err)
		}
		var meta struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(res.Output, &meta)
		paths = append(paths, meta.Path)
	}
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			t.Fatalf("路径应该带递增序号避免冲突, 重复路径: %s", p)
		}
		seen[p] = true
	}
}
