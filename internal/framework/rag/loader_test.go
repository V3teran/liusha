package rag

import (
	"context"
	"os"
	"strings"
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
		defer os.Remove(tmpFile.Name())

		content := "这是测试内容\n第二行"
		if _, err := tmpFile.WriteString(content); err != nil {
			t.Fatal(err)
		}
		tmpFile.Close()

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

	t.Run("带元数据加载", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test-*.txt")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tmpFile.Name())

		tmpFile.WriteString("测试")
		tmpFile.Close()

		loader := NewTextLoader(tmpFile.Name()).
			WithMetadata("author", "测试").
			WithMetadata("version", "1.0")

		docs, err := loader.Load(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if docs[0].Metadata["author"] != "测试" {
			t.Error("author metadata not set")
		}
		if docs[0].Metadata["version"] != "1.0" {
			t.Error("version metadata not set")
		}
	})
}

func TestReaderLoader(t *testing.T) {
	ctx := context.Background()

	t.Run("从 Reader 加载", func(t *testing.T) {
		content := "测试内容"
		reader := strings.NewReader(content)

		loader := NewReaderLoader(reader)
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
	})
}

func TestRecursiveCharacterSplitter(t *testing.T) {
	t.Run("小于块大小不分割", func(t *testing.T) {
		splitter := NewRecursiveCharacterSplitter(100, 10)
		text := "短文本"

		chunks := splitter.Split(text)

		if len(chunks) != 1 {
			t.Errorf("len(chunks) = %d, want 1", len(chunks))
		}
		if chunks[0] != text {
			t.Errorf("chunk = %q, want %q", chunks[0], text)
		}
	})

	t.Run("按段落分割", func(t *testing.T) {
		splitter := NewRecursiveCharacterSplitter(30, 0)
		text := "第一段内容比较长，超过了块大小的限制。\n\n第二段内容也比较长，也超过了块大小。\n\n第三段内容。"

		chunks := splitter.Split(text)

		if len(chunks) < 2 {
			t.Errorf("len(chunks) = %d, want >= 2", len(chunks))
		}
	})

	t.Run("按句子分割", func(t *testing.T) {
		splitter := NewRecursiveCharacterSplitter(15, 5)
		text := "这是第一句话。这是第二句话。这是第三句话。"

		chunks := splitter.Split(text)

		if len(chunks) < 2 {
			t.Errorf("len(chunks) = %d, want >= 2", len(chunks))
		}
	})

	t.Run("带重叠分割", func(t *testing.T) {
		splitter := NewRecursiveCharacterSplitter(20, 5)
		text := "这是一段很长的测试文本内容，需要分割成多个块。"

		chunks := splitter.Split(text)

		if len(chunks) < 2 {
			t.Errorf("len(chunks) = %d, want >= 2", len(chunks))
		}

		// 验证重叠：第二块的开头应该包含第一块的结尾
		if len(chunks) >= 2 {
			firstEnd := []rune(chunks[0])
			if len(firstEnd) >= 5 {
				overlap := string(firstEnd[len(firstEnd)-5:])
				if !strings.HasPrefix(chunks[1], overlap) {
					t.Log("第一块:", chunks[0])
					t.Log("第二块:", chunks[1])
					t.Log("期望重叠:", overlap)
				}
			}
		}
	})

	t.Run("自定义分隔符", func(t *testing.T) {
		splitter := NewRecursiveCharacterSplitter(8, 0).
			WithSeparators([]string{"|", " "})

		text := "部分1内容较长|部分2内容较长|部分3内容较长"
		chunks := splitter.Split(text)

		if len(chunks) < 2 {
			t.Errorf("len(chunks) = %d, want >= 2", len(chunks))
		}
	})

	t.Run("分割文档", func(t *testing.T) {
		splitter := NewRecursiveCharacterSplitter(20, 0)

		docs := []Document{
			{
				Content:  "这是第一个文档的内容，比较长，需要分割。",
				Metadata: map[string]any{"id": "1"},
			},
			{
				Content:  "这是第二个文档的内容，也比较长，也需要分割。",
				Metadata: map[string]any{"id": "2"},
			},
		}

		result := splitter.SplitDocuments(docs)

		if len(result) < 2 {
			t.Errorf("len(result) = %d, want >= 2", len(result))
		}

		// 验证元数据继承
		if result[0].Metadata["id"] != "1" {
			t.Error("元数据未继承")
		}

		// 验证块索引
		if _, ok := result[0].Metadata["chunk_index"]; !ok {
			t.Error("chunk_index 未设置")
		}
	})
}

func TestFixedLengthSplitter(t *testing.T) {
	t.Run("固定长度分割", func(t *testing.T) {
		splitter := NewFixedLengthSplitter(10, 0)
		text := "这是一段测试文本内容"

		chunks := splitter.Split(text)

		for i, chunk := range chunks {
			runeCount := len([]rune(chunk))
			if i < len(chunks)-1 && runeCount != 10 {
				t.Errorf("chunk[%d] length = %d, want 10", i, runeCount)
			}
		}
	})

	t.Run("带重叠的固定长度分割", func(t *testing.T) {
		splitter := NewFixedLengthSplitter(10, 3)
		text := "这是一段很长的测试文本内容，用于测试重叠功能。"

		chunks := splitter.Split(text)

		if len(chunks) < 2 {
			t.Errorf("len(chunks) = %d, want >= 2", len(chunks))
		}

		// 验证步长
		for i, chunk := range chunks {
			t.Logf("chunk[%d]: %q (len=%d)", i, chunk, len([]rune(chunk)))
		}
	})

	t.Run("分割文档", func(t *testing.T) {
		splitter := NewFixedLengthSplitter(15, 5)

		docs := []Document{
			{
				Content:  "这是一段需要分割的长文本内容测试",
				Metadata: map[string]any{"source": "test"},
			},
		}

		result := splitter.SplitDocuments(docs)

		if len(result) < 1 {
			t.Error("no chunks generated")
		}

		// 验证元数据
		if result[0].Metadata["source"] != "test" {
			t.Error("元数据未继承")
		}

		if result[0].Metadata["chunk_index"] != 0 {
			t.Error("chunk_index 应该为 0")
		}
	})
}

func TestSplitterEdgeCases(t *testing.T) {
	t.Run("空文本", func(t *testing.T) {
		splitter := NewRecursiveCharacterSplitter(100, 10)
		chunks := splitter.Split("")

		if len(chunks) != 1 || chunks[0] != "" {
			t.Error("空文本应返回单个空块")
		}
	})

	t.Run("单字符文本", func(t *testing.T) {
		splitter := NewRecursiveCharacterSplitter(100, 10)
		chunks := splitter.Split("a")

		if len(chunks) != 1 || chunks[0] != "a" {
			t.Error("单字符文本应返回单块")
		}
	})

	t.Run("中文字符处理", func(t *testing.T) {
		splitter := NewRecursiveCharacterSplitter(5, 0)
		text := "我爱编程语言"

		chunks := splitter.Split(text)

		// 验证中文按字符数正确分割
		for _, chunk := range chunks {
			runeCount := len([]rune(chunk))
			if runeCount > 5 {
				t.Errorf("chunk rune count = %d, want <= 5", runeCount)
			}
		}
	})

	t.Run("零重叠", func(t *testing.T) {
		splitter := NewFixedLengthSplitter(5, 0)
		text := "0123456789"

		chunks := splitter.Split(text)

		// 验证无重叠
		reconstructed := strings.Join(chunks, "")
		if reconstructed != text {
			t.Errorf("reconstructed = %q, want %q", reconstructed, text)
		}
	})
}
