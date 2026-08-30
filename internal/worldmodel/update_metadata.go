package worldmodel

import (
	"context"
	"encoding/json"
	"fmt"
)

// UpdateNodeMetadata 更新节点的 metadata。
func (s *Store) UpdateNodeMetadata(ctx context.Context, id string, metadata json.RawMessage) error {
	query := `
		UPDATE wm_node
		SET metadata = $2, updated_at = now()
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
