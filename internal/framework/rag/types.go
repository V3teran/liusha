package rag

import (
	"context"
	"math"
)

// Document 文档（检索的基本单元）
type Document struct {
	// 文档内容
	Content string `json:"content"`

	// 文档元数据
	Metadata map[string]any `json:"metadata,omitempty"`

	// 文档 ID（可选）
	ID string `json:"id,omitempty"`

	// 相似度分数（检索结果中使用）
	Score float64 `json:"score,omitempty"`
}

// Embedding 向量接口
type Embedding interface {
	// EmbedText 将文本转换为向量
	EmbedText(ctx context.Context, text string) ([]float64, error)

	// EmbedTexts 批量转换文本为向量
	EmbedTexts(ctx context.Context, texts []string) ([][]float64, error)

	// Dimension 返回向量维度
	Dimension() int
}

// Retriever 检索器接口
type Retriever interface {
	// Retrieve 检索相关文档
	Retrieve(ctx context.Context, query string, topK int) ([]Document, error)

	// RetrieveWithScore 检索相关文档（包含分数）
	RetrieveWithScore(ctx context.Context, query string, topK int) ([]Document, error)
}

// VectorStore 向量存储接口
type VectorStore interface {
	// Add 添加文档及其向量
	Add(ctx context.Context, documents []Document, vectors [][]float64) error

	// Search 向量相似度搜索
	Search(ctx context.Context, queryVector []float64, topK int) ([]Document, error)

	// Delete 删除文档
	Delete(ctx context.Context, ids []string) error

	// Clear 清空存储
	Clear(ctx context.Context) error
}

// ─────────────────────────────────────────────
//  向量相似度计算
// ─────────────────────────────────────────────

// CosineSimilarity 余弦相似度
func CosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0.0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0.0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// EuclideanDistance 欧几里得距离
func EuclideanDistance(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0.0
	}

	var sum float64
	for i := range a {
		diff := a[i] - b[i]
		sum += diff * diff
	}

	return sum // 返回平方距离（避免开方）
}

// DotProduct 点积
func DotProduct(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0.0
	}

	var sum float64
	for i := range a {
		sum += a[i] * b[i]
	}

	return sum
}
