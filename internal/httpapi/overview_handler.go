package httpapi

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/overview"
	"github.com/V3teran/liusha/internal/worker"
)

// OverviewAPI 读概览快照（*overview.Store 满足）。
type OverviewAPI interface {
	Snapshot(ctx context.Context, q overview.Query) (overview.Snapshot, error)
}

// registerOverviewRoutes 注册「概览」页的两个只读端点：数据快照与组件健康。
func registerOverviewRoutes(r gin.IRouter, d Deps) {
	if d.Overview == nil {
		return
	}
	r.GET("/overview", overviewSnapshotHandler(d.Overview))
	r.GET("/overview/health", overviewHealthHandler(d))
}

// overviewSnapshotHandler 处理 GET /overview?tz=<IANA 时区>&days=<7|14|30>：按查看者时区
// 切分「天」的快照。tz 缺省按 UTC；不是合法的 IANA 时区名时 400（「Local」这类依赖服务器
// 环境的名字同样拒绝）。days 缺省 14；不在允许档位里时 400。
func overviewSnapshotHandler(api OverviewAPI) gin.HandlerFunc {
	return func(c *gin.Context) {
		loc := time.UTC
		if tz := strings.TrimSpace(c.Query("tz")); tz != "" {
			l, err := time.LoadLocation(tz)
			if err != nil || tz == "Local" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "时区无效：" + tz})
				return
			}
			loc = l
		}
		days := overview.DefaultWindowDays
		if raw := strings.TrimSpace(c.Query("days")); raw != "" {
			d, err := strconv.Atoi(raw)
			if err != nil || !overview.AllowedWindowDays[d] {
				c.JSON(http.StatusBadRequest, gin.H{"error": "时间窗无效：" + raw + "（允许 7、14、30）"})
				return
			}
			days = d
		}
		snap, err := api.Snapshot(c.Request.Context(), overview.Query{Now: time.Now(), Location: loc, Days: days})
		if err != nil {
			respondErr(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(http.StatusOK, snap)
	}
}

// 组件健康状态：ok 正常；warn 在运行但有隐患（缺工具、连接池占满、停机中……）；down 不可用；
// unknown 查不到（检查超出预算或该项未装配）——查不到不等于坏了，不能报成 down。
const (
	healthOK      = "ok"
	healthWarn    = "warn"
	healthDown    = "down"
	healthUnknown = "unknown"
)

// healthCheckTimeout 是单项检查的预算：沙盒或数据库不可达时请求会挂到连接超时，不能拖住整页。
const healthCheckTimeout = 3 * time.Second

// slowProbe 是数据依赖一次往返的「慢」阈值：与平台同处内网的 PostgreSQL / Redis 往返在毫秒级，
// 超过它说明网络或服务端已在承压，任务的每一步都会被拖慢。
const slowProbe = 200 * time.Millisecond

// saturationWarnRatio 是占用达到上限的告警比例：PostgreSQL 连接打满后所有组件都拿不到连接；
// Redis 内存到上限后按淘汰策略丢键或拒绝写入，队列与事件流会因此丢任务。
const saturationWarnRatio = 0.9

// proxyTrafficWindow 是代理一项统计近期流量的时间窗。
const proxyTrafficWindow = time.Hour

// RunnerRegistryAPI 列出在线的 runner 实例（*worker.Client 满足）。
type RunnerRegistryAPI interface {
	Runners() ([]worker.Runner, error)
}

// ProxyTrafficAPI 统计代理捕获的流量（*traffic.ProxyStore 满足）。
type ProxyTrafficAPI interface {
	CountSince(ctx context.Context, since time.Time) (int, error)
}

// DataStoresAPI 探测数据依赖（db.DataStores 满足）。
type DataStoresAPI interface {
	Postgres(ctx context.Context) (db.PostgresStatus, error)
	RedisStatus(ctx context.Context) (db.RedisStatus, error)
}

type runnerInstance struct {
	Host        string    `json:"host"`
	PID         int       `json:"pid"`
	StartedAt   time.Time `json:"started_at"`
	Concurrency int       `json:"concurrency"`
	Active      int       `json:"active"`
	Status      string    `json:"status"`
}

type runnerHealth struct {
	State     string           `json:"state"`
	Instances []runnerInstance `json:"instances"`
}

type proxyHealth struct {
	State         string     `json:"state"`
	LastSeenAt    *time.Time `json:"last_seen_at"`
	LastRequestAt *time.Time `json:"last_request_at"`
	// RequestsLastHour 是近一小时捕获的请求数；读不到时为 null
	RequestsLastHour *int `json:"requests_last_hour"`
}

type sandboxHealth struct {
	State          string `json:"state"`
	Reachable      bool   `json:"reachable"`
	ToolsInstalled int    `json:"tools_installed"`
	ToolsTotal     int    `json:"tools_total"`
	Browser        bool   `json:"browser"`
}

type postgresHealth struct {
	State          string  `json:"state"`
	Version        string  `json:"version"`
	LatencyMs      float64 `json:"latency_ms"`
	Connections    int     `json:"connections"` // 数据库当前的客户端连接数（全部客户端）
	MaxConnections int     `json:"max_connections"`
}

type redisHealth struct {
	State           string  `json:"state"`
	Version         string  `json:"version"`
	LatencyMs       float64 `json:"latency_ms"`
	UsedMemoryBytes int64   `json:"used_memory_bytes"`
	MaxMemoryBytes  int64   `json:"max_memory_bytes"` // 0 = 未设上限
}

type overviewHealth struct {
	Runner   runnerHealth   `json:"runner"`
	Proxy    proxyHealth    `json:"proxy"`
	Sandbox  sandboxHealth  `json:"sandbox"`
	Postgres postgresHealth `json:"postgres"`
	Redis    redisHealth    `json:"redis"`
}

// overviewHealthHandler 处理 GET /overview/health：三个工作进程（runner、proxy、sandbox）与两个
// 数据依赖（PostgreSQL、Redis）。api 不在其中：能拿到这个响应，api 就在线。检查并发执行、
// 各自限时，一项失败只影响这一项。
func overviewHealthHandler(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var out overviewHealth
		var wg sync.WaitGroup
		run := func(fn func(ctx context.Context)) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(c.Request.Context(), healthCheckTimeout)
				defer cancel()
				fn(ctx)
			}()
		}
		run(func(ctx context.Context) { out.Runner = checkRunner(ctx, d.Runners) })
		run(func(ctx context.Context) { out.Proxy = checkProxy(ctx, d.Proxy, d.ProxyTraffic) })
		run(func(ctx context.Context) { out.Sandbox = checkSandbox(ctx, d.SandboxInventory) })
		run(func(ctx context.Context) { out.Postgres = checkPostgres(ctx, d.DataStores) })
		run(func(ctx context.Context) { out.Redis = checkRedis(ctx, d.DataStores) })
		wg.Wait()
		c.JSON(http.StatusOK, out)
	}
}

// probeFailed 把一次失败的探测归为 down 或 unknown：预算用完（ctx 已结束）说明这次没查完，
// 不等于对方不可达。
func probeFailed(ctx context.Context, what string, err error) string {
	if ctx.Err() != nil {
		apiLog.Warn().Err(err).Msgf("概览：%s检查超时", what)
		return healthUnknown
	}
	apiLog.Warn().Err(err).Msgf("概览：%s不可用", what)
	return healthDown
}

// checkRunner：没有在线实例为 down（没人消费队列，任务只会排队）；有实例在停机中（不再取新任务）
// 为 warn。实例列表来自队列库的心跳登记，查询本身没有 ctx，按预算另起等待。
func checkRunner(ctx context.Context, api RunnerRegistryAPI) runnerHealth {
	h := runnerHealth{State: healthUnknown, Instances: []runnerInstance{}}
	if api == nil {
		return h
	}
	type result struct {
		runners []worker.Runner
		err     error
	}
	done := make(chan result, 1)
	go func() {
		rs, err := api.Runners()
		done <- result{rs, err}
	}()
	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		apiLog.Warn().Msg("概览：runner 检查超时")
		return h
	}
	if res.err != nil {
		apiLog.Warn().Err(res.err).Msg("概览：读 runner 实例失败")
		return h // 读不到登记（Redis 故障）不等于 runner 不在，Redis 一项会报出来
	}
	h.State = healthDown
	if len(res.runners) == 0 {
		return h
	}
	h.State = healthOK
	for _, r := range res.runners {
		h.Instances = append(h.Instances, runnerInstance{
			Host: r.Host, PID: r.PID, StartedAt: r.StartedAt,
			Concurrency: r.Concurrency, Active: r.Active, Status: r.Status,
		})
		if r.Status != worker.RunnerStatusActive {
			h.State = healthWarn
		}
	}
	return h
}

// checkProxy：心跳在线为 ok，否则 down；流量是附带的读数（读不到不影响状态）。
func checkProxy(ctx context.Context, api ProxyStatusAPI, traffic ProxyTrafficAPI) proxyHealth {
	h := proxyHealth{State: healthUnknown}
	if api == nil {
		return h
	}
	rec, ok, err := api.Get(ctx)
	if err != nil {
		apiLog.Warn().Err(err).Msg("概览：读代理状态失败")
		return h
	}
	h.State = healthDown
	if !ok {
		return h // 从未运行过
	}
	seen := rec.HeartbeatAt
	h.LastSeenAt, h.LastRequestAt = &seen, rec.LastRequestAt
	if rec.Online(time.Now()) {
		h.State = healthOK
	}
	if traffic != nil {
		if n, err := traffic.CountSince(ctx, time.Now().Add(-proxyTrafficWindow)); err != nil {
			apiLog.Warn().Err(err).Msg("概览：统计代理流量失败")
		} else {
			h.RequestsLastHour = &n
		}
	}
	return h
}

// checkSandbox：可达且工具与浏览器齐全为 ok，缺任何一样为 warn（相关能力在任务里不可用）。
func checkSandbox(ctx context.Context, inv SandboxInventoryAPI) sandboxHealth {
	h := sandboxHealth{State: healthUnknown}
	if inv == nil {
		return h
	}
	got, err := inv.Inventory(ctx)
	if err != nil {
		h.State = probeFailed(ctx, "沙盒", err)
		return h
	}
	h.Reachable = true
	h.ToolsTotal = len(got.Tools)
	for _, ok := range got.Tools {
		if ok {
			h.ToolsInstalled++
		}
	}
	h.Browser = got.Browser.Installed
	h.State = healthOK
	if h.ToolsInstalled < h.ToolsTotal || !h.Browser {
		h.State = healthWarn
	}
	return h
}

// checkPostgres：往返慢或数据库连接接近上限为 warn。
func checkPostgres(ctx context.Context, api DataStoresAPI) postgresHealth {
	h := postgresHealth{State: healthUnknown}
	if api == nil {
		return h
	}
	st, err := api.Postgres(ctx)
	if err != nil {
		h.State = probeFailed(ctx, "PostgreSQL ", err)
		return h
	}
	h.Version, h.LatencyMs, h.Connections, h.MaxConnections = st.Version, millis(st.Latency), st.Connections, st.MaxConnections
	h.State = healthOK
	if st.Latency > slowProbe || nearLimit(int64(st.Connections), int64(st.MaxConnections)) {
		h.State = healthWarn
	}
	return h
}

// checkRedis：往返慢或已用内存接近上限为 warn。
func checkRedis(ctx context.Context, api DataStoresAPI) redisHealth {
	h := redisHealth{State: healthUnknown}
	if api == nil {
		return h
	}
	st, err := api.RedisStatus(ctx)
	if err != nil {
		h.State = probeFailed(ctx, "Redis ", err)
		return h
	}
	h.Version, h.LatencyMs, h.UsedMemoryBytes, h.MaxMemoryBytes = st.Version, millis(st.Latency), st.UsedMemory, st.MaxMemory
	h.State = healthOK
	if st.Latency > slowProbe || nearLimit(st.UsedMemory, st.MaxMemory) {
		h.State = healthWarn
	}
	return h
}

// nearLimit 报告占用是否接近上限；上限为 0 表示没有上限。
func nearLimit(used, limit int64) bool {
	return limit > 0 && float64(used) >= saturationWarnRatio*float64(limit)
}

// millis 把耗时换成毫秒（保留一位小数：同处内网的往返常在 1 毫秒以内）。
func millis(d time.Duration) float64 {
	return math.Round(float64(d)/float64(time.Millisecond)*10) / 10
}
