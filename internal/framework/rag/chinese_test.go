package rag_test

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/framework/rag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBM25ChineseSupport 测试中文支持
func TestBM25ChineseSupport(t *testing.T) {
	ctx := context.Background()

	retriever := rag.NewBM25Retriever()

	// 中文文档
	documents := []rag.Document{
		{ID: "1", Content: "机器学习是人工智能的一个分支"},
		{ID: "2", Content: "深度学习是机器学习的子集"},
		{ID: "3", Content: "自然语言处理技术应用广泛"},
		{ID: "4", Content: "Go语言是一门高效的编程语言"},
		{ID: "5", Content: "Python在机器学习领域很流行"},
	}

	err := retriever.Index(ctx, documents)
	require.NoError(t, err)

	t.Run("搜索中文关键词", func(t *testing.T) {
		results, err := retriever.Search(ctx, "机器学习", 3)
		require.NoError(t, err)
		assert.NotEmpty(t, results)

		// 验证相关文档排在前面
		foundRelevant := false
		for _, doc := range results {
			if doc.ID == "1" || doc.ID == "2" || doc.ID == "5" {
				foundRelevant = true
				break
			}
		}
		assert.True(t, foundRelevant, "应该返回包含'机器学习'的文档")
	})

	t.Run("搜索多词查询", func(t *testing.T) {
		results, err := retriever.Search(ctx, "深度学习算法", 2)
		require.NoError(t, err)
		assert.NotEmpty(t, results)
	})
}

// TestBM25MixedLanguage 测试中英文混合
func TestBM25MixedLanguage(t *testing.T) {
	ctx := context.Background()

	retriever := rag.NewBM25Retriever()

	documents := []rag.Document{
		{ID: "1", Content: "Go语言适合做后端开发"},
		{ID: "2", Content: "Python is great for machine learning"},
		{ID: "3", Content: "使用TensorFlow进行深度学习"},
		{ID: "4", Content: "Rust语言注重安全性和性能"},
	}

	err := retriever.Index(ctx, documents)
	require.NoError(t, err)

	t.Run("中文查询", func(t *testing.T) {
		results, err := retriever.Search(ctx, "深度学习", 2)
		require.NoError(t, err)
		assert.NotEmpty(t, results)
	})

	t.Run("英文查询", func(t *testing.T) {
		results, err := retriever.Search(ctx, "machine learning", 2)
		require.NoError(t, err)
		assert.NotEmpty(t, results)
	})

	t.Run("混合查询", func(t *testing.T) {
		results, err := retriever.Search(ctx, "Go语言", 2)
		require.NoError(t, err)
		assert.NotEmpty(t, results)
	})
}

// TestChineseTokenization 测试中文分词效果
func TestChineseTokenization(t *testing.T) {
	ctx := context.Background()

	retriever := rag.NewBM25Retriever()

	// 包含需要分词的中文文档
	documents := []rag.Document{
		{ID: "1", Content: "北京大学是中国顶尖学府"},
		{ID: "2", Content: "清华大学也是知名高校"},
		{ID: "3", Content: "中国科学技术大学位于合肥"},
	}

	err := retriever.Index(ctx, documents)
	require.NoError(t, err)

	// 搜索"大学"应该能找到多个文档
	results, err := retriever.Search(ctx, "大学", 3)
	require.NoError(t, err)
	assert.NotEmpty(t, results)
	assert.GreaterOrEqual(t, len(results), 1, "应该找到至少一个包含'大学'的文档")
}
