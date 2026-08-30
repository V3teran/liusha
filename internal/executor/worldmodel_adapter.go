package executor

import (
	"context"
	"encoding/json"

	"github.com/V3teran/liusha/internal/worldmodel"
)

// worldModelAdapter 将 worldmodel.Store 适配为 Actor.WorldModelReader 接口。
type worldModelAdapter struct {
	store *worldmodel.Store
}

// NewWorldModelAdapter 创建适配器。
func NewWorldModelAdapter(store *worldmodel.Store) WorldModelReader {
	if store == nil {
		return nil
	}
	return &worldModelAdapter{store: store}
}

// GetNode 实现 WorldModelReader 接口。
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
