package explorationgraph

import (
	"github.com/V3teran/liusha/internal/framework/persistence/memory"
	"github.com/V3teran/liusha/internal/framework/persistence/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewStore 创建探索图 Store（使用 Framework GraphStore）
func NewStore(pool *pgxpool.Pool) *Store {
	graphStore := postgres.NewGraphStore(pool)
	store := newStore(graphStore)
	// 设置 pool 字段用于 Roadmap 功能
	store.pool = pool
	return store
}

// NewMemoryStore 创建内存版探索图 Store（用于测试）
func NewMemoryStore() *Store {
	graphStore := memory.NewGraphStore()
	store := newStore(graphStore)
	// 内存版不需要 pool
	return store
}
