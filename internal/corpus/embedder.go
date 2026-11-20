package corpus

import "context"

// Embedder 把文本编码为向量：入库文本与检索查询分开编码（非对称检索）。
type Embedder interface {
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
	EmbedPassage(ctx context.Context, texts []string) ([][]float32, error)
}

// Reranker 给候选文档按与查询的相关度打分。
type Reranker interface {
	// Rerank 返回按相关度降序的打分结果（下标指向 docs），至多 topN 条。
	Rerank(ctx context.Context, query string, docs []string, topN int) ([]Ranked, error)
}

// Ranked 是一篇候选文档的重排结果。
type Ranked struct {
	Index int
	Score float64
}

// Models 是语义检索依赖的模型服务。Embedder 与 Reranker 同时具备才启用语义检索。
type Models struct {
	Embedder Embedder
	Reranker Reranker
	// 启用的两个模型名（只用于展示与日志）。
	EmbeddingModel string
	RerankModel    string
}

// Semantic 表示语义检索可用。
func (m Models) Semantic() bool { return m.Embedder != nil && m.Reranker != nil }
