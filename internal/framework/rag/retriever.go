package rag

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
)

// MemoryVectorStore 内存向量存储（用于测试和小规模场景）
type MemoryVectorStore struct {
	mu        sync.RWMutex
	documents []Document
	vectors   [][]float64
}

// NewMemoryVectorStore 创建内存向量存储
func NewMemoryVectorStore() *MemoryVectorStore {
	return &MemoryVectorStore{
		documents: make([]Document, 0),
		vectors:   make([][]float64, 0),
	}
}

// Add 添加文档及其向量
func (s *MemoryVectorStore) Add(ctx context.Context, documents []Document, vectors [][]float64) error {
	if len(documents) != len(vectors) {
		return fmt.Errorf("文档数量 (%d) 与向量数量 (%d) 不匹配", len(documents), len(vectors))
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.documents = append(s.documents, documents...)
	s.vectors = append(s.vectors, vectors...)

	return nil
}

// Search 向量相似度搜索
func (s *MemoryVectorStore) Search(ctx context.Context, queryVector []float64, topK int) ([]Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.documents) == 0 {
		return []Document{}, nil
	}

	// 计算所有文档的相似度
	type scoreDoc struct {
		doc   Document
		score float64
	}

	scores := make([]scoreDoc, len(s.documents))
	for i := range s.documents {
		similarity := CosineSimilarity(queryVector, s.vectors[i])
		scores[i] = scoreDoc{
			doc:   s.documents[i],
			score: similarity,
		}
	}

	// 按分数降序排序
	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	// 返回 topK 结果
	k := topK
	if k > len(scores) {
		k = len(scores)
	}

	results := make([]Document, k)
	for i := 0; i < k; i++ {
		results[i] = scores[i].doc
		results[i].Score = scores[i].score
	}

	return results, nil
}

// Delete 删除文档
func (s *MemoryVectorStore) Delete(ctx context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idSet := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}

	// 过滤出未被删除的文档
	newDocs := make([]Document, 0, len(s.documents))
	newVectors := make([][]float64, 0, len(s.vectors))

	for i, doc := range s.documents {
		if _, shouldDelete := idSet[doc.ID]; !shouldDelete {
			newDocs = append(newDocs, doc)
			newVectors = append(newVectors, s.vectors[i])
		}
	}

	s.documents = newDocs
	s.vectors = newVectors

	return nil
}

// Clear 清空存储
func (s *MemoryVectorStore) Clear(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.documents = make([]Document, 0)
	s.vectors = make([][]float64, 0)

	return nil
}

// Size 返回存储的文档数量
func (s *MemoryVectorStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.documents)
}

// ─────────────────────────────────────────────
//  向量检索器
// ─────────────────────────────────────────────

// VectorRetriever 基于向量存储的检索器
type VectorRetriever struct {
	store     VectorStore
	embedding Embedding
}

// NewVectorRetriever 创建向量检索器
func NewVectorRetriever(store VectorStore, embedding Embedding) *VectorRetriever {
	return &VectorRetriever{
		store:     store,
		embedding: embedding,
	}
}

// Retrieve 检索相关文档
func (r *VectorRetriever) Retrieve(ctx context.Context, query string, topK int) ([]Document, error) {
	// 将查询转换为向量
	queryVector, err := r.embedding.EmbedText(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("查询向量化失败: %w", err)
	}

	// 向量搜索
	return r.store.Search(ctx, queryVector, topK)
}

// RetrieveWithScore 检索相关文档（包含分数）
func (r *VectorRetriever) RetrieveWithScore(ctx context.Context, query string, topK int) ([]Document, error) {
	// VectorStore.Search 已经返回带分数的文档
	return r.Retrieve(ctx, query, topK)
}

// ─────────────────────────────────────────────
//  简单 Embedding 实现（用于测试）
// ─────────────────────────────────────────────

// MockEmbedding 模拟 Embedding（用于测试）
type MockEmbedding struct {
	dimension int
}

// NewMockEmbedding 创建模拟 Embedding
func NewMockEmbedding(dimension int) *MockEmbedding {
	return &MockEmbedding{dimension: dimension}
}

// EmbedText 将文本转换为向量（简单哈希）
func (e *MockEmbedding) EmbedText(ctx context.Context, text string) ([]float64, error) {
	vector := make([]float64, e.dimension)

	// 简单的字符哈希方法
	for i, char := range text {
		idx := i % e.dimension
		vector[idx] += float64(char)
	}

	// 归一化
	var norm float64
	for _, v := range vector {
		norm += v * v
	}
	norm = math.Sqrt(norm)

	if norm > 0 {
		for i := range vector {
			vector[i] /= norm
		}
	}

	return vector, nil
}

// EmbedTexts 批量转换文本为向量
func (e *MockEmbedding) EmbedTexts(ctx context.Context, texts []string) ([][]float64, error) {
	vectors := make([][]float64, len(texts))
	for i, text := range texts {
		vec, err := e.EmbedText(ctx, text)
		if err != nil {
			return nil, err
		}
		vectors[i] = vec
	}
	return vectors, nil
}

// Dimension 返回向量维度
func (e *MockEmbedding) Dimension() int {
	return e.dimension
}
