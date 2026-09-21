package knowledgegraph

import (
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewStore 创建知识图谱 Store（使用 Framework GraphStore）
func NewStore(pool *pgxpool.Pool) *Store {
	graphStore := core.NewPostgresGraphStore(pool)
	store := NewAdapterStore(graphStore)
	// 设置 pool 字段用于 Roadmap 功能
	store.pool = pool
	return store
}

// NewMemoryStore 创建内存版知识图谱 Store（用于测试）
func NewMemoryStore() *Store {
	graphStore := core.NewInMemoryGraphStore()
	store := NewAdapterStore(graphStore)
	// 内存版不需要 pool
	return store
}
