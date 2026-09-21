package rag_test

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/framework/rag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockCrossEncoder 模拟交叉编码器
type MockCrossEncoder struct{}

func (m *MockCrossEncoder) Score(ctx context.Context, query string, document string) (float64, error) {
	// 简单模拟：根据文档长度和查询匹配度返回分数
	score := 0.5
	// 这里可以实现更复杂的逻辑
	return score, nil
}

func (m *MockCrossEncoder) BatchScore(ctx context.Context, query string, documents []string) ([]float64, error) {
	scores := make([]float64, len(documents))
	for i := range documents {
		// 模拟：包含查询词的文档得分更高
		if containsQuery(documents[i], query) {
			scores[i] = 0.9 - float64(i)*0.05 // 越靠前的文档分数越高
		} else {
			scores[i] = 0.3 - float64(i)*0.05
		}
	}
	return scores, nil
}

func containsQuery(text, query string) bool {
	// 简化的包含检查
	return len(text) > 0 && len(query) > 0
}

// TestCrossEncoderReranker 测试交叉编码器重排序
func TestCrossEncoderReranker(t *testing.T) {
	ctx := context.Background()

	// 创建重排序器
	encoder := &MockCrossEncoder{}
	reranker := rag.NewCrossEncoderReranker(encoder)

	// 准备文档（原始顺序不是最优的）
	documents := []rag.Document{
		{ID: "1", Content: "irrelevant document about cats", Score: 0.8},
		{ID: "2", Content: "machine learning tutorial for beginners", Score: 0.7},
		{ID: "3", Content: "advanced machine learning techniques", Score: 0.6},
		{ID: "4", Content: "random document about food", Score: 0.5},
	}

	// 重排序
	reranked, err := reranker.Rerank(ctx, "machine learning", documents)
	require.NoError(t, err)
	assert.Len(t, reranked, 4)

	// 验证重排序后的顺序（分数应该更新）
	for i := 0; i < len(reranked)-1; i++ {
		assert.GreaterOrEqual(t, reranked[i].Score, reranked[i+1].Score, "文档应按分数降序排列")
	}
}

// TestEmbeddingReranker 测试基于嵌入的重排序
func TestEmbeddingReranker(t *testing.T) {
	ctx := context.Background()

	// 使用 MockEmbedding
	embedding := rag.NewMockEmbedding(128)
	reranker := rag.NewEmbeddingReranker(embedding)

	documents := []rag.Document{
		{ID: "1", Content: "Go programming language", Score: 0.5},
		{ID: "2", Content: "Python programming", Score: 0.6},
		{ID: "3", Content: "Rust systems programming", Score: 0.7},
	}

	// 重排序
	reranked, err := reranker.Rerank(ctx, "Go language", documents)
	require.NoError(t, err)
	assert.Len(t, reranked, 3)

	// 验证分数已更新
	for _, doc := range reranked {
		assert.Greater(t, doc.Score, 0.0)
	}
}

// TestCompositeReranker 测试组合重排序器
func TestCompositeReranker(t *testing.T) {
	ctx := context.Background()

	// 创建两级重排序器
	embedding := rag.NewMockEmbedding(128)
	reranker1 := rag.NewEmbeddingReranker(embedding)

	encoder := &MockCrossEncoder{}
	reranker2 := rag.NewCrossEncoderReranker(encoder)

	composite := rag.NewCompositeReranker(reranker1, reranker2)

	documents := []rag.Document{
		{ID: "1", Content: "first document", Score: 0.5},
		{ID: "2", Content: "second document", Score: 0.6},
		{ID: "3", Content: "third document", Score: 0.7},
	}

	// 多级重排序
	reranked, err := composite.Rerank(ctx, "test query", documents)
	require.NoError(t, err)
	assert.Len(t, reranked, 3)
}

// TestRetrievalWithRerank 测试检索 + 重排序管道
func TestRetrievalWithRerank(t *testing.T) {
	ctx := context.Background()

	// 准备测试数据
	documents := []rag.Document{
		{ID: "1", Content: "machine learning basics"},
		{ID: "2", Content: "deep learning networks"},
		{ID: "3", Content: "natural language processing"},
		{ID: "4", Content: "computer vision"},
		{ID: "5", Content: "reinforcement learning"},
	}

	// 创建 BM25 检索器
	bm25 := rag.NewBM25Retriever()
	err := bm25.Index(ctx, documents)
	require.NoError(t, err)

	// 创建重排序器
	embedding := rag.NewMockEmbedding(128)
	reranker := rag.NewEmbeddingReranker(embedding)

	// 创建检索 + 重排序管道
	pipeline := rag.NewRetrievalWithRerank(
		bm25,
		reranker,
		rag.RetrievalWithRerankConfig{
			TopK:   10, // 初始检索 10 个
			FinalK: 3,  // 最终返回 3 个
		},
	)

	// 执行检索 + 重排序
	results, err := pipeline.Retrieve(ctx, "machine learning", 3)
	require.NoError(t, err)
	assert.NotEmpty(t, results)
	assert.LessOrEqual(t, len(results), 3)

	// 验证结果按分数排序
	for i := 0; i < len(results)-1; i++ {
		assert.GreaterOrEqual(t, results[i].Score, results[i+1].Score)
	}
}

// TestRerankerWithEmptyDocuments 测试空文档列表
func TestRerankerWithEmptyDocuments(t *testing.T) {
	ctx := context.Background()

	embedding := rag.NewMockEmbedding(128)
	reranker := rag.NewEmbeddingReranker(embedding)

	// 空文档列表
	documents := []rag.Document{}

	reranked, err := reranker.Rerank(ctx, "test", documents)
	require.NoError(t, err)
	assert.Empty(t, reranked)
}

// TestRerankerScoreUpdate 测试重排序后分数更新
func TestRerankerScoreUpdate(t *testing.T) {
	ctx := context.Background()

	embedding := rag.NewMockEmbedding(128)
	reranker := rag.NewEmbeddingReranker(embedding)

	documents := []rag.Document{
		{ID: "1", Content: "document one", Score: 0.1},
		{ID: "2", Content: "document two", Score: 0.2},
	}

	reranked, err := reranker.Rerank(ctx, "query", documents)
	require.NoError(t, err)

	// 验证分数已更新（不再是原始的 0.1, 0.2）
	for i, doc := range reranked {
		// 重排序后分数应该改变
		assert.NotEqual(t, documents[i].Score, doc.Score, "重排序应该更新分数")
	}
}

// TestRetrievalWithRerankTopK 测试 TopK 参数
func TestRetrievalWithRerankTopK(t *testing.T) {
	ctx := context.Background()

	documents := []rag.Document{
		{ID: "1", Content: "doc 1"},
		{ID: "2", Content: "doc 2"},
		{ID: "3", Content: "doc 3"},
		{ID: "4", Content: "doc 4"},
		{ID: "5", Content: "doc 5"},
	}

	bm25 := rag.NewBM25Retriever()
	err := bm25.Index(ctx, documents)
	require.NoError(t, err)

	embedding := rag.NewMockEmbedding(128)
	reranker := rag.NewEmbeddingReranker(embedding)

	pipeline := rag.NewRetrievalWithRerank(
		bm25,
		reranker,
		rag.RetrievalWithRerankConfig{
			TopK:   10,
			FinalK: 2,
		},
	)

	// 请求 2 个结果
	results, err := pipeline.Retrieve(ctx, "test", 2)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(results), 2)

	// 请求 10 个结果（超过文档总数）
	results, err = pipeline.Retrieve(ctx, "test", 10)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(results), 5) // 不会超过文档总数
}
