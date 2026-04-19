package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	graphmodel "github.com/V3teran/liusha/internal/graph/model"
)

const (
	graphHotKeyFmt = "liusha:graph:hot:%s"
	graphHotTTL    = 7 * 24 * time.Hour
)

type Redis struct {
	rdb *redis.Client
}

func NewRedis(rdb *redis.Client) *Redis {
	return &Redis{rdb: rdb}
}

// SetNode 将节点写入 Redis Hash，并刷新整个 Hash 的 TTL。
func (s *Redis) SetNode(ctx context.Context, n graphmodel.Node) error {
	b, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("序列化 node 失败: %w", err)
	}
	key := fmt.Sprintf(graphHotKeyFmt, n.EngagementID)
	pipe := s.rdb.Pipeline()
	pipe.HSet(ctx, key, n.ID.String(), string(b))
	pipe.Expire(ctx, key, graphHotTTL)
	_, err = pipe.Exec(ctx)
	return err
}

// GetNode 从 Redis 读取单个节点。
func (s *Redis) GetNode(ctx context.Context, engID, nodeID uuid.UUID) (graphmodel.Node, error) {
	key := fmt.Sprintf(graphHotKeyFmt, engID)
	val, err := s.rdb.HGet(ctx, key, nodeID.String()).Result()
	if err != nil {
		return graphmodel.Node{}, fmt.Errorf("HGet graph node 失败: %w", err)
	}
	var n graphmodel.Node
	if err := json.Unmarshal([]byte(val), &n); err != nil {
		return graphmodel.Node{}, fmt.Errorf("反序列化 node 失败: %w", err)
	}
	return n, nil
}

// ListNodes 返回 engagement 在 Redis 中的所有热节点。
func (s *Redis) ListNodes(ctx context.Context, engID uuid.UUID) ([]graphmodel.Node, error) {
	key := fmt.Sprintf(graphHotKeyFmt, engID)
	vals, err := s.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("HGetAll graph nodes 失败: %w", err)
	}
	nodes := make([]graphmodel.Node, 0, len(vals))
	for _, v := range vals {
		var n graphmodel.Node
		if err := json.Unmarshal([]byte(v), &n); err != nil {
			return nil, fmt.Errorf("反序列化 node 失败: %w", err)
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}
