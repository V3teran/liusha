package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/V3teran/liusha/internal/toolruntime"
)

// makeBigOutput 构造指定大小的可读字节（重复 ASCII 模式）。
func makeBigOutput(size int) []byte {
	b := make([]byte, size)
	for i := range b {
		b[i] = byte('A' + (i % 26))
	}
	return b
}

// stubExecutor 返回固定 Result 的 base executor，用于驱动中间件测试。
func stubExecutor(out []byte, summary string) toolfx.ActionExecutor {
	return func(_ context.Context, _ string, _ json.RawMessage) (toolfx.Result, error) {
		return toolfx.Result{Output: out, Summary: summary}, nil
	}
}

func TestResultCompress_SmallNotTruncated(t *testing.T) {
	// threshold=2KB / snippet=512 / summary=128
	mw := ResultCompress(2048, 512, 128)

	original := makeBigOutput(1024) // 1KB <= 2KB 阈值 → 透传
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
}

func TestResultCompress_LargeTruncated(t *testing.T) {
	// threshold=2KB / snippet=512 / summary=128
	mw := ResultCompress(2048, 512, 128)

	original := makeBigOutput(5 * 1024) // 5KB > 2KB → 截断
	exec := mw(stubExecutor(original, "原 summary"))

	res, err := exec(context.Background(), "leak", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("不应该出错: %v", err)
	}

	// 1) Output 被截断到 snippet 字节
	if len(res.Output) != 512 {
		t.Fatalf("Output 应该是 snippet=512 字节, 实际 %d", len(res.Output))
	}
	if string(res.Output) != string(original[:512]) {
		t.Fatal("Output 截断内容应该是原 output 前 512 字节")
	}

	// 2) Summary 被替换为前 summary 字节（覆盖 caller 给的"原 summary"）
	if len(res.Summary) != 128 {
		t.Fatalf("Summary 应该是 summary=128 字节, 实际 %d", len(res.Summary))
	}
	if res.Summary != string(original[:128]) {
		t.Fatal("Summary 应该等于截断 Output 前 128 字节")
	}
}

func TestResultCompress_ZeroParamsUseFallback(t *testing.T) {
	// 三个零参 → 使用 fallback (64KB / 16KB / 1KB)
	mw := ResultCompress(0, 0, 0)

	// 100KB > 64KB threshold
	original := makeBigOutput(100 * 1024)
	exec := mw(stubExecutor(original, ""))

	res, err := exec(context.Background(), "scan", nil)
	if err != nil {
		t.Fatalf("不应该出错: %v", err)
	}
	if len(res.Output) != 16*1024 {
		t.Fatalf("Output 应该是 fallback snippet=16KB, 实际 %d", len(res.Output))
	}
	if len(res.Summary) != 1024 {
		t.Fatalf("Summary 应该是 fallback summary=1KB, 实际 %d", len(res.Summary))
	}
}

func TestResultCompress_PreservesDoneFlag(t *testing.T) {
	mw := ResultCompress(2048, 512, 128)

	exec := mw(func(_ context.Context, _ string, _ json.RawMessage) (toolfx.Result, error) {
		return toolfx.Result{Output: makeBigOutput(5 * 1024), Done: true}, nil
	})

	res, err := exec(context.Background(), "done_tool", nil)
	if err != nil {
		t.Fatalf("不应该出错: %v", err)
	}
	if !res.Done {
		t.Fatal("截断时 Done 标志应该保留")
	}
}

func TestResultCompress_ErrorPassthrough(t *testing.T) {
	mw := ResultCompress(2048, 512, 128)

	expectedErr := errors.New("upstream tool failure")
	exec := mw(func(_ context.Context, _ string, _ json.RawMessage) (toolfx.Result, error) {
		return toolfx.Result{Output: makeBigOutput(5 * 1024)}, expectedErr
	})

	_, err := exec(context.Background(), "scan", nil)
	if err == nil {
		t.Fatal("err 应该被透传，不被截断逻辑吞掉")
	}
	if err.Error() != expectedErr.Error() {
		t.Fatalf("err 内容应该原样返回, 实际: %v", err)
	}
}
