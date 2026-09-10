package executor

import (
	"context"
	"encoding/json"

	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// worldModelAdapter 将 knowledgegraph.Store 适配为 Actor.KnowledgeGraphReader 接口。
type worldModelAdapter struct {
	store *knowledgegraph.Store
}

// NewWorldModelAdapter 创建适配器。
func NewWorldModelAdapter(store *knowledgegraph.Store) KnowledgeGraphReader {
	if store == nil {
		return nil
	}
	return &worldModelAdapter{store: store}
}

// GetNode 实现 KnowledgeGraphReader 接口。
func (w *worldModelAdapter) GetNode(ctx context.Context, id string) (*WorldModelNode, error) {
	node, err := w.store.GetNode(ctx, id)
	if err != nil {
		return nil, err
	}

	return &WorldModelNode{
		ID:       node.ID,
		Metadata: json.RawMessage(node.Metadata),
	}, nil
}
