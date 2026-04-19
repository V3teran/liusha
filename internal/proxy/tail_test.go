package proxy_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/proxy"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestTailer_ParsesLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flows.jsonl")

	// proxify JSONL 样本：body 字段为 base64 编码
	line := `{"timestamp":"2024-02-20T01:56:49+05:30","url":"https://example.com/api","request":{"method":"GET","header":{"Accept":"*/*"},"body":"aGVsbG8="},"response":{"status_code":200,"header":{"Content-Type":"application/json"},"body":"e30="}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(line), 0o644))

	engID := uuid.New()
	ch := make(chan proxy.ParsedFlow, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go proxy.TailJSONL(ctx, path, engID, ch)

	select {
	case pf := <-ch:
		require.Equal(t, "https://example.com/api", pf.URL)
		require.Equal(t, "GET", pf.Method)
		require.Equal(t, 200, pf.RespStatus)
		require.Equal(t, []byte("hello"), pf.ReqBody)   // base64 "aGVsbG8=" = "hello"
		require.Equal(t, []byte("{}"), pf.RespBody)     // base64 "e30=" = "{}"
		require.Equal(t, engID, pf.EngagementID)
	case <-ctx.Done():
		t.Fatal("超时未收到解析结果")
	}
}

func TestTailer_WaitsForFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "flows.jsonl")

	engID := uuid.New()
	ch := make(chan proxy.ParsedFlow, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go proxy.TailJSONL(ctx, path, engID, ch)

	// 文件稍后创建
	time.Sleep(300 * time.Millisecond)
	line := `{"timestamp":"2024-02-20T01:56:49Z","url":"https://example.com/late","request":{"method":"POST","header":{}},"response":{"status_code":201}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(line), 0o644))

	select {
	case pf := <-ch:
		require.Equal(t, "https://example.com/late", pf.URL)
	case <-ctx.Done():
		t.Fatal("超时：文件写入后未能读到数据")
	}
}
