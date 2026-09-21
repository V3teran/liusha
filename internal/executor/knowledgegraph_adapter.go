package executor

import (
	"context"
	"encoding/json"

	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// knowledgeGraphAdapter 将 knowledgegraph.Store 适配为 Actor.KnowledgeGraphReader 接口。
type knowledgeGraphAdapter struct {
	store *knowledgegraph.Store
}

// NewKnowledgeGraphAdapter 创建适配器。
func NewKnowledgeGraphAdapter(store *knowledgegraph.Store) KnowledgeGraphReader {
	if store == nil {
		return nil
	}
	return &knowledgeGraphAdapter{store: store}
}

// GetNode 实现 KnowledgeGraphReader 接口。
func (w *knowledgeGraphAdapter) GetNode(ctx context.Context, id string) (*KnowledgeGraphNode, error) {
	node, err := w.store.GetNode(ctx, id)
	if err != nil {
		return nil, err
	}

	return &KnowledgeGraphNode{
		ID:       node.ID,
		Metadata: json.RawMessage(node.Metadata),
	}, nil
}
