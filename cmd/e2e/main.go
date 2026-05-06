// Package main 是 liusha 端到端验收触发器，按 profile 选漏洞类型与样本。
//
// 完整流程：
//  1. POST /engagement/proxy 懒创建 engagement（同 host 幂等）
//  2. POST /credential/batch 录身份（profile 决定具体身份集）
//  3. 读 sample 文件（一组 raw HTTP/1.1 报文）
//  4. 用 net.Dial 直连 proxify(:8888) 写 raw bytes（proxy 内置 sanitizer 做
//     relative→absolute URI 改写），不解析 headers/body
//  5. 轮询 finding 表，直到 ≥minFindings 条 <kindPrefix>* 且 ≥minKinds 类齐全
//
// Profile 切换：
//   - LIUSHA_E2E_PROFILE=bac（默认）：3 身份正常流量 → 期望 ≥3 条 bac.* / 3 类齐全
//   - LIUSHA_E2E_PROFILE=sqli       ：DVWA 单流量      → 期望 ≥1 条 sqli.*
//
// 触发器只发起"用户正常流量"——具体漏洞由子 ReAct 内部 fetch_credentials +
// replay_matrix（BAC）或 run_command 容器化沙箱（SQLi）多 payload 重放发现。
//
// 想加新漏洞类型：profiles map 加一行 + 写 examples/sample_<vuln>_raw.json 即可。
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/vulnfinding"
)

const (
	pollInterval = 15 * time.Second
	pollDeadline = 6 * time.Minute
	dialTimeout  = 10 * time.Second
	rawIOTimeout = 100 * time.Second
)

// credentialEntry 是 /credential/batch 单条身份记录的结构。
type credentialEntry struct {
	Name        string              `json:"name"`
	Role        string              `json:"role"`
	Credentials []map[string]string `json:"credentials"`
}

// profile 描述一个 e2e 验收剧本（BAC / SQLi）：sample 文件 + 身份集 + 验收门槛。
type profile struct {
	name           string
	defaultSamples string
	kindPrefix     string
	minFindings    int
	minKinds       int
	// credsForHost 接收 scope_host 返回该 host 的身份列表（profile 自决定身份组）。
	credsForHost func(host string) []credentialEntry
}

var profiles = map[string]profile{
	"bac": {
		name:           "bac",
		defaultSamples: "examples/sample_bac_raw.json",
		kindPrefix:     "bac.",
		minFindings:    3,
		minKinds:       3,
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "session=admin_sess_a1b2c3"},
				}},
				{Name: "test", Role: "user", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "session=test_sess_d4e5f6"},
				}},
				{Name: "m233241", Role: "user", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "session=m233241_sess_g7h8i9"},
				}},
			}
		},
	},
	"sqli": {
		name:           "sqli",
		defaultSamples: "examples/sample_sqli_raw.json",
		kindPrefix:     "sqli.",
		minFindings:    1,
		minKinds:       1,
		// DVWA 风格：单一已认证 admin 身份（PHPSESSID + security=low 双 cookie 拼成一行）。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=333lg0l6qt4p9u48aktuquo5r3; security=low"},
				}},
			}
		},
	},
}

func main() {
	logger := logx.New("e2e")
	ctx := context.Background()

	apiBase := envOr("LIUSHA_API_BASE", "http://localhost:8080")
	apiKey := envOr("LIUSHA_API_KEY", "changeme-dev-key")
	pgDSN := envOr("LIUSHA_POSTGRES_DSN", "postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable")
	proxyURL := envOr("LIUSHA_PROXY_ADDR", "http://localhost:8888")
	vulnBase := envOr("LIUSHA_VULNAPP_BASE", "http://host.docker.internal:8001")

	prof, err := selectProfile(envOr("LIUSHA_E2E_PROFILE", "bac"))
	if err != nil {
		logger.Fatal().Err(err).Msg("select profile")
	}
	samplesPath := envOr("LIUSHA_E2E_SAMPLES", prof.defaultSamples)

	proxyHostPort, err := extractHostPort(proxyURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("parse proxy addr")
	}

	samples, err := loadRawSamples(samplesPath)
	if err != nil {
		logger.Fatal().Err(err).Msg("load samples")
	}

	scopeHost, err := resolveScopeHost(vulnBase, samples)
	if err != nil {
		logger.Fatal().Err(err).Msg("resolve scope host")
	}
	logger.Info().
		Str("profile", prof.name).
		Str("scope_host", scopeHost).
		Str("proxy", proxyHostPort).
		Str("samples", samplesPath).
		Int("sample_count", len(samples)).
		Msg("e2e starting")

	eid, err := createProxyEngagement(apiBase, apiKey, scopeHost)
	if err != nil {
		logger.Fatal().Err(err).Msg("create engagement")
	}
	logger.Info().Str("engagement_id", eid).Msg("engagement ready")

	if err := saveCreds(apiBase, apiKey, scopeHost, prof.credsForHost(scopeHost)); err != nil {
		logger.Fatal().Err(err).Msg("save credentials")
	}
	logger.Info().Msg("credentials enrolled")

	logger.Info().Int("samples", len(samples)).Msg("samples loaded")

	for i, raw := range samples {
		if err := dispatchRaw(proxyHostPort, raw); err != nil {
			logger.Fatal().Err(err).Int("idx", i).Msg("dispatch raw")
		}
		logger.Info().Int("idx", i).Msg("raw dispatched via proxy")
	}

	pool, err := db.NewPgPool(ctx, pgDSN, 5, 1)
	if err != nil {
		logger.Fatal().Err(err).Msg("pg")
	}
	defer pool.Close()
	store := vulnfinding.NewStore(pool)

	deadline := time.Now().Add(pollDeadline)
	for time.Now().Before(deadline) {
		all, err := store.ListByEngagement(ctx, eid)
		if err != nil {
			logger.Warn().Err(err).Msg("list findings")
			time.Sleep(pollInterval)
			continue
		}
		matched := filterByPrefix(all, prof.kindPrefix)
		kinds := countKinds(matched)
		logger.Info().
			Str("profile", prof.name).
			Int("count", len(matched)).
			Interface("kinds", kinds).
			Msg("poll")
		if len(matched) >= prof.minFindings && len(kinds) >= prof.minKinds {
			logger.Info().Int("count", len(matched)).Int("kinds", len(kinds)).Msg("e2e PASS")
			fmt.Printf("✓ e2e 验收通过：%s findings=%d kinds=%d\n", prof.name, len(matched), len(kinds))
			return
		}
		time.Sleep(pollInterval)
	}
	logger.Error().Msg("e2e timeout: 未达 finding/类覆盖门槛")
	os.Exit(1)
}

// selectProfile 按名字解析 profile；未知值报错（避免静默退化）。
func selectProfile(name string) (profile, error) {
	p, ok := profiles[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		known := make([]string, 0, len(profiles))
		for k := range profiles {
			known = append(known, k)
		}
		return profile{}, fmt.Errorf("未知 profile %q（可选：%s）", name, strings.Join(known, ", "))
	}
	return p, nil
}

// resolveScopeHost 决定 engagement scope_host：
//
//	优先级：env LIUSHA_E2E_SCOPE_HOST > 首条样本的 Host: 头去端口 > vulnBase URL 的 host
//
// 这样 SQLi 用外网 DVWA 时无需配 LIUSHA_VULNAPP_BASE，BAC 用本地 vulnapp 时也不破坏旧行为。
func resolveScopeHost(vulnBase string, samples []string) (string, error) {
	if v := os.Getenv("LIUSHA_E2E_SCOPE_HOST"); v != "" {
		return v, nil
	}
	if len(samples) > 0 {
		if h := extractHostFromRaw(samples[0]); h != "" {
			return stripPort(h), nil
		}
	}
	return extractHost(vulnBase)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// filterByPrefix 过滤 kind 以 prefix 开头的 finding（profile 用）。
func filterByPrefix(all []vulnfinding.VulnFinding, prefix string) []vulnfinding.VulnFinding {
	out := make([]vulnfinding.VulnFinding, 0, len(all))
	for _, f := range all {
		if strings.HasPrefix(f.Kind, prefix) {
			out = append(out, f)
		}
	}
	return out
}

// countKinds 统计 finding 切片中各 kind 的出现次数。
func countKinds(fs []vulnfinding.VulnFinding) map[string]int {
	out := make(map[string]int, len(fs))
	for _, f := range fs {
		out[f.Kind]++
	}
	return out
}
