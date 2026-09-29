package rag

import (
	"context"
	"os"
	"testing"
)

func TestTextLoader(t *testing.T) {
	ctx := context.Background()

	t.Run("加载文本文件", func(t *testing.T) {
		// 创建临时文件
		tmpFile, err := os.CreateTemp("", "test-*.txt")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Remove(tmpFile.Name()) }()

		content := "这是测试内容\n第二行"
		if _, err := tmpFile.WriteString(content); err != nil {
			t.Fatal(err)
		}
		_ = tmpFile.Close()

		// 加载
		loader := NewTextLoader(tmpFile.Name())
		docs, err := loader.Load(ctx)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if len(docs) != 1 {
			t.Errorf("len(docs) = %d, want 1", len(docs))
		}

		if docs[0].Content != content {
			t.Errorf("Content = %q, want %q", docs[0].Content, content)
		}

		if docs[0].Metadata["source"] != tmpFile.Name() {
			t.Errorf("source metadata not set")
		}
	})

	t.Run("文件不存在", func(t *testing.T) {
		loader := NewTextLoader("/nonexistent/file.txt")
		_, err := loader.Load(ctx)
		if err == nil {
			t.Error("Load() should return error for nonexistent file")
		}
	})
}
