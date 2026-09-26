package explorationgraph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/core"
)

// UpdateNodeMetadata 合并更新节点的 metadata（jsonb 浅合并，传入键覆盖、
// 未提及键保留）。绕过本方法直接整列替换会洗掉 task_id/source_type 等
// 内部键，导致节点从 planner 视图中消失。
func (s *Store) UpdateNodeMetadata(ctx context.Context, id string, metadata json.RawMessage) error {
	if s.pool == nil {
		return s.updateNodeMetadataInMemory(ctx, id, metadata)
	}

	query := `
		UPDATE wm_node
		SET metadata = COALESCE(metadata, '{}'::jsonb) || $2::jsonb,
		    updated_at = now()
		WHERE id = $1
	`

	tag, err := s.pool.Exec(ctx, query, id, metadata)
	if err != nil {
		return fmt.Errorf("update node metadata: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("node not found: %s", id)
	}
	return nil
}

// updateNodeMetadataInMemory 内存后端的合并语义实现（与 jsonb || 对齐：浅合并）。
func (s *Store) updateNodeMetadataInMemory(ctx context.Context, id string, metadata json.RawMessage) error {
	node, err := s.GetNode(ctx, id)
	if err != nil {
		return err
	}
	merged := map[string]interface{}{}
	if len(node.Metadata) > 0 {
		if err := json.Unmarshal(node.Metadata, &merged); err != nil {
			return fmt.Errorf("unmarshal existing metadata: %w", err)
		}
	}
	var patch map[string]interface{}
	if err := json.Unmarshal(metadata, &patch); err != nil {
		return fmt.Errorf("unmarshal metadata patch: %w", err)
	}
	for k, v := range patch {
		merged[k] = v
	}
	return s.graphStore.UpdateNode(ctx, id, core.GraphNodeUpdate{Metadata: merged})
}
