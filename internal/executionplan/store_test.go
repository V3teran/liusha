package executionplan

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/worldmodel"
)

func setupTestStore(t *testing.T) (*Store, func()) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// 启动 PostgreSQL 容器
	container, err := tcpostgres.Run(ctx, "pgvector/pgvector:pg17",
		tcpostgres.WithDatabase("liusha"),
		tcpostgres.WithUsername("liusha"),
		tcpostgres.WithPassword("liusha"),
		tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	pool, err := db.NewPgPool(ctx, dsn, 5, 1, 5, 0)
	require.NoError(t, err)

	store := NewStore(pool)

	cleanup := func() {
		pool.Close()
		_ = container.Terminate(context.Background())
	}

	return store, cleanup
}

func TestStore_Create(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	taskID := uuid.New().String()

	move := Move{
		TaskID: taskID,
		Kind:   MoveEnumerate,
		Domain: "web",
		TargetRef: worldmodel.TargetRef{
			Domain:  "web",
			RefKind: "endpoint",
			Locator: "https://example.com",
		},
		Priority: 10,
		Reason:   "Initial reconnaissance",
	}

	created, err := store.Create(ctx, move)
	require.NoError(t, err)
	assert.NotEqual(t, uuid.Nil, created.ID)
	assert.Equal(t, StatusPending, created.Status)
	assert.NotZero(t, created.CreatedAt)
}

func TestStore_ListPending(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	taskID := uuid.New().String()

	// 创建多个 Move
	moves := []Move{
		{
			TaskID:   taskID,
			Kind:     MoveEnumerate,
			Domain:   "web",
			TargetRef: worldmodel.TargetRef{Domain: "web", RefKind: "endpoint", Locator: "https://a.com"},
			Priority: 5,
			Reason:   "Low priority",
		},
		{
			TaskID:   taskID,
			Kind:     MoveProbe,
			Domain:   "web",
			TargetRef: worldmodel.TargetRef{Domain: "web", RefKind: "endpoint", Locator: "https://b.com"},
			Priority: 10,
			Reason:   "High priority",
		},
	}

	for _, m := range moves {
		_, err := store.Create(ctx, m)
		require.NoError(t, err)
	}

	// 查询待执行 Move
	pending, err := store.ListPending(ctx, taskID)
	require.NoError(t, err)
	assert.Len(t, pending, 2)

	// 验证按优先级降序
	assert.Equal(t, 10, pending[0].Priority)
	assert.Equal(t, 5, pending[1].Priority)
}

func TestStore_MarkExecuting(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	taskID := uuid.New().String()

	move := Move{
		TaskID: taskID,
		Kind:   MoveExploit,
		Domain: "web",
		TargetRef: worldmodel.TargetRef{Domain: "web", RefKind: "endpoint", Locator: "https://test.com"},
		Reason: "Test exploit",
	}

	created, err := store.Create(ctx, move)
	require.NoError(t, err)

	// 标记为执行中
	err = store.MarkExecuting(ctx, created.ID)
	require.NoError(t, err)

	// 验证状态
	updated, err := store.GetByID(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusExecuting, updated.Status)
	assert.NotNil(t, updated.StartedAt)
}

func TestStore_MarkCompleted(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	taskID := uuid.New().String()

	move := Move{
		TaskID: taskID,
		Kind:   MoveEnumerate,
		Domain: "web",
		TargetRef: worldmodel.TargetRef{Domain: "web", RefKind: "endpoint", Locator: "https://test.com"},
		Reason: "Test",
	}

	created, err := store.Create(ctx, move)
	require.NoError(t, err)

	// 必须先标记为执行中
	err = store.MarkExecuting(ctx, created.ID)
	require.NoError(t, err)

	// 标记为完成
	err = store.MarkCompleted(ctx, created.ID)
	require.NoError(t, err)

	// 验证状态
	updated, err := store.GetByID(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, updated.Status)
	assert.NotNil(t, updated.CompletedAt)
}

func TestStore_MarkFailed(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	taskID := uuid.New().String()

	move := Move{
		TaskID: taskID,
		Kind:   MoveExploit,
		Domain: "web",
		TargetRef: worldmodel.TargetRef{Domain: "web", RefKind: "endpoint", Locator: "https://test.com"},
		Reason: "Test",
	}

	created, err := store.Create(ctx, move)
	require.NoError(t, err)

	// 必须先标记为执行中
	err = store.MarkExecuting(ctx, created.ID)
	require.NoError(t, err)

	// 标记为失败
	errMsg := "Connection timeout"
	err = store.MarkFailed(ctx, created.ID, errMsg)
	require.NoError(t, err)

	// 验证状态
	updated, err := store.GetByID(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, updated.Status)
	assert.Equal(t, errMsg, updated.ErrorMessage)
	assert.NotNil(t, updated.CompletedAt)
}

func TestStore_DeleteByTaskID(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	taskID := uuid.New().String()

	// 创建多个 Move
	for i := 0; i < 3; i++ {
		move := Move{
			TaskID: taskID,
			Kind:   MoveEnumerate,
			Domain: "web",
			TargetRef: worldmodel.TargetRef{Domain: "web", RefKind: "endpoint", Locator: "https://test.com"},
			Reason: "Test",
		}
		_, err := store.Create(ctx, move)
		require.NoError(t, err)
	}

	// 删除所有 Move
	err := store.DeleteByTaskID(ctx, taskID)
	require.NoError(t, err)

	// 验证已删除
	moves, err := store.ListAll(ctx, taskID)
	require.NoError(t, err)
	assert.Len(t, moves, 0)
}
