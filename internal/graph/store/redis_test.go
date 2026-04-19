package store_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	graphmodel "github.com/V3teran/liusha/internal/graph/model"
	"github.com/V3teran/liusha/internal/graph/store"
)

func TestIntegration_GraphRedis_SetAndGet(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()

	rc, err := tcredis.Run(ctx, "redis:8")
	require.NoError(t, err)
	t.Cleanup(func() { _ = rc.Terminate(ctx) })

	addr, err := rc.Endpoint(ctx, "")
	require.NoError(t, err)
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()

	st := store.NewRedis(rdb)
	engID := uuid.New()
	node := graphmodel.Node{
		ID:           uuid.New(),
		EngagementID: engID,
		Kind:         graphmodel.KindFinding,
		Payload:      map[string]any{"title": "SQLi in /login"},
	}

	require.NoError(t, st.SetNode(ctx, node))
	got, err := st.GetNode(ctx, engID, node.ID)
	require.NoError(t, err)
	assert.Equal(t, graphmodel.KindFinding, got.Kind)
	assert.Equal(t, "SQLi in /login", got.Payload["title"])

	all, err := st.ListNodes(ctx, engID)
	require.NoError(t, err)
	assert.Len(t, all, 1)
}
