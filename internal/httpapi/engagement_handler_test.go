package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/engagement/store"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestIntegration_CreateAndGetEngagement: 走一遍创建 → 获取 → 按租户列出的完整链路，
// 验证 handler 与 store 正确装配，且 JSON 字段与领域模型一致。
func TestIntegration_CreateAndGetEngagement(t *testing.T) {
	if testing.Short() {
		t.Skip("集成测试：需要 Docker")
	}
	ctx := context.Background()
	pool, cleanup := newTestPool(ctx, t)
	t.Cleanup(cleanup)

	srv := httpapi.New(config.Config{
		Env:     "test",
		APIAddr: "127.0.0.1:0",
		API:     config.APIConfig{ReadTimeoutSeconds: 5, WriteTimeoutSeconds: 5},
	}, store.NewPostgres(pool))

	body := []byte(`{
		"tenant_id": "33333333-3333-3333-3333-333333333333",
		"mode": "proxy",
		"scope": {"hosts": ["example.com"]},
		"objective": "probe upload"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/engagements", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.NotEmpty(t, created.ID)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/engagements/"+created.ID, nil)
	getW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getW, getReq)
	assert.Equal(t, http.StatusOK, getW.Code)

	listReq := httptest.NewRequest(http.MethodGet,
		"/api/v1/engagements?tenant_id=33333333-3333-3333-3333-333333333333", nil)
	listW := httptest.NewRecorder()
	srv.Handler().ServeHTTP(listW, listReq)
	assert.Equal(t, http.StatusOK, listW.Code)
	assert.Contains(t, listW.Body.String(), created.ID)
}

// TestIntegration_CreateEngagement_RejectsUnknownMode: 非法 mode 应被 handler 层拦截为 400，
// 且错误体里包含业务码 invalid_input，与 respondError 约定一致。
func TestIntegration_CreateEngagement_RejectsUnknownMode(t *testing.T) {
	if testing.Short() {
		t.Skip("集成测试：需要 Docker")
	}
	ctx := context.Background()
	pool, cleanup := newTestPool(ctx, t)
	t.Cleanup(cleanup)

	srv := httpapi.New(config.Config{
		Env:     "test",
		APIAddr: "127.0.0.1:0",
		API:     config.APIConfig{ReadTimeoutSeconds: 5, WriteTimeoutSeconds: 5},
	}, store.NewPostgres(pool))

	body := []byte(`{
		"tenant_id": "33333333-3333-3333-3333-333333333333",
		"mode": "wifi",
		"scope": {"hosts": ["example.com"]}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/engagements", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid_input")
}

// newTestPool 启动 pgvector 容器、跑全部迁移，返回连接池与清理函数；与 store 层测试保持一致。
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
	ctxPool, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctxPool, dsn)
	require.NoError(t, err)
	return pool, func() {
		pool.Close()
		_ = pg.Terminate(ctx)
	}
}

// migrationsDir 从 internal/httpapi 回溯两级到仓库根，再定位 db/migrations 的绝对路径。
func migrationsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	abs, err := filepath.Abs(filepath.Join(root, "db", "migrations"))
	require.NoError(t, err)
	_, err = os.Stat(abs)
	require.NoError(t, err)
	return abs
}
