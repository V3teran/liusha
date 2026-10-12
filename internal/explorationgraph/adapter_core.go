package explorationgraph

import (
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 是探索图的 Framework 适配器：领域 Node/Edge 与通用 GraphNode/GraphEdge
// 的双向映射。pool 非 nil 时（Postgres 后端），统计/状态投影等查询直接 SQL 下推；
// 内存后端 pool 为 nil，退化为经 GraphStore 通用查询。
type Store struct {
	graphStore core.GraphStore
	pool       *pgxpool.Pool
}

// newStore 用 Framework GraphStore 组装 Store。公共入口是 factory.go 的
// NewStore(pool)/NewMemoryStore()——本构造器仅包内组装用。
func newStore(graphStore core.GraphStore) *Store {
	return &Store{
		graphStore: graphStore,
	}
}

// typedMetadataKeys 是 graph metadata 中映射到 Node 类型化字段的键。
// 其余键在读取时原样进入 Node.Metadata（业务扩展位：verification_outcome、
// failure_reason 等），写入时 Node.Metadata 的键平铺合并进 graph metadata。
var typedMetadataKeys = map[string]bool{
	"task_id":        true,
	"source_type":    true,
	"source_id":      true,
	"priority":       true,
	"complexity":     true,
	"depends_on":     true,
	"blocked_reason": true,
	"confidence":     true,
}
