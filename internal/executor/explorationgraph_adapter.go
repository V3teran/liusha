package executor

import (
	"context"

	"github.com/V3teran/liusha/internal/explorationgraph"
)

// explorationGraphAdapter 将 explorationgraph.Store 适配为 Actor.ExplorationGraphReader 接口。
type explorationGraphAdapter struct {
	store *explorationgraph.Store
}

// NewExplorationGraphAdapter 创建适配器。
func NewExplorationGraphAdapter(store *explorationgraph.Store) ExplorationGraphReader {
	if store == nil {
		return nil
	}
	return &explorationGraphAdapter{store: store}
}

// GetNode 实现 ExplorationGraphReader 接口。
func (w *explorationGraphAdapter) GetNode(ctx context.Context, id string) (*ExplorationGraphNode, error) {
	node, err := w.store.GetNode(ctx, id)
	if err != nil {
		return nil, err
	}

	return &ExplorationGraphNode{
		ID:       node.ID,
		Metadata: node.Metadata,
	}, nil
}
