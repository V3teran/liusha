package rag

import (
	"context"
	"fmt"
	"sort"
)

// Reranker 重排序器接口
type Reranker interface {
	// Rerank 对检索结果重新排序
	Rerank(ctx context.Context, query string, documents []Document) ([]Document, error)
}

// ──────────────────────────────────────────────────────
//  交叉编码器重排序（基于语义相似度）
// ──────────────────────────────────────────────────────

// CrossEncoderReranker 交叉编码器重排序器
type CrossEncoderReranker struct {
	encoder CrossEncoder
}

// CrossEncoder 交叉编码器接口
type CrossEncoder interface {
	// Score 计算查询和文档的相关性分数
	Score(ctx context.Context, query string, document string) (float64, error)

	// BatchScore 批量计算相关性分数
	BatchScore(ctx context.Context, query string, documents []string) ([]float64, error)
}

// NewCrossEncoderReranker 创建交叉编码器重排序器
func NewCrossEncoderReranker(encoder CrossEncoder) *CrossEncoderReranker {
	return &CrossEncoderReranker{
		encoder: encoder,
	}
}

// Rerank 重新排序
func (r *CrossEncoderReranker) Rerank(ctx context.Context, query string, documents []Document) ([]Document, error) {
	if len(documents) == 0 {
		return documents, nil
	}

	// 提取文档内容
	contents := make([]string, len(documents))
	for i, doc := range documents {
		contents[i] = doc.Content
	}

	// 批量计算相关性分数
	scores, err := r.encoder.BatchScore(ctx, query, contents)
	if err != nil {
		return nil, fmt.Errorf("batch scoring failed: %w", err)
	}

	if len(scores) != len(documents) {
		return nil, fmt.Errorf("score count mismatch: expected %d, got %d", len(documents), len(scores))
	}

	// 更新文档分数
	type scoredDoc struct {
		doc   Document
		score float64
	}

	scored := make([]scoredDoc, len(documents))
	for i, doc := range documents {
		doc.Score = scores[i]
		scored[i] = scoredDoc{doc: doc, score: scores[i]}
	}

	// 按分数降序排序
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// 提取排序后的文档
	reranked := make([]Document, len(scored))
	for i, s := range scored {
		reranked[i] = s.doc
	}

	return reranked, nil
}

// ──────────────────────────────────────────────────────
//  基于嵌入的重排序（余弦相似度）
// ──────────────────────────────────────────────────────

// EmbeddingReranker 基于嵌入的重排序器
type EmbeddingReranker struct {
	embedding Embedding
}

// NewEmbeddingReranker 创建基于嵌入的重排序器
func NewEmbeddingReranker(embedding Embedding) *EmbeddingReranker {
	return &EmbeddingReranker{
		embedding: embedding,
	}
}

// Rerank 重新排序
func (r *EmbeddingReranker) Rerank(ctx context.Context, query string, documents []Document) ([]Document, error) {
	if len(documents) == 0 {
		return documents, nil
	}

	// 生成查询向量
	queryVector, err := r.embedding.EmbedText(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to embed query: %w", err)
	}

	// 提取文档内容
	contents := make([]string, len(documents))
	for i, doc := range documents {
		contents[i] = doc.Content
	}

	// 批量生成文档向量
	docVectors, err := r.embedding.EmbedTexts(ctx, contents)
	if err != nil {
		return nil, fmt.Errorf("failed to embed documents: %w", err)
	}

	if len(docVectors) != len(documents) {
		return nil, fmt.Errorf("vector count mismatch: expected %d, got %d", len(documents), len(docVectors))
	}

	// 计算余弦相似度
	type scoredDoc struct {
		doc   Document
		score float64
	}

	scored := make([]scoredDoc, len(documents))
	for i, doc := range documents {
		similarity := CosineSimilarity(queryVector, docVectors[i])
		doc.Score = similarity
		scored[i] = scoredDoc{doc: doc, score: similarity}
	}

	// 按分数降序排序
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// 提取排序后的文档
	reranked := make([]Document, len(scored))
	for i, s := range scored {
		reranked[i] = s.doc
	}

	return reranked, nil
}

// ──────────────────────────────────────────────────────
//  组合重排序器
// ──────────────────────────────────────────────────────

// CompositeReranker 组合重排序器（多级重排序）
type CompositeReranker struct {
	rerankers []Reranker
}

// NewCompositeReranker 创建组合重排序器
func NewCompositeReranker(rerankers ...Reranker) *CompositeReranker {
	return &CompositeReranker{
		rerankers: rerankers,
	}
}

// Rerank 多级重排序
func (r *CompositeReranker) Rerank(ctx context.Context, query string, documents []Document) ([]Document, error) {
	current := documents

	for i, reranker := range r.rerankers {
		reranked, err := reranker.Rerank(ctx, query, current)
		if err != nil {
			return nil, fmt.Errorf("reranker %d failed: %w", i, err)
		}
		current = reranked
	}

	return current, nil
}

// ──────────────────────────────────────────────────────
//  检索 + 重排序管道
// ──────────────────────────────────────────────────────

// RetrievalWithRerank 检索 + 重排序管道
type RetrievalWithRerank struct {
	retriever Retriever
	reranker  Reranker
	topK      int // 初始检索数量
	finalK    int // 最终返回数量
}

// RetrievalWithRerankConfig 检索 + 重排序配置
type RetrievalWithRerankConfig struct {
	// TopK 初始检索数量（通常是最终数量的 2-5 倍）
	TopK int

	// FinalK 最终返回数量
	FinalK int
}

// NewRetrievalWithRerank 创建检索 + 重排序管道
func NewRetrievalWithRerank(retriever Retriever, reranker Reranker, config RetrievalWithRerankConfig) *RetrievalWithRerank {
	if config.TopK == 0 {
		config.TopK = config.FinalK * 3 // 默认检索 3 倍数量
	}

	return &RetrievalWithRerank{
		retriever: retriever,
		reranker:  reranker,
		topK:      config.TopK,
		finalK:    config.FinalK,
	}
}

// Retrieve 检索 + 重排序
func (r *RetrievalWithRerank) Retrieve(ctx context.Context, query string, topK int) ([]Document, error) {
	// 1. 初始检索（获取更多候选）
	initialTopK := topK * 3
	if initialTopK < r.topK {
		initialTopK = r.topK
	}

	candidates, err := r.retriever.Retrieve(ctx, query, initialTopK)
	if err != nil {
		return nil, fmt.Errorf("retrieval failed: %w", err)
	}

	// 2. 重排序
	reranked, err := r.reranker.Rerank(ctx, query, candidates)
	if err != nil {
		return nil, fmt.Errorf("reranking failed: %w", err)
	}

	// 3. 截取 Top K
	if len(reranked) > topK {
		reranked = reranked[:topK]
	}

	return reranked, nil
}

// RetrieveWithScore 检索 + 重排序（带分数过滤）
func (r *RetrievalWithRerank) RetrieveWithScore(ctx context.Context, query string, topK int) ([]Document, error) {
	return r.Retrieve(ctx, query, topK)
}
