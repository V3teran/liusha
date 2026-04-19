package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/flow"
	flowstore "github.com/V3teran/liusha/internal/flow/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startRedis(t *testing.T) *redis.Client {
	t.Helper()
	ctx := context.Background()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "redis:8",
			ExposedPorts: []string{"6379/tcp"},
			WaitingFor:   wait.ForLog("Ready to accept connections"),
		},
		Started: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	host, err := c.Host(ctx)
	require.NoError(t, err)
	port, err := c.MappedPort(ctx, "6379")
	require.NoError(t, err)
	return redis.NewClient(&redis.Options{Addr: host + ":" + port.Port()})
}

func TestIntegration_RedisStore_PushAndRead(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	rdb := startRedis(t)
	s := flowstore.NewRedis(rdb)
	ctx := context.Background()

	engID := uuid.New()
	f := flow.New(flow.RawInput{
		EngagementID: engID,
		Timestamp:    time.Now().UTC(),
		Method:       "GET",
		URL:          "https://example.com/test",
		RespStatus:   200,
		RespBody:     []byte("hello"),
	})

	require.NoError(t, s.Push(ctx, f))

	entries, err := s.ReadNew(ctx, engID, "0", 10)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, f.URL, entries[0].URL)
	require.Equal(t, f.RespStatus, entries[0].RespStatus)
}

func TestIntegration_RedisStore_EmptyRead(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	rdb := startRedis(t)
	s := flowstore.NewRedis(rdb)
	ctx := context.Background()

	entries, err := s.ReadNew(ctx, uuid.New(), "0", 10)
	require.NoError(t, err)
	require.Empty(t, entries)
}
