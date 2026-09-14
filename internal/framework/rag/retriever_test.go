package rag

import (
	"context"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a        []float64
		b        []float64
		expected float64
	}{
		{
			name:     "相同向量",
			a:        []float64{1, 0, 0},
			b:        []float64{1, 0, 0},
			expected: 1.0,
		},
		{
			name:     "正交向量",
			a:        []float64{1, 0, 0},
			b:        []float64{0, 1, 0},
			expected: 0.0,
		},
		{
			name:     "相反向量",
			a:        []float64{1, 0, 0},
			b:        []float64{-1, 0, 0},
			expected: -1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CosineSimilarity(tt.a, tt.b)
			if got != tt.expected {
				t.Errorf("CosineSimilarity() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestMemoryVectorStore(t *testing.T) {
	ctx := context.Background()

	t.Run("添加和搜索", func(t *testing.T) {
		store := NewMemoryVectorStore()

		docs := []Document{
			{ID: "1", Content: "Go 编程语言"},
			{ID: "2", Content: "Python 编程语言"},
			{ID: "3", Content: "JavaScript 编程语言"},
		}

		vectors := [][]float64{
			{1.0, 0.0, 0.0},
			{0.8, 0.2, 0.0},
			{0.0, 1.0, 0.0},
		}

		err := store.Add(ctx, docs, vectors)
		if err != nil {
			t.Fatalf("Add() error = %v", err)
		}

		if store.Size() != 3 {
			t.Errorf("Size() = %d, want 3", store.Size())
		}

		// 搜索与第一个向量最相似的文档
		queryVector := []float64{0.9, 0.1, 0.0}
		results, err := store.Search(ctx, queryVector, 2)
		if err != nil {
			t.Fatalf("Search() error = %v", err)
		}

		if len(results) != 2 {
			t.Errorf("len(results) = %d, want 2", len(results))
		}

		// 第一个结果应该是最相似的
		if results[0].ID != "1" {
			t.Errorf("results[0].ID = %s, want 1", results[0].ID)
		}
	})

	t.Run("删除文档", func(t *testing.T) {
		store := NewMemoryVectorStore()

		docs := []Document{
			{ID: "1", Content: "文档1"},
			{ID: "2", Content: "文档2"},
			{ID: "3", Content: "文档3"},
		}

		vectors := [][]float64{
			{1.0, 0.0},
			{0.0, 1.0},
			{0.5, 0.5},
		}

		_ = store.Add(ctx, docs, vectors)

		err := store.Delete(ctx, []string{"2"})
		if err != nil {
			t.Fatalf("Delete() error = %v", err)
		}

		if store.Size() != 2 {
			t.Errorf("Size() = %d, want 2", store.Size())
		}
	})

	t.Run("清空存储", func(t *testing.T) {
		store := NewMemoryVectorStore()

		docs := []Document{{ID: "1", Content: "文档1"}}
		vectors := [][]float64{{1.0, 0.0}}

		_ = store.Add(ctx, docs, vectors)

		err := store.Clear(ctx)
		if err != nil {
			t.Fatalf("Clear() error = %v", err)
		}

		if store.Size() != 0 {
			t.Errorf("Size() = %d, want 0", store.Size())
		}
	})
}

func TestVectorRetriever(t *testing.T) {
	ctx := context.Background()

	t.Run("检索相关文档", func(t *testing.T) {
		// 创建存储和 Embedding
		store := NewMemoryVectorStore()
		embedding := NewMockEmbedding(128)

		// 添加文档
		docs := []Document{
			{ID: "1", Content: "Go 是一种编译型语言"},
			{ID: "2", Content: "Python 是一种解释型语言"},
			{ID: "3", Content: "Go 语言性能优秀"},
		}

		vectors, err := embedding.EmbedTexts(ctx, []string{
			docs[0].Content,
			docs[1].Content,
			docs[2].Content,
		})
		if err != nil {
			t.Fatalf("EmbedTexts() error = %v", err)
		}

		err = store.Add(ctx, docs, vectors)
		if err != nil {
			t.Fatalf("Add() error = %v", err)
		}

		// 创建检索器
		retriever := NewVectorRetriever(store, embedding)

		// 检索
		results, err := retriever.Retrieve(ctx, "Go 语言", 2)
		if err != nil {
			t.Fatalf("Retrieve() error = %v", err)
		}

		if len(results) != 2 {
			t.Errorf("len(results) = %d, want 2", len(results))
		}

		// 验证分数存在
		if results[0].Score == 0 {
			t.Error("results[0].Score should not be 0")
		}
	})
}

func TestMockEmbedding(t *testing.T) {
	ctx := context.Background()

	t.Run("向量维度", func(t *testing.T) {
		embedding := NewMockEmbedding(128)

		if embedding.Dimension() != 128 {
			t.Errorf("Dimension() = %d, want 128", embedding.Dimension())
		}
	})

	t.Run("文本向量化", func(t *testing.T) {
		embedding := NewMockEmbedding(64)

		vector, err := embedding.EmbedText(ctx, "测试文本")
		if err != nil {
			t.Fatalf("EmbedText() error = %v", err)
		}

		if len(vector) != 64 {
			t.Errorf("len(vector) = %d, want 64", len(vector))
		}

		// 验证归一化（向量模长接近 1）
		var norm float64
		for _, v := range vector {
			norm += v * v
		}

		if norm < 0.99 || norm > 1.01 {
			t.Errorf("向量未正确归一化，模长 = %f", norm)
		}
	})

	t.Run("批量向量化", func(t *testing.T) {
		embedding := NewMockEmbedding(32)

		texts := []string{"文本1", "文本2", "文本3"}
		vectors, err := embedding.EmbedTexts(ctx, texts)
		if err != nil {
			t.Fatalf("EmbedTexts() error = %v", err)
		}

		if len(vectors) != 3 {
			t.Errorf("len(vectors) = %d, want 3", len(vectors))
		}

		for i, vec := range vectors {
			if len(vec) != 32 {
				t.Errorf("vectors[%d] len = %d, want 32", i, len(vec))
			}
		}
	})
}
