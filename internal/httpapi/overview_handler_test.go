package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/overview"
	"github.com/V3teran/liusha/internal/proxystatus"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/worker"
	"strconv"
)

type fakeOverview struct{ got overview.Query }

func (f *fakeOverview) Snapshot(_ context.Context, q overview.Query) (overview.Snapshot, error) {
	f.got = q
	return overview.Snapshot{Window: overview.Window{Days: overview.DefaultWindowDays}}, nil
}

func overviewRouter(d Deps) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerOverviewRoutes(r, d)
	return r
}

func getJSON(t *testing.T, r http.Handler, url string, out any) int {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	if out != nil && w.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), out))
	}
	return w.Code
}

// TestOverviewSnapshot_Timezone 锁定时区参数：按查看者时区切分「天」；缺省 UTC；
// 非法名与依赖服务器环境的 Local 一律 400。
func TestOverviewSnapshot_Timezone(t *testing.T) {
	fake := &fakeOverview{}
	r := overviewRouter(Deps{Overview: fake})

	require.Equal(t, http.StatusOK, getJSON(t, r, "/overview?tz=Asia/Shanghai", nil))
	assert.Equal(t, "Asia/Shanghai", fake.got.Location.String())

	require.Equal(t, http.StatusOK, getJSON(t, r, "/overview", nil))
	assert.Equal(t, time.UTC, fake.got.Location)

	for _, tz := range []string{"Mars/Olympus", "Local"} {
		assert.Equal(t, http.StatusBadRequest, getJSON(t, r, "/overview?tz="+tz, nil), tz)
	}
}

type fakeRunners struct {
	runners []worker.Runner
	err     error
}

func (f fakeRunners) Runners() ([]worker.Runner, error) { return f.runners, f.err }

type fakeProxyTraffic struct{ n int }

func (f fakeProxyTraffic) CountSince(context.Context, time.Time) (int, error) { return f.n, nil }

type fakeDataStores struct {
	pg    db.PostgresStatus
	pgErr error
	rd    db.RedisStatus
	rdErr error
}

func (f fakeDataStores) Postgres(context.Context) (db.PostgresStatus, error) { return f.pg, f.pgErr }
func (f fakeDataStores) RedisStatus(context.Context) (db.RedisStatus, error) { return f.rd, f.rdErr }

// TestOverviewHealth_AllGood 锁定各项正常时的读数：在线 runner 实例与忙闲、代理心跳与近一小时流量、
// 沙盒工具、数据依赖的版本 / 往返耗时 / 占用。
func TestOverviewHealth_AllGood(t *testing.T) {
	now := time.Now()
	started := now.Add(-time.Hour)
	r := overviewRouter(Deps{
		Overview: &fakeOverview{},
		Runners: fakeRunners{runners: []worker.Runner{
			{Host: "h1", PID: 42, StartedAt: started, Concurrency: 10, Active: 2, Status: worker.RunnerStatusActive},
		}},
		Proxy:            fakeProxyStatus{rec: proxystatus.Record{StartedAt: started, HeartbeatAt: now}, ok: true},
		ProxyTraffic:     fakeProxyTraffic{n: 42},
		SandboxInventory: fakeInventory{inv: sandbox.Inventory{Tools: map[string]bool{"nmap": true}, Browser: sandbox.BrowserStatus{Installed: true}}},
		DataStores: fakeDataStores{
			pg: db.PostgresStatus{Version: "17.2", Latency: 1500 * time.Microsecond, Connections: 12, MaxConnections: 100},
			rd: db.RedisStatus{Version: "8.0.2", Latency: 400 * time.Microsecond, UsedMemory: 3 << 20},
		},
	})
	var h overviewHealth
	require.Equal(t, http.StatusOK, getJSON(t, r, "/overview/health", &h))

	assert.Equal(t, healthOK, h.Runner.State)
	require.Len(t, h.Runner.Instances, 1)
	assert.Equal(t, 2, h.Runner.Instances[0].Active)
	assert.Equal(t, 10, h.Runner.Instances[0].Concurrency)
	assert.Equal(t, healthOK, h.Proxy.State)
	require.NotNil(t, h.Proxy.RequestsLastHour)
	assert.Equal(t, 42, *h.Proxy.RequestsLastHour)
	assert.Equal(t, healthOK, h.Sandbox.State)
	assert.Equal(t, postgresHealth{State: healthOK, Version: "17.2", LatencyMs: 1.5, Connections: 12, MaxConnections: 100}, h.Postgres)
	assert.Equal(t, redisHealth{State: healthOK, Version: "8.0.2", LatencyMs: 0.4, UsedMemoryBytes: 3 << 20}, h.Redis)
}

// TestOverviewHealth_Problems 锁定异常判定：没有在线 runner 为 down、有实例在停机中为 warn；
// 代理从未运行为 down；数据依赖连不上为 down，往返慢、连接或内存接近上限为 warn。
func TestOverviewHealth_Problems(t *testing.T) {
	refused := errors.New("dial tcp: connection refused")
	base := Deps{
		Overview:         &fakeOverview{},
		Runners:          fakeRunners{},
		Proxy:            fakeProxyStatus{},
		SandboxInventory: fakeInventory{err: refused},
		DataStores:       fakeDataStores{pgErr: refused, rdErr: refused},
	}
	var h overviewHealth
	require.Equal(t, http.StatusOK, getJSON(t, overviewRouter(base), "/overview/health", &h))
	assert.Equal(t, healthDown, h.Runner.State)
	assert.NotNil(t, h.Runner.Instances, "没有实例时是空数组")
	assert.Equal(t, healthDown, h.Proxy.State)
	assert.Equal(t, healthDown, h.Sandbox.State)
	assert.Equal(t, healthDown, h.Postgres.State)
	assert.Equal(t, healthDown, h.Redis.State)

	warn := base
	warn.Runners = fakeRunners{runners: []worker.Runner{{Status: worker.RunnerStatusActive}, {Status: "stopped"}}}
	warn.DataStores = fakeDataStores{
		pg: db.PostgresStatus{Latency: time.Millisecond, Connections: 95, MaxConnections: 100},
		rd: db.RedisStatus{Latency: 300 * time.Millisecond, UsedMemory: 10, MaxMemory: 100},
	}
	require.Equal(t, http.StatusOK, getJSON(t, overviewRouter(warn), "/overview/health", &h))
	assert.Equal(t, healthWarn, h.Runner.State, "有实例在停机中")
	assert.Equal(t, healthWarn, h.Postgres.State, "连接接近上限")
	assert.Equal(t, healthWarn, h.Redis.State, "往返慢")

	warn.DataStores = fakeDataStores{rd: db.RedisStatus{Latency: time.Millisecond, UsedMemory: 95, MaxMemory: 100}}
	require.Equal(t, http.StatusOK, getJSON(t, overviewRouter(warn), "/overview/health", &h))
	assert.Equal(t, healthWarn, h.Redis.State, "内存接近上限")

	// 读不到 runner 登记（Redis 故障）不等于 runner 不在
	unknown := base
	unknown.Runners = fakeRunners{err: refused}
	require.Equal(t, http.StatusOK, getJSON(t, overviewRouter(unknown), "/overview/health", &h))
	assert.Equal(t, healthUnknown, h.Runner.State)
}

func TestOverviewRoutes_NotRegistered(t *testing.T) {
	assert.Equal(t, http.StatusNotFound, getJSON(t, overviewRouter(Deps{}), "/overview", nil))
}

// slowInventory 在 ctx 结束前不返回：模拟沙盒首次探测慢于本页的检查预算。
type slowInventory struct{}

func (slowInventory) Inventory(ctx context.Context) (sandbox.Inventory, error) {
	<-ctx.Done()
	return sandbox.Inventory{}, ctx.Err()
}

// slowDataStores 在 ctx 结束前不返回：模拟数据依赖挂到连接超时。
type slowDataStores struct{}

func (slowDataStores) Postgres(ctx context.Context) (db.PostgresStatus, error) {
	<-ctx.Done()
	return db.PostgresStatus{}, ctx.Err()
}

func (slowDataStores) RedisStatus(ctx context.Context) (db.RedisStatus, error) {
	<-ctx.Done()
	return db.RedisStatus{}, ctx.Err()
}

// 用完的是本页的检查预算，不等于对方不可达：一律 unknown；真连不上才是 down。
func TestHealthChecks_BudgetExhaustedIsUnknownNotDown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if got := checkSandbox(ctx, slowInventory{}); got.State != healthUnknown {
		t.Fatalf("沙盒检查超时应为 unknown，得到 %q", got.State)
	}
	if got := checkPostgres(ctx, slowDataStores{}); got.State != healthUnknown {
		t.Fatalf("PostgreSQL 检查超时应为 unknown，得到 %q", got.State)
	}
	if got := checkRedis(ctx, slowDataStores{}); got.State != healthUnknown {
		t.Fatalf("Redis 检查超时应为 unknown，得到 %q", got.State)
	}
	if got := checkSandbox(context.Background(), fakeInventory{err: errors.New("dial tcp: connection refused")}); got.State != healthDown {
		t.Fatalf("连不上应为 down，得到 %q", got.State)
	}
}

// TestOverviewSnapshot_DaysParam：days 缺省 14；只接受 7 / 14 / 30（其它值 400）；
// 合法值透传给快照查询。
func TestOverviewSnapshot_DaysParam(t *testing.T) {
	fake := &fakeOverview{}
	r := overviewRouter(Deps{Overview: fake})

	require.Equal(t, http.StatusOK, getJSON(t, r, "/overview", nil))
	assert.Equal(t, overview.DefaultWindowDays, fake.got.Days)

	for _, d := range []int{7, 30} {
		require.Equal(t, http.StatusOK, getJSON(t, r, fmt.Sprintf("/overview?days=%d", d), nil), "days="+strconv.Itoa(d))
		assert.Equal(t, d, fake.got.Days, "days="+strconv.Itoa(d))
	}
	for _, d := range []string{"8", "0", "-1", "abc", "31"} {
		assert.Equal(t, http.StatusBadRequest, getJSON(t, r, "/overview?days="+d, nil), "days="+d)
	}
}
