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
	testcontainers "github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/V3teran/liusha/internal/window"
	winstore "github.com/V3teran/liusha/internal/window/store"
)

func TestIntegration_WindowStore(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool, cleanup := newWindowTestPool(ctx, t)
	t.Cleanup(cleanup)

	engID := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO engagement (id, tenant_id, mode, scope, status, version)
         VALUES ($1, $2, 'proxy', '{}', 'active', 1)`,
		engID, uuid.New(),
	)
	require.NoError(t, err)

	st := winstore.NewPostgres(pool)

	w := window.AnalysisWindow{
		ID:           uuid.New(),
		EngagementID: engID,
		TriggerKind:  window.TriggerTime,
		StartedAt:    time.Now().UTC().Truncate(time.Millisecond),
	}
	require.NoError(t, st.CreateWindow(ctx, w))

	got, err := st.GetWindow(ctx, w.ID)
	require.NoError(t, err)
	assert.Equal(t, window.TriggerTime, got.TriggerKind)

	task := window.AgentTask{
		ID:       uuid.New(),
		WindowID: w.ID,
		Role:     "planner",
		Status:   window.TaskPending,
	}
	require.NoError(t, st.CreateTask(ctx, task))

	tasks, err := st.ListTasks(ctx, w.ID)
	require.NoError(t, err)
	assert.Len(t, tasks, 1)
	assert.Equal(t, "planner", tasks[0].Role)
}

func TestIntegration_WindowStore_UpdateTask(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool, cleanup := newWindowTestPool(ctx, t)
	t.Cleanup(cleanup)

	engID := uuid.New()
	_, err := pool.Exec(ctx,
		`INSERT INTO engagement (id, tenant_id, mode, scope, status, version)
         VALUES ($1, $2, 'proxy', '{}', 'active', 1)`,
		engID, uuid.New(),
	)
	require.NoError(t, err)

	st := winstore.NewPostgres(pool)
	w := window.AnalysisWindow{
		ID:           uuid.New(),
		EngagementID: engID,
		TriggerKind:  window.TriggerTime,
		StartedAt:    time.Now().UTC().Truncate(time.Millisecond),
	}
	require.NoError(t, st.CreateWindow(ctx, w))

	task := window.AgentTask{
		ID:       uuid.New(),
		WindowID: w.ID,
		Role:     "planner",
		Status:   window.TaskPending,
	}
	require.NoError(t, st.CreateTask(ctx, task))

	// running：只带 startedAt
	started := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, st.UpdateTask(ctx, task.ID, window.TaskRunning, &started, nil))

	tasks, err := st.ListTasks(ctx, w.ID)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, window.TaskRunning, tasks[0].Status)
	require.NotNil(t, tasks[0].StartedAt)
	assert.WithinDuration(t, started, *tasks[0].StartedAt, time.Second)
	assert.Nil(t, tasks[0].EndedAt)
	assert.False(t, tasks[0].CreatedAt.IsZero())

	// succeeded：只带 endedAt，startedAt 不应被覆盖
	ended := started.Add(2 * time.Second)
	require.NoError(t, st.UpdateTask(ctx, task.ID, window.TaskSucceeded, nil, &ended))

	tasks, err = st.ListTasks(ctx, w.ID)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, window.TaskSucceeded, tasks[0].Status)
	require.NotNil(t, tasks[0].StartedAt)
	assert.WithinDuration(t, started, *tasks[0].StartedAt, time.Second)
	require.NotNil(t, tasks[0].EndedAt)
	assert.WithinDuration(t, ended, *tasks[0].EndedAt, time.Second)
}

func TestIntegration_WindowStore_UpdateTask_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool, cleanup := newWindowTestPool(ctx, t)
	t.Cleanup(cleanup)

	st := winstore.NewPostgres(pool)
	err := st.UpdateTask(ctx, uuid.New(), window.TaskRunning, nil, nil)
	require.Error(t, err)
}

func newWindowTestPool(ctx context.Context, t *testing.T) (*pgxpool.Pool, func()) {
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
	m, err := migrate.New("file://"+windowMigrationsDir(t), dsn)
	require.NoError(t, err)
	require.NoError(t, m.Up())
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	return pool, func() {
		pool.Close()
		_ = pg.Terminate(ctx)
	}
}

func windowMigrationsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	abs, err := filepath.Abs(filepath.Join(root, "db", "migrations"))
	require.NoError(t, err)
	_, err = os.Stat(abs)
	require.NoError(t, err)
	return abs
}
