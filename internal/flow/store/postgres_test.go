package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/flow"
	flowstore "github.com/V3teran/liusha/internal/flow/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func startPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "pgvector/pgvector:pg17",
		tcpostgres.WithDatabase("liusha"),
		tcpostgres.WithUsername("liusha"),
		tcpostgres.WithPassword("liusha"),
		tcpostgres.WithInitScripts("../../../db/migrations/0005_http_flow.up.sql"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func TestIntegration_PostgresStore_BatchInsert(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	pool := startPostgres(t)
	s := flowstore.NewPostgres(pool)
	ctx := context.Background()

	engID := uuid.New()
	flows := []flow.Flow{
		flow.New(flow.RawInput{
			EngagementID: engID,
			Timestamp:    time.Now().UTC(),
			Method:       "GET",
			URL:          "https://example.com/a",
			RespStatus:   200,
			RespBody:     []byte("ok"),
		}),
		flow.New(flow.RawInput{
			EngagementID:   engID,
			Timestamp:      time.Now().UTC(),
			Method:         "POST",
			URL:            "https://example.com/b",
			ReqContentType: "application/json",
			ReqBody:        []byte(`{"x":1}`),
			RespStatus:     201,
		}),
	}

	require.NoError(t, s.BatchInsert(ctx, flows))

	var count int
	err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM http_flow WHERE engagement_id = $1", engID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 2, count)
}

func TestIntegration_PostgresStore_EmptyBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	pool := startPostgres(t)
	s := flowstore.NewPostgres(pool)
	require.NoError(t, s.BatchInsert(context.Background(), nil))
}
