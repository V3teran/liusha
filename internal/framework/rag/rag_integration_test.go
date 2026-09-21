package rag_test

import (
	"context"
	"os"
	"testing"

	"github.com/V3teran/liusha/internal/framework/rag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenAIEmbedding 测试 OpenAI Embedding（需要 API Key）
func TestOpenAIEmbedding(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set, skipping OpenAI embedding test")
	}

	ctx := context.Background()
	embedding := rag.NewOpenAIEmbedding(apiKey, "text-embedding-3-small")

	// 测试单个文本
	text := "Hello, world!"
	vector, err := embedding.EmbedText(ctx, text)
	require.NoError(t, err)
	assert.Len(t, vector, 1536, "text-embedding-3-small should return 1536 dimensions")

	// 测试批量文本
	texts := []string{"Hello", "World", "OpenAI"}
	vectors, err := embedding.EmbedTexts(ctx, texts)
	require.NoError(t, err)
	assert.Len(t, vectors, 3)
	for _, v := range vectors {
		assert.Len(t, v, 1536)
	}

	// 测试维度
	assert.Equal(t, 1536, embedding.Dimension())
}

// TestPostgresVectorStore 测试 Postgres 向量存储（需要 Postgres+pgvector）
func TestPostgresVectorStore(t *testing.T) {
	connString := os.Getenv("POSTGRES_CONN_STRING")
	if connString == "" {
		t.Skip("POSTGRES_CONN_STRING not set, skipping Postgres vector store test")
	}

	ctx := context.Background()

	// 创建向量存储
	config := rag.PostgresVectorStoreConfig{
		ConnString: connString,
		TableName:  "test_documents",
		Dimension:  1536,
	}

	store, err := rag.NewPostgresVectorStore(ctx, config)
	require.NoError(t, err)
	defer store.Close()

	// 清空测试数据
	err = store.Clear(ctx)
	require.NoError(t, err)

	// 准备测试文档
	documents := []rag.Document{
		{Content: "The quick brown fox jumps over the lazy dog", Metadata: map[string]any{"type": "sentence"}},
		{Content: "Machine learning is a subset of artificial intelligence", Metadata: map[string]any{"type": "definition"}},
		{Content: "Python is a popular programming language", Metadata: map[string]any{"type": "statement"}},
	}

	// 生成模拟向量（实际应该使用真实 Embedding）
	vectors := make([][]float64, len(documents))
	for i := range documents {
		vectors[i] = make([]float64, 1536)
		for j := range vectors[i] {
			vectors[i][j] = float64(i+1) * 0.1 // 简单模拟
		}
	}

	// 添加文档
	err = store.Add(ctx, documents, vectors)
	require.NoError(t, err)

	// 搜索
	queryVector := make([]float64, 1536)
	for i := range queryVector {
		queryVector[i] = 0.1 // 与第一个文档相似
	}

	results, err := store.Search(ctx, queryVector, 2)
	require.NoError(t, err)
	assert.NotEmpty(t, results)
	assert.LessOrEqual(t, len(results), 2)

	// 清理
	err = store.Clear(ctx)
	require.NoError(t, err)
}

// TestPostgresRetriever 测试 Postgres 检索器（需要 API Key 和数据库）
func TestPostgresRetriever(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	connString := os.Getenv("POSTGRES_CONN_STRING")

	if apiKey == "" || connString == "" {
		t.Skip("OPENAI_API_KEY or POSTGRES_CONN_STRING not set, skipping retriever test")
	}

	ctx := context.Background()

	// 创建 Embedding
	embedding := rag.NewOpenAIEmbedding(apiKey, "text-embedding-3-small")

	// 创建向量存储
	config := rag.PostgresVectorStoreConfig{
		ConnString: connString,
		TableName:  "test_retriever",
		Dimension:  1536,
	}

	store, err := rag.NewPostgresVectorStore(ctx, config)
	require.NoError(t, err)
	defer store.Close()

	// 清空测试数据
	err = store.Clear(ctx)
	require.NoError(t, err)

	// 创建检索器
	retriever := rag.NewPostgresRetriever(embedding, store)

	// 添加文档
	documents := []rag.Document{
		{Content: "Go is a statically typed, compiled programming language designed at Google", Metadata: map[string]any{"topic": "golang"}},
		{Content: "Python is an interpreted, high-level programming language", Metadata: map[string]any{"topic": "python"}},
		{Content: "Rust is a multi-paradigm programming language focused on safety and performance", Metadata: map[string]any{"topic": "rust"}},
	}

	err = retriever.AddDocuments(ctx, documents)
	require.NoError(t, err)

	// 检索
	results, err := retriever.Retrieve(ctx, "What is Go programming language?", 2)
	require.NoError(t, err)
	assert.NotEmpty(t, results)
	assert.LessOrEqual(t, len(results), 2)

	// 第一个结果应该是关于 Go 的文档
	if len(results) > 0 {
		assert.Contains(t, results[0].Content, "Go")
		assert.Greater(t, results[0].Score, 0.0)
	}

	// 带分数过滤
	results, err = retriever.RetrieveWithScore(ctx, "Python programming", 3, 0.5)
	require.NoError(t, err)
	for _, doc := range results {
		assert.GreaterOrEqual(t, doc.Score, 0.5)
	}

	// 清理
	err = store.Clear(ctx)
	require.NoError(t, err)
}

// TestRAGWorkflow 测试完整 RAG 工作流
func TestRAGWorkflow(t *testing.T) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	connString := os.Getenv("POSTGRES_CONN_STRING")

	if apiKey == "" || connString == "" {
		t.Skip("OPENAI_API_KEY or POSTGRES_CONN_STRING not set, skipping RAG workflow test")
	}

	ctx := context.Background()

	// 1. 加载文档
	loader := rag.NewTextLoader("testdata/sample.txt")
	documents, err := loader.Load(ctx)
	if err != nil {
		// 如果文件不存在，使用模拟数据
		documents = []rag.Document{
			{Content: "Artificial intelligence (AI) is intelligence demonstrated by machines"},
			{Content: "Machine learning is a method of data analysis that automates analytical model building"},
			{Content: "Deep learning is part of a broader family of machine learning methods"},
		}
	}

	// 2. 分割文档
	splitter := rag.NewFixedLengthSplitter(200, 50)
	chunks := splitter.SplitDocuments(documents)
	assert.NotEmpty(t, chunks)

	// 3. 创建 Embedding
	embedding := rag.NewOpenAIEmbedding(apiKey, "text-embedding-3-small")

	// 4. 创建向量存储
	config := rag.PostgresVectorStoreConfig{
		ConnString: connString,
		TableName:  "test_rag_workflow",
		Dimension:  1536,
	}

	store, err := rag.NewPostgresVectorStore(ctx, config)
	require.NoError(t, err)
	defer store.Close()

	err = store.Clear(ctx)
	require.NoError(t, err)

	// 5. 创建检索器并添加文档
	retriever := rag.NewPostgresRetriever(embedding, store)
	err = retriever.AddDocuments(ctx, chunks)
	require.NoError(t, err)

	// 6. 检索
	query := "What is machine learning?"
	results, err := retriever.Retrieve(ctx, query, 3)
	require.NoError(t, err)
	assert.NotEmpty(t, results)

	// 验证结果质量
	for _, doc := range results {
		assert.NotEmpty(t, doc.Content)
		assert.Greater(t, doc.Score, 0.0)
	}

	// 清理
	err = store.Clear(ctx)
	require.NoError(t, err)
}
