package store

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	graphmodel "github.com/V3teran/liusha/internal/graph/model"
)

// nodeColdStore 是 CachingStore 所依赖的冷存储接口（通常是 Postgres）。
// 定义在调用处而非 Postgres 实现处，便于 mock 测试与未来替换底层。
type nodeColdStore interface {
	AddNode(ctx context.Context, n graphmodel.Node) error
	GetNode(ctx context.Context, id uuid.UUID) (graphmodel.Node, error)
}

// nodeHotCache 是 CachingStore 所依赖的热缓存接口（通常是 Redis）。
// 命中失败时不影响主流程，只打日志。
type nodeHotCache interface {
	SetNode(ctx context.Context, n graphmodel.Node) error
	GetNode(ctx context.Context, engID, nodeID uuid.UUID) (graphmodel.Node, error)
}

// CachingStore 把冷/热存储拼成图谱节点的统一入口：
//   - 写：冷存储权威落库；热缓存尽力而为（失败只打日志，不阻塞业务）。
//   - 读：先查热缓存，miss 时回源冷存储，并把结果回填到热缓存。
//
// 这样既保证数据不丢，也保留了 7 天窗口的热读性能。
type CachingStore struct {
	cold   nodeColdStore
	hot    nodeHotCache
	logger *slog.Logger
}

func NewCachingStore(cold nodeColdStore, hot nodeHotCache, logger *slog.Logger) *CachingStore {
	if logger == nil {
		logger = slog.Default()
	}
	return &CachingStore{cold: cold, hot: hot, logger: logger}
}

// AddNode 先写冷存储，成功后尽力写热缓存。
// 冷存储失败直接返回错误；热缓存失败只记 warn，保持接口成功。
func (s *CachingStore) AddNode(ctx context.Context, n graphmodel.Node) error {
	if err := s.cold.AddNode(ctx, n); err != nil {
		return err
	}
	if err := s.hot.SetNode(ctx, n); err != nil {
		s.logger.WarnContext(ctx, "热缓存写入失败（已写冷存储，继续）",
			slog.String("node_id", n.ID.String()),
			slog.Any("err", err),
		)
	}
	return nil
}

// GetNode 先查热缓存；miss 或错误时回源冷存储，并尝试回填热缓存。
// 回填失败同样只记日志，不影响读路径。
func (s *CachingStore) GetNode(ctx context.Context, engID, nodeID uuid.UUID) (graphmodel.Node, error) {
	if n, err := s.hot.GetNode(ctx, engID, nodeID); err == nil {
		return n, nil
	}
	n, err := s.cold.GetNode(ctx, nodeID)
	if err != nil {
		return graphmodel.Node{}, err
	}
	if err := s.hot.SetNode(ctx, n); err != nil {
		s.logger.WarnContext(ctx, "热缓存回填失败（已命中冷存储）",
			slog.String("node_id", nodeID.String()),
			slog.Any("err", err),
		)
	}
	return n, nil
}
