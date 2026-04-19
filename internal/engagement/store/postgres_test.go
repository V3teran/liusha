package store_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/engagement/store"
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
)

// TestIntegration_EngagementStore_CreateGetList: 启动 pgvector 容器，
// 应用全部迁移后验证 Create → Get → ListByTenant 的端到端行为。
func TestIntegration_EngagementStore_CreateGetList(t *testing.T) {
	if testing.Short() {
		t.Skip("集成测试：需要 Docker，使用 -short 可跳过")
	}
	ctx := context.Background()
	pool, cleanup := newTestPool(ctx, t)
	t.Cleanup(cleanup)

	s := store.NewPostgres(pool)
	e, err := engagement.New(engagement.CreateInput{
		TenantID:  uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		Mode:      engagement.ModeProxy,
		Scope:     map[string]any{"hosts": []string{"example.com"}},
		Objective: "probe upload flow",
	})
	require.NoError(t, err)

	require.NoError(t, s.Create(ctx, e))

	got, err := s.Get(ctx, e.ID)
	require.NoError(t, err)
	assert.Equal(t, e.ID, got.ID)
	assert.Equal(t, e.TenantID, got.TenantID)
	assert.Equal(t, engagement.ModeProxy, got.Mode)
	assert.Equal(t, engagement.StatusActive, got.Status)
	assert.Equal(t, "probe upload flow", got.Objective)

	list, err := s.ListByTenant(ctx, e.TenantID, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, e.ID, list[0].ID)
}

// TestIntegration_EngagementStore_ArchiveOptimisticLock: 首次 Archive 应成功；
// 再次使用旧 version 应返回 ErrVersionMismatch（乐观锁生效）。
func TestIntegration_EngagementStore_ArchiveOptimisticLock(t *testing.T) {
	if testing.Short() {
		t.Skip("集成测试：需要 Docker，使用 -short 可跳过")
	}
	ctx := context.Background()
	pool, cleanup := newTestPool(ctx, t)
	t.Cleanup(cleanup)

	s := store.NewPostgres(pool)
	e, err := engagement.New(engagement.CreateInput{
		TenantID: uuid.New(),
		Mode:     engagement.ModeProxy,
		Scope:    map[string]any{"hosts": []string{"example.com"}},
	})
	require.NoError(t, err)
	require.NoError(t, s.Create(ctx, e))

	require.NoError(t, s.Archive(ctx, e.ID, e.Version))

	err = s.Archive(ctx, e.ID, e.Version) // 旧 version，应失败
	require.ErrorIs(t, err, store.ErrVersionMismatch)
}

// newTestPool 启动 pgvector 容器并跑全部迁移，返回连接池与清理函数。
// 使用 pg17 与开发环境 docker-compose 对齐，避免镜像版本漂移。
func newTestPool(ctx context.Context, t *testing.T) (*pgxpool.Pool, func()) {
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
	m, err := migrate.New("file://"+migrationsDir(t), dsn)
	require.NoError(t, err)
	require.NoError(t, m.Up())
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool, func() {
		pool.Close()
		_ = pg.Terminate(ctx)
	}
}

// migrationsDir 根据当前测试文件位置解析出 <repo>/db/migrations 的绝对路径。
func migrationsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	abs, err := filepath.Abs(filepath.Join(root, "db", "migrations"))
	require.NoError(t, err)
	_, err = os.Stat(abs)
	require.NoError(t, err)
	return abs
}
