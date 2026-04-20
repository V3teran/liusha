// internal/graph/store/postgres_test.go
package store_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	graphmodel "github.com/V3teran/liusha/internal/graph/model"
	"github.com/V3teran/liusha/internal/graph/store"
)

func TestIntegration_GraphStore_AddAndGet(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool, cleanup := newGraphTestPool(ctx, t)
	t.Cleanup(cleanup)

	engID := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO engagement (id, tenant_id, mode, scope, status, version)
         VALUES ($1, $2, 'proxy', '{}', 'active', 1)`,
		engID, uuid.New(),
	)
	require.NoError(t, err)

	st := store.NewPostgres(pool)

	node := graphmodel.Node{
		ID:           uuid.New(),
		EngagementID: engID,
		Kind:         graphmodel.KindSurface,
		Payload:      map[string]any{"url": "http://example.com/login"},
		DedupKey:     "GET:example.com/login",
	}
	require.NoError(t, st.AddNode(ctx, node))

	got, err := st.GetNode(ctx, node.ID)
	require.NoError(t, err)
	assert.Equal(t, node.Kind, got.Kind)
	assert.Equal(t, "http://example.com/login", got.Payload["url"])
}

func TestIntegration_GraphStore_AddNodeWithVector(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool, cleanup := newGraphTestPool(ctx, t)
	t.Cleanup(cleanup)

	engID := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO engagement (id, tenant_id, mode, scope, status, version)
         VALUES ($1, $2, 'proxy', '{}', 'active', 1)`,
		engID, uuid.New(),
	)
	require.NoError(t, err)

	st := store.NewPostgres(pool)

	vec := make([]float32, 1536)
	for i := range vec {
		vec[i] = float32(i%7) / 11.0
	}
	node := graphmodel.Node{
		ID:             uuid.New(),
		EngagementID:   engID,
		Kind:           graphmodel.KindFinding,
		Payload:        map[string]any{"title": "embedded finding"},
		Vector:         vec,
		EmbeddingModel: "text-embedding-3-small",
	}
	require.NoError(t, st.AddNode(ctx, node))

	got, err := st.GetNode(ctx, node.ID)
	require.NoError(t, err)
	assert.Equal(t, "text-embedding-3-small", got.EmbeddingModel)
	require.Len(t, got.Vector, 1536)
	assert.InDelta(t, vec[0], got.Vector[0], 1e-6)
	assert.InDelta(t, vec[1535], got.Vector[1535], 1e-6)
}

func TestIntegration_GraphStore_AddNodeWithoutVector(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool, cleanup := newGraphTestPool(ctx, t)
	t.Cleanup(cleanup)

	engID := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO engagement (id, tenant_id, mode, scope, status, version)
         VALUES ($1, $2, 'proxy', '{}', 'active', 1)`,
		engID, uuid.New(),
	)
	require.NoError(t, err)

	st := store.NewPostgres(pool)
	node := graphmodel.Node{
		ID:           uuid.New(),
		EngagementID: engID,
		Kind:         graphmodel.KindSurface,
		Payload:      map[string]any{"url": "http://novec.test"},
	}
	require.NoError(t, st.AddNode(ctx, node))

	got, err := st.GetNode(ctx, node.ID)
	require.NoError(t, err)
	assert.Nil(t, got.Vector)
	assert.Empty(t, got.EmbeddingModel)
}

func TestIntegration_GraphStore_AddEdgeDedup(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool, cleanup := newGraphTestPool(ctx, t)
	t.Cleanup(cleanup)

	engID := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO engagement (id, tenant_id, mode, scope, status, version)
         VALUES ($1, $2, 'proxy', '{}', 'active', 1)`,
		engID, uuid.New(),
	)
	require.NoError(t, err)

	st := store.NewPostgres(pool)

	// 准备两个 node 作为 edge 的端点
	src := graphmodel.Node{
		ID: uuid.New(), EngagementID: engID,
		Kind: graphmodel.KindSurface, Payload: map[string]any{"url": "http://dedup.test/a"},
	}
	dst := graphmodel.Node{
		ID: uuid.New(), EngagementID: engID,
		Kind: graphmodel.KindFinding, Payload: map[string]any{"title": "vuln"},
	}
	require.NoError(t, st.AddNode(ctx, src))
	require.NoError(t, st.AddNode(ctx, dst))

	// 连续三次插入相同语义关系（engagement/src/dst/kind 相同，每次 ID 不同）
	for i := 0; i < 3; i++ {
		e := graphmodel.Edge{
			ID:           uuid.New(),
			EngagementID: engID,
			SrcID:        src.ID,
			DstID:        dst.ID,
			Kind:         graphmodel.EdgeFinding,
		}
		require.NoError(t, st.AddEdge(ctx, e))
	}

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM graph_edge WHERE engagement_id=$1 AND src_id=$2 AND dst_id=$3 AND kind=$4`,
		engID, src.ID, dst.ID, string(graphmodel.EdgeFinding),
	).Scan(&count))
	assert.Equal(t, 1, count, "同一语义关系应只保留一条")
}

func TestIntegration_GraphStore_DedupIgnore(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool, cleanup := newGraphTestPool(ctx, t)
	t.Cleanup(cleanup)

	engID := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO engagement (id, tenant_id, mode, scope, status, version)
         VALUES ($1, $2, 'proxy', '{}', 'active', 1)`,
		engID, uuid.New(),
	)
	require.NoError(t, err)

	st := store.NewPostgres(pool)
	n1 := graphmodel.Node{
		ID: uuid.New(), EngagementID: engID,
		Kind: graphmodel.KindSurface, Payload: map[string]any{"url": "http://x.com"},
		DedupKey: "x.com",
	}
	n2 := graphmodel.Node{
		ID: uuid.New(), EngagementID: engID,
		Kind: graphmodel.KindSurface, Payload: map[string]any{"url": "http://x.com/other"},
		DedupKey: "x.com",
	}
	require.NoError(t, st.AddNode(ctx, n1))
	require.NoError(t, st.AddNode(ctx, n2))

	nodes, err := st.ListNodes(ctx, engID, graphmodel.KindSurface)
	require.NoError(t, err)
	assert.Len(t, nodes, 1)
}

// newGraphTestPool 启动 pgvector 容器并通过 golang-migrate 应用全部迁移。
func newGraphTestPool(ctx context.Context, t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	pg, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg17",
		tcpostgres.WithDatabase("liusha"),
		tcpostgres.WithUsername("liusha"),
		tcpostgres.WithPassword("liusha"),
		tcpostgres.BasicWaitStrategies(),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second)),
	)
	require.NoError(t, err)
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	m, err := migrate.New("file://"+graphMigrationsDir(t), dsn)
	require.NoError(t, err)
	require.NoError(t, m.Up())
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool, func() {
		pool.Close()
		_ = pg.Terminate(ctx)
	}
}

// graphMigrationsDir 根据当前测试文件位置解析出 <repo>/db/migrations 的绝对路径。
func graphMigrationsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	abs, err := filepath.Abs(filepath.Join(root, "db", "migrations"))
	require.NoError(t, err)
	_, err = os.Stat(abs)
	require.NoError(t, err)
	return abs
}
