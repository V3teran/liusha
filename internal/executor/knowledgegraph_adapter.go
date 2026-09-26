package executor

import (
	"context"
	"encoding/json"

	"github.com/V3teran/liusha/internal/explorationgraph"
)

// knowledgeGraphAdapter 将 explorationgraph.Store 适配为 Actor.ExplorationGraphReader 接口。
type knowledgeGraphAdapter struct {
	store *explorationgraph.Store
}

// NewExplorationGraphAdapter 创建适配器。
func NewExplorationGraphAdapter(store *explorationgraph.Store) ExplorationGraphReader {
	if store == nil {
		return nil
	}
	return &knowledgeGraphAdapter{store: store}
}

// GetNode 实现 ExplorationGraphReader 接口。
func (w *knowledgeGraphAdapter) GetNode(ctx context.Context, id string) (*ExplorationGraphNode, error) {
	node, err := w.store.GetNode(ctx, id)
	if err != nil {
		return nil, err
	}

	return &ExplorationGraphNode{
		ID:       node.ID,
		Metadata: json.RawMessage(node.Metadata),
	}, nil
}
