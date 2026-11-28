package explorationgraph

import (
	"encoding/json"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 是探索图的 Framework 适配器：探索图 Node/Edge 与通用 GraphNode/GraphEdge
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
// 其余键在读取时原样进入 Node.Metadata（业务扩展位：refute_reason、
// rejected_by 等），写入时 Node.Metadata 的键平铺合并进 graph metadata。
var typedMetadataKeys = map[string]bool{
	"source_type":    true,
	"priority":       true,
	"complexity":     true,
	"state_reason":   true,
	metaVerification: true,
	metaLead:         true,
	metaLeadClosed:   true,
	metaUrgent:       true,
}

// metaVerification 是验证状态在 graph metadata 中的键。evaluator 回写验证结论时以
// 同一键浅合并（UpdateNodeMetadata），与 Node.VerificationStatus 同一事实。
const metaVerification = "verification_status"

// typedFromMetadata 把 metadata 里的一个对象还原成类型化字段（线索、关闭批注）：存进去的是
// 经 JSON 往返的 map，这里再往返回来。不是对象或还原不了时返回 nil——按没有这个字段处理。
func typedFromMetadata[T any](v interface{}) *T {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out T
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return &out
}

// typedToMetadata 把类型化字段变成可入 metadata 的对象：经 JSON 往返，结构的 json 标签即
// 存储键。
func typedToMetadata(v any) map[string]interface{} {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]interface{}{}
	}
	var m map[string]interface{}
	if json.Unmarshal(b, &m) != nil {
		return map[string]interface{}{}
	}
	return m
}
