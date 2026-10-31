package corpus

import "context"

// Embedder 是 corpus 检索依赖的 embedding 能力（写入侧只有本包 store 与离线导入 CLI）。
// embedding.Client 满足此接口。nil 时降级纯 sparse 检索。
type Embedder interface {
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
	EmbedPassage(ctx context.Context, texts []string) ([][]float32, error)
}
