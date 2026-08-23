package corpus

import "context"

// Embedder 是 search/write_corpus 依赖的 embedding 能力。
// embedding.Client 满足此接口。nil 时降级纯 sparse 检索。
type Embedder interface {
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
	EmbedPassage(ctx context.Context, texts []string) ([][]float32, error)
}
