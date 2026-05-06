// Package main 是 liusha 端到端验收触发器，按 profile 选漏洞类型与样本。
//
// CLI 用法：
//   go run ./cmd/e2e            # 不加参数 = 跑所有 profile
//   go run ./cmd/e2e bac        # 只跑 bac
//   go run ./cmd/e2e sqli       # 只跑 sqli
//   go run ./cmd/e2e bac sqli   # 多选
//
// 流程（每个 profile 独立跑）：
//  1. POST /credential/batch 一次预录所有 profile 全部 host 的凭证（启动期，不论 args）
//  2. POST /engagement/proxy 懒创建 engagement（同 host 幂等）
//  3. 读 sample 文件 → net.Dial 直连 proxify 写 raw bytes（不解析 headers/body）
//  4. 轮询 finding 表直到 ≥minFindings 条 <kindPrefix>* 且 ≥minKinds 类齐全
//
// 内置 profile：
//   - bac ：localhost 三身份正常流量 → 期望 ≥3 条 bac.* / 3 类齐全
//   - sqli：DVWA 49.234.23.42 单流量 → 期望 ≥1 条 sqli.*
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
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

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

// profilePlan 是单个 profile 的运行计划：解析后的 host + 加载好的样本。
type profilePlan struct {
	prof      profile
	host      string
	samples   []string
	samplePth string
}

func main() {
	logger := logx.New("e2e")
	ctx := context.Background()

	apiBase := envOr("LIUSHA_API_BASE", "http://localhost:8080")
	apiKey := envOr("LIUSHA_API_KEY", "changeme-dev-key")
	pgDSN := envOr("LIUSHA_POSTGRES_DSN", "postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable")
	proxyURL := envOr("LIUSHA_PROXY_ADDR", "http://localhost:8888")
	vulnBase := envOr("LIUSHA_VULNAPP_BASE", "http://host.docker.internal:8001")

	selected, err := selectProfiles(os.Args[1:])
	if err != nil {
		logger.Fatal().Err(err).Msg("select profiles")
	}

	proxyHostPort, err := extractHostPort(proxyURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("parse proxy addr")
	}

	// 1. 准备所有被选 profile 的 plan（加载样本 / 解析 host）
	plans, err := buildPlans(selected, vulnBase)
	if err != nil {
		logger.Fatal().Err(err).Msg("build plans")
	}

	// 2. 启动期一次预录所有 profile（不只是被选的）的全部 host 凭证。
	//    这样无论本次跑哪个 profile，子 ReAct fetch_credentials 都有完整身份池可用。
	if err := enrollAllCreds(apiBase, apiKey, vulnBase); err != nil {
		logger.Fatal().Err(err).Msg("enroll all credentials")
	}
	logger.Info().Msg("all credentials enrolled (across every known profile)")

	// 3. 共享 PG pool（所有 profile 共用）
	pool, err := db.NewPgPool(ctx, pgDSN, 5, 1)
	if err != nil {
		logger.Fatal().Err(err).Msg("pg")
	}
	defer pool.Close()

	// 4. 串行跑每个 plan；任一失败标记 fail，全部跑完才退出
	failed := []string{}
	for _, plan := range plans {
		if err := runProfile(ctx, plan, proxyHostPort, apiBase, apiKey, pool, logger); err != nil {
			logger.Error().Err(err).Str("profile", plan.prof.name).Msg("profile FAIL")
			failed = append(failed, plan.prof.name)
		}
	}

	if len(failed) > 0 {
		logger.Error().Strs("failed_profiles", failed).Msg("e2e 部分失败")
		fmt.Printf("✗ e2e 失败 profile=[%s]\n", strings.Join(failed, ","))
		os.Exit(len(failed))
	}
	names := make([]string, len(plans))
	for i, p := range plans {
		names[i] = p.prof.name
	}
	fmt.Printf("✓ e2e 全部通过 profile=[%s]\n", strings.Join(names, ","))
}

// selectProfiles 解析 CLI args；空 = 全部 profile（按名字字典序）。
// 未知 profile 立即报错，避免静默忽略。
func selectProfiles(args []string) ([]profile, error) {
	if len(args) == 0 {
		all := make([]profile, 0, len(profiles))
		for _, p := range profiles {
			all = append(all, p)
		}
		sort.Slice(all, func(i, j int) bool { return all[i].name < all[j].name })
		return all, nil
	}
	out := make([]profile, 0, len(args))
	seen := map[string]struct{}{}
	for _, a := range args {
		key := strings.ToLower(strings.TrimSpace(a))
		if key == "" {
			continue
		}
		p, ok := profiles[key]
		if !ok {
			known := make([]string, 0, len(profiles))
			for k := range profiles {
				known = append(known, k)
			}
			sort.Strings(known)
			return nil, fmt.Errorf("未知 profile %q（可选：%s）", a, strings.Join(known, ", "))
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, p)
	}
	return out, nil
}

// buildPlans 为每个被选 profile 加载样本并解析 scope_host。
func buildPlans(selected []profile, vulnBase string) ([]profilePlan, error) {
	out := make([]profilePlan, 0, len(selected))
	for _, p := range selected {
		samples, err := loadRawSamples(p.defaultSamples)
		if err != nil {
			return nil, fmt.Errorf("load samples for %s: %w", p.name, err)
		}
		host, err := resolveScopeHost(vulnBase, samples)
		if err != nil {
			return nil, fmt.Errorf("resolve scope host for %s: %w", p.name, err)
		}
		out = append(out, profilePlan{prof: p, host: host, samples: samples, samplePth: p.defaultSamples})
	}
	return out, nil
}

// enrollAllCreds 一次写入"所有已注册 profile"对应 host 的全部身份。
// 不论 args 选了哪些 profile，这里都把所有 profile 的凭证池预填——子 ReAct
// fetch_credentials 在 SKILL 中可能跨 profile 引用，提前录入更省事。
func enrollAllCreds(apiBase, apiKey, vulnBase string) error {
	hostCreds := map[string][]credentialEntry{}
	for _, p := range profiles {
		samples, err := loadRawSamples(p.defaultSamples)
		if err != nil {
			return fmt.Errorf("load samples for %s: %w", p.name, err)
		}
		host, err := resolveScopeHost(vulnBase, samples)
		if err != nil {
			return fmt.Errorf("resolve host for %s: %w", p.name, err)
		}
		hostCreds[host] = append(hostCreds[host], p.credsForHost(host)...)
	}
	return saveCredsBatch(apiBase, apiKey, hostCreds)
}

// runProfile 跑单个 profile 的完整生命周期：建 engagement → 发样本 → 轮询 finding。
func runProfile(ctx context.Context, plan profilePlan, proxyHostPort, apiBase, apiKey string, pool *pgxpool.Pool, logger zerolog.Logger) error {
	logger.Info().
		Str("profile", plan.prof.name).
		Str("scope_host", plan.host).
		Str("samples", plan.samplePth).
		Int("sample_count", len(plan.samples)).
		Msg("profile starting")

	eid, err := createProxyEngagement(apiBase, apiKey, plan.host)
	if err != nil {
		return fmt.Errorf("create engagement: %w", err)
	}
	logger.Info().Str("profile", plan.prof.name).Str("engagement_id", eid).Msg("engagement ready")

	for i, raw := range plan.samples {
		if err := dispatchRaw(proxyHostPort, raw); err != nil {
			return fmt.Errorf("dispatch sample %d: %w", i, err)
		}
		logger.Info().Str("profile", plan.prof.name).Int("idx", i).Msg("raw dispatched")
	}

	store := vulnfinding.NewStore(pool)
	deadline := time.Now().Add(pollDeadline)
	for time.Now().Before(deadline) {
		all, err := store.ListByEngagement(ctx, eid)
		if err != nil {
			logger.Warn().Str("profile", plan.prof.name).Err(err).Msg("list findings")
			time.Sleep(pollInterval)
			continue
		}
		matched := filterByPrefix(all, plan.prof.kindPrefix)
		kinds := countKinds(matched)
		logger.Info().
			Str("profile", plan.prof.name).
			Int("count", len(matched)).
			Interface("kinds", kinds).
			Msg("poll")
		if len(matched) >= plan.prof.minFindings && len(kinds) >= plan.prof.minKinds {
			logger.Info().
				Str("profile", plan.prof.name).
				Int("count", len(matched)).
				Int("kinds", len(kinds)).
				Msg("profile PASS")
			fmt.Printf("✓ profile=%s findings=%d kinds=%d\n", plan.prof.name, len(matched), len(kinds))
			return nil
		}
		time.Sleep(pollInterval)
	}
	return fmt.Errorf("timeout: 未达 finding/类覆盖门槛")
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
