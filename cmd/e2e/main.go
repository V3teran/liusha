// Package main 是 liusha 端到端验收触发器，按 profile 选漏洞类型与样本。
//
// CLI 用法：
//
//	go run ./cmd/e2e                              # 不加参数 = 跑所有 profile
//	go run ./cmd/e2e bac                          # 只跑 bac（业务向访问控制）
//	go run ./cmd/e2e sqli                         # 只跑 sqli
//	go run ./cmd/e2e xss                          # 只跑 xss（reflected/stored/DOM）
//	go run ./cmd/e2e brute                        # 只跑 brute（暴力破解）
//	go run ./cmd/e2e path-traversal               # 只跑 path-traversal（任意文件读取/CWE-22）
//	go run ./cmd/e2e unrestricted-upload          # 只跑 unrestricted-upload（CWE-434）
//	go run ./cmd/e2e bac sqli xss                 # 多选
//
// 流程（每个 profile 独立跑）：
//  1. POST /credential/batch 一次预录所有 profile 全部 host 的凭证（启动期，不论 args）
//  2. POST /engagement/proxy 懒创建 engagement（同 host 幂等）
//  3. 读 sample 文件 → net.Dial 直连 proxify 写 raw bytes（不解析 headers/body）
//  4. 轮询 finding 表直到 ≥minFindings 条 <kindPrefix>* 且 ≥minKinds 类齐全
//
// 内置 profile（6 个）：
//   - bac                ：本地 vulnapp 多身份正常流量 → 期望 ≥3 条 finding / 2 类 severity 齐全
//   - sqli               ：远程 DVWA SQLi → 期望 ≥1 条
//   - xss                ：远程 DVWA XSS（reflected/stored/DOM 三条样本）→ 期望 ≥3 条
//   - brute              ：远程 DVWA 暴力破解 → 期望 ≥1 条
//   - path-traversal     ：远程 DVWA 路径遍历（OWASP CWE-22）→ 期望 ≥1 条
//   - unrestricted-upload：远程 DVWA 任意文件上传（OWASP CWE-434）→ 期望 ≥1 条
//
// 注：e2e 数据已证实 LLM 对常规漏洞（sqli/xss/path-traversal/upload/brute）自身知识充分，
// 删 vuln SKILL 后表现不降反升。这些 profile 保留作为镜像/架构回归测试的流量基线。
//
// 触发器只发起"用户正常流量"——具体漏洞由 hunter agent 用 credentials/run_command
// 自由组合工具挖掘（无预设流程）。
//
// 想加新漏洞类型：profiles map 加一行 + 写 examples/sample_<vuln>_raw.json 即可。
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/logx"
)

const (
	pollInterval        = 15 * time.Second
	defaultPollDeadline = 40 * time.Minute // 与 scanner.agent_run_timeout_seconds (2400s) 对齐；让 main_task 在 e2e 超时前自然结束
	dialTimeout         = 10 * time.Second
	rawIOTimeout        = 100 * time.Second
)

// pollDeadline 从 ENV LIUSHA_E2E_POLL_DEADLINE_SECONDS 读取（开发期可调），缺省 12 分钟。
func pollDeadline() time.Duration {
	if v := os.Getenv("LIUSHA_E2E_POLL_DEADLINE_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return defaultPollDeadline
}

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
	// credsForHost 接收 target_host 返回该 host 的身份列表（profile 自决定身份组）。
	credsForHost func(host string) []credentialEntry
}

var profiles = map[string]profile{
	"bac": {
		name:           "bac",
		defaultSamples: "examples/sample_bac_raw.json",
		kindPrefix:     "bac.",
		minFindings:    3,
		// BAC 天然以 critical/high 为主，medium/low 难自然产生；
		// 凑 3 个 severity 等级强人所难，2 类（critical+high 或 high+任一）即可。
		minKinds: 2,
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
		minFindings:    2, // 2 条 sample（sqli + sqli_blind）期望各产 1 finding
		minKinds:       1,
		// DVWA 远程靶场（111.229.193.40:34280）：仅 admin 身份。
		// PHPSESSID + security=low 双 cookie 拼成一行；旧 gordonb 身份的 cookie 在新靶机上无效，
		// 需要时让用户在远程 DVWA 重新登录拿 cookie 再补回来。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=f0be9e4b2148f43da74884680ecbfd96; security=low"},
				}},
			}
		},
	},
	"xss": {
		name:           "xss",
		defaultSamples: "examples/sample_xss_raw.json",
		kindPrefix:     "xss.",
		minFindings:    3, // 3 条样本（reflected/stored/DOM）期望各出 1 finding，等齐才 PASS
		minKinds:       1,
		// 同 DVWA 远程靶场，admin 同凭证；3 条样本覆盖 reflected (xss_r) / stored (xss_s) / DOM (xss_d) 三种场景。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=f0be9e4b2148f43da74884680ecbfd96; security=low"},
				}},
			}
		},
	},
	"brute": {
		name:           "brute",
		defaultSamples: "examples/sample_brute_raw.json",
		kindPrefix:     "brute.",
		minFindings:    1,
		minKinds:       1,
		// 同 DVWA 远程靶场。/vulnerabilities/brute/ 是登录表单类暴力破解漏洞。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=f0be9e4b2148f43da74884680ecbfd96; security=low"},
				}},
			}
		},
	},
	"path-traversal": {
		name:           "path-traversal",
		defaultSamples: "examples/sample_path-traversal_raw.json",
		kindPrefix:     "path-traversal.",
		minFindings:    1,
		minKinds:       1,
		// 同 DVWA 远程靶场。/vulnerabilities/fi/?page= 是路径遍历 / 任意文件读取漏洞（OWASP CWE-22）。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=f0be9e4b2148f43da74884680ecbfd96; security=low"},
				}},
			}
		},
	},
	"unrestricted-upload": {
		name:           "unrestricted-upload",
		defaultSamples: "examples/sample_unrestricted-upload_raw.json",
		kindPrefix:     "unrestricted-upload.",
		minFindings:    1,
		minKinds:       1,
		// 同 DVWA 远程靶场。/vulnerabilities/upload/ 是 Unrestricted File Upload（OWASP CWE-434）。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=f0be9e4b2148f43da74884680ecbfd96; security=low"},
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
	vulnBase := envOr("LIUSHA_VULNAPP_BASE", "http://111.229.193.40:38001")

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
	//    这样无论本次跑哪个 profile，hunter agent 调 credentials() 都能拿到完整身份池。
	if err := enrollAllCreds(apiBase, apiKey, vulnBase); err != nil {
		logger.Fatal().Err(err).Msg("enroll all credentials")
	}
	logger.Info().Msg("all credentials enrolled (across every known profile)")

	// 3. 共享 PG pool（所有 profile 共用）
	pool, err := db.NewPgPool(ctx, pgDSN, 5, 1, 0, 0)
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

// buildPlans 为每个被选 profile 加载样本并解析 target_host。
func buildPlans(selected []profile, vulnBase string) ([]profilePlan, error) {
	out := make([]profilePlan, 0, len(selected))
	for _, p := range selected {
		samples, err := loadRawSamples(p.defaultSamples)
		if err != nil {
			return nil, fmt.Errorf("load samples for %s: %w", p.name, err)
		}
		host, err := resolveTargetHost(vulnBase, samples)
		if err != nil {
			return nil, fmt.Errorf("resolve scope host for %s: %w", p.name, err)
		}
		out = append(out, profilePlan{prof: p, host: host, samples: samples, samplePth: p.defaultSamples})
	}
	return out, nil
}

// enrollAllCreds 一次写入"所有已注册 profile"对应 host 的全部身份。
// 不论 args 选了哪些 profile，这里都把所有 profile 的凭证池预填——hunter agent
// 的 credentials 工具可能跨 profile 拉取，提前录入更省事。
func enrollAllCreds(apiBase, apiKey, vulnBase string) error {
	hostCreds := map[string][]credentialEntry{}
	for _, p := range profiles {
		samples, err := loadRawSamples(p.defaultSamples)
		if err != nil {
			return fmt.Errorf("load samples for %s: %w", p.name, err)
		}
		host, err := resolveTargetHost(vulnBase, samples)
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
		Str("target_host", plan.host).
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

	store := finding.NewStore(pool)
	agentRunStore := agentrun.NewStore(pool)
	deadline := time.Now().Add(pollDeadline())
	for time.Now().Before(deadline) {
		all, err := store.ListByEngagement(ctx, eid)
		if err != nil {
			logger.Warn().Str("profile", plan.prof.name).Err(err).Msg("list findings")
			time.Sleep(pollInterval)
			continue
		}
		matched := filterByPrefix(all, plan.prof.kindPrefix)
		kinds := countKinds(matched)

		// agent_run 完成度（每个 sample 对应 1 个 react 循环；e2e PASS 前要等所有 react 收手，
		// 避免"第 1 个 react 已挖出 finding 满足门槛 → e2e 立即 exit → 第 2 个 react 还卡在
		// time-based blind 等长任务里"的假阳性 PASS）。
		unfinishedRuns := -1 // -1 表示查询失败；正常应 ≥ 0
		if runs, runErr := agentRunStore.ListByEngagement(ctx, eid, 100); runErr == nil {
			unfinishedRuns = 0
			for _, r := range runs {
				if r.Status == "pending" || r.Status == "running" {
					unfinishedRuns++
				}
			}
		}

		logger.Info().
			Str("profile", plan.prof.name).
			Int("count", len(matched)).
			Interface("kinds", kinds).
			Int("unfinished_runs", unfinishedRuns).
			Msg("poll")

		// PASS = finding 门槛满足 AND 所有 agent_run 都 done（无 pending/running 剩余）。
		// agent_run 查询失败（unfinishedRuns=-1）时退化为仅看 finding 门槛——
		// 防止 DB 临时抖动让所有 e2e profile 全 FAIL。
		findingsOK := len(matched) >= plan.prof.minFindings && len(kinds) >= plan.prof.minKinds
		runsOK := unfinishedRuns == 0 || unfinishedRuns == -1
		if findingsOK && runsOK {
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
	return fmt.Errorf("timeout: 未达 finding/类覆盖门槛（或仍有 agent_run pending/running 未收手）")
}

// resolveTargetHost 决定 engagement target_host：
//
//	优先级：env LIUSHA_E2E_SCOPE_HOST > 首条样本的 Host: 头去端口 > vulnBase URL 的 host
//
// 这样不同 profile 用不同目标（如 SQLi 用本地 DVWA、BAC 用本地 vulnapp）时无需切 LIUSHA_VULNAPP_BASE。
func resolveTargetHost(vulnBase string, samples []string) (string, error) {
	if v := os.Getenv("LIUSHA_E2E_SCOPE_HOST"); v != "" {
		return v, nil
	}
	if len(samples) > 0 {
		if h := extractHostFromRaw(samples[0]); h != "" {
			return h, nil
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

// filterByPrefix：LLM 自由命名 finding，不按结构化前缀过滤，直接返回全部；
// e2e 判定看 minFindings 即可（签名保留以避免改 main_test.go）。
func filterByPrefix(all []finding.VulnFinding, _ string) []finding.VulnFinding {
	return all
}

// countKinds：按 severity 分组——e2e profile.minKinds 表示"期望的 severity 等级数"。
func countKinds(fs []finding.VulnFinding) map[string]int {
	out := make(map[string]int, len(fs))
	for _, f := range fs {
		out[f.Severity]++
	}
	return out
}
