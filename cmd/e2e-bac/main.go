// Package main 是 liusha BAC 端到端验收触发器：
//
// 完整流程：
//  1. 通过 POST /engagement/proxy 懒创建 vulnapp engagement（与 proxify_consumer 端共用同一 host 索引，幂等）；
//  2. 通过 POST /credential/batch 录入 admin/test/m233241 三套 cookie 凭证；
//  3. 设 HTTP_PROXY=proxify(8888)，向 vulnapp 打 18 次请求覆盖 5 类 BAC 场景；
//  4. 轮询 finding 表，直到出现 ≥5 条 bac.* finding 且至少 3 类齐全，否则 6 分钟超时退出 1。
//
// 本程序假设完整 docker-compose stack（含 proxify + vulnapp + agent-worker）已就绪——
// 它只负责"敲门 + 验收"，不负责拉起依赖。容器编排由 Task 10 的 e2e 脚本/profile 处理。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/logx"
)

// pollInterval 是 finding 轮询节拍；总超时 pollDeadline。
// 节奏取自 plan §9：15s tick × 6min budget — 给 worker（observer + distill + replayer）留足端到端时间。
const (
	pollInterval = 15 * time.Second
	pollDeadline = 6 * time.Minute
	// minBACFindings 是退出码 0 的最低 finding 数门槛。
	minBACFindings = 5
	// minBACKinds 是退出码 0 的最低 finding 类别覆盖度门槛。
	minBACKinds = 3
)

func main() {
	logger := logx.New("e2e-bac")
	ctx := context.Background()

	apiBase := envOr("LIUSHA_API_BASE", "http://localhost:8080")
	apiKey := envOr("LIUSHA_API_KEY", "changeme-dev-key")
	pgDSN := envOr("LIUSHA_POSTGRES_DSN", "postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable")
	proxyAddr := envOr("LIUSHA_PROXY_ADDR", "http://localhost:8888")
	vulnBase := envOr("LIUSHA_VULNAPP_BASE", "http://host.docker.internal:8001")

	eid, err := createProxyEngagement(apiBase, apiKey, "vulnapp")
	if err != nil {
		logger.Fatal().Err(err).Msg("create engagement")
	}
	logger.Info().Str("engagement_id", eid).Msg("engagement ready")

	if err := saveCreds(apiBase, apiKey); err != nil {
		logger.Fatal().Err(err).Msg("save credentials")
	}
	logger.Info().Msg("credentials enrolled")

	calls := proxyRequests()
	if err := drive(proxyAddr, vulnBase, calls); err != nil {
		logger.Fatal().Err(err).Msg("drive vulnapp")
	}
	logger.Info().Int("requests", len(calls)).Msg("proxy traffic dispatched")

	pool, err := db.NewPgPool(ctx, pgDSN, 5, 1)
	if err != nil {
		logger.Fatal().Err(err).Msg("pg")
	}
	defer pool.Close()
	store := finding.NewStore(pool)

	deadline := time.Now().Add(pollDeadline)
	for time.Now().Before(deadline) {
		all, err := store.ListByEngagement(ctx, eid)
		if err != nil {
			logger.Warn().Err(err).Msg("list findings")
			time.Sleep(pollInterval)
			continue
		}
		bac := filterBAC(all)
		kinds := countKinds(bac)
		logger.Info().Int("bac", len(bac)).Interface("kinds", kinds).Msg("poll")
		if len(bac) >= minBACFindings && len(kinds) >= minBACKinds {
			logger.Info().Int("bac", len(bac)).Int("kinds", len(kinds)).Msg("e2e PASS")
			return
		}
		time.Sleep(pollInterval)
	}
	logger.Error().Msg("e2e timeout: 未达 5 条 finding/3 类覆盖")
	os.Exit(1)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// createProxyEngagement 调 POST /engagement/proxy 拿 engagement_id；
// 后端 LookupOrCreateProxy 同 host 幂等，重复调用不会产生新 engagement。
func createProxyEngagement(base, key, host string) (string, error) {
	body, _ := json.Marshal(map[string]string{"host": host})
	req, _ := http.NewRequest(http.MethodPost, base+"/engagement/proxy", bytes.NewReader(body))
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("post engagement/proxy: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("engagement/proxy %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		EngagementID string `json:"engagement_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode engagement/proxy: %w", err)
	}
	if out.EngagementID == "" {
		return "", fmt.Errorf("engagement/proxy returned empty engagement_id")
	}
	return out.EngagementID, nil
}

// saveCreds 录入 vulnapp 三套身份的 session cookie；
// 写入是 host 维度全量替换语义，重复执行幂等。
func saveCreds(base, key string) error {
	body := []byte(`{
	  "ttl_seconds": 0,
	  "credentials": {
	    "vulnapp": [
	      {"name":"admin","role":"admin","credentials":[{"type":"headers","key":"Cookie","value":"session=admin_sess_a1b2c3"}]},
	      {"name":"test","role":"user","credentials":[{"type":"headers","key":"Cookie","value":"session=test_sess_d4e5f6"}]},
	      {"name":"m233241","role":"user","credentials":[{"type":"headers","key":"Cookie","value":"session=m233241_sess_g7h8i9"}]}
	    ]
	  }
	}`)
	req, _ := http.NewRequest(http.MethodPost, base+"/credential/batch", bytes.NewReader(body))
	req.Header.Set("X-API-Key", key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("post credential/batch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("credential/batch %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// proxyCall 描述一次"经 proxify 转发到 vulnapp"的请求形态；
// Sess=="" 表示 anonymous（不附 cookie），用于触发"未授权访问"类场景。
type proxyCall struct {
	Name   string
	Sess   string
	Method string
	Path   string
	Body   string
}

// proxyRequests 返回 18 个固定调用，覆盖 5 类 BAC 场景：
//
//	profile（baseline 健康路径）、user/info IDOR、order 读 IDOR、
//	order/cancel 写 IDOR、admin 垂直越权（含未授权 anonymous）。
//
// 每类 ×3 身份保证 Replayer 拿到同 endpoint 的多身份对照样本。
// 顺序无依赖；后续可由配置驱动而不影响调用方。
func proxyRequests() []proxyCall {
	return []proxyCall{
		// 1. baseline /api/profile：每个身份各取自己的资料（不应触发 finding，作流量基线）
		{"admin", "admin_sess_a1b2c3", http.MethodGet, "/api/profile", ""},
		{"test", "test_sess_d4e5f6", http.MethodGet, "/api/profile", ""},
		{"m233241", "m233241_sess_g7h8i9", http.MethodGet, "/api/profile", ""},

		// 2. /api/user/info?uid=1：低权限身份读 uid=1 资料 → 水平越权
		{"admin", "admin_sess_a1b2c3", http.MethodGet, "/api/user/info?uid=1", ""},
		{"test", "test_sess_d4e5f6", http.MethodGet, "/api/user/info?uid=1", ""},
		{"m233241", "m233241_sess_g7h8i9", http.MethodGet, "/api/user/info?uid=1", ""},

		// 3. /api/order/7：他人订单读 → IDOR
		{"admin", "admin_sess_a1b2c3", http.MethodGet, "/api/order/7", ""},
		{"test", "test_sess_d4e5f6", http.MethodGet, "/api/order/7", ""},
		{"m233241", "m233241_sess_g7h8i9", http.MethodGet, "/api/order/7", ""},

		// 4. POST /api/order/cancel：他人订单写 → 状态变更越权
		{"admin", "admin_sess_a1b2c3", http.MethodPost, "/api/order/cancel", `{"order_id":"7"}`},
		{"test", "test_sess_d4e5f6", http.MethodPost, "/api/order/cancel", `{"order_id":"7"}`},
		{"m233241", "m233241_sess_g7h8i9", http.MethodPost, "/api/order/cancel", `{"order_id":"7"}`},

		// 5. /api/admin/users：垂直越权（普通用户访问管理端点）
		{"admin", "admin_sess_a1b2c3", http.MethodGet, "/api/admin/users", ""},
		{"test", "test_sess_d4e5f6", http.MethodGet, "/api/admin/users", ""},
		{"m233241", "m233241_sess_g7h8i9", http.MethodGet, "/api/admin/users", ""},

		// 6. POST /api/admin/user/delete：高危管理动作，含未授权（anonymous 无 cookie）
		{"admin", "admin_sess_a1b2c3", http.MethodPost, "/api/admin/user/delete", `{"uid":"1"}`},
		{"test", "test_sess_d4e5f6", http.MethodPost, "/api/admin/user/delete", `{"uid":"1"}`},
		{"anonymous", "", http.MethodPost, "/api/admin/user/delete", `{"uid":"1"}`},
	}
}

// drive 把所有 proxyCall 通过 proxify HTTP 代理打到 vulnapp。
// 每个请求带 100s timeout（含 proxify 内部 LLM sniffer 的写盘耗时）；
// 任一请求失败立即返回，让上层 logger.Fatal 终止——避免后续轮询白等。
func drive(proxyAddr, vulnBase string, calls []proxyCall) error {
	parsedProxy, err := url.Parse(proxyAddr)
	if err != nil {
		return fmt.Errorf("parse proxy addr %q: %w", proxyAddr, err)
	}
	client := &http.Client{
		Timeout:   100 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(parsedProxy)},
	}

	for _, c := range calls {
		var body io.Reader
		if c.Body != "" {
			body = bytes.NewReader([]byte(c.Body))
		}
		req, err := http.NewRequest(c.Method, vulnBase+c.Path, body)
		if err != nil {
			return fmt.Errorf("build %s %s: %w", c.Method, c.Path, err)
		}
		if c.Body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if c.Sess != "" {
			req.AddCookie(&http.Cookie{Name: "session", Value: c.Sess})
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("call %s %s %s: %w", c.Name, c.Method, c.Path, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	return nil
}

// filterBAC 过滤 kind 以 "bac." 开头的 finding；其它 kind（如 leak.*、debug.*）跳过。
func filterBAC(all []finding.Finding) []finding.Finding {
	out := make([]finding.Finding, 0, len(all))
	for _, f := range all {
		if strings.HasPrefix(f.Kind, "bac.") {
			out = append(out, f)
		}
	}
	return out
}

// countKinds 统计 finding 切片中各 kind 的出现次数；用于"至少 N 类齐全"门槛。
func countKinds(fs []finding.Finding) map[string]int {
	out := make(map[string]int, len(fs))
	for _, f := range fs {
		out[f.Kind]++
	}
	return out
}
