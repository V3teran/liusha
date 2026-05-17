// Package main 是 liusha 端到端验收触发器，覆盖 passive + active 两种模式。
//
// CLI 用法（args 用前缀区分模式）：
//
//	go run ./cmd/e2e                              # 不加参数 = 跑所有 passive profile
//	go run ./cmd/e2e bac                          # 只跑 passive bac（业务向访问控制）
//	go run ./cmd/e2e sqli                         # 只跑 passive sqli
//	go run ./cmd/e2e xss                          # 只跑 passive xss（reflected/stored/DOM）
//	go run ./cmd/e2e bac sqli xss                 # passive 多选
//	go run ./cmd/e2e active:xss                   # 只跑 active xss（自然语言 brief 喂 hunter LLM）
//	go run ./cmd/e2e sqli active:xss              # 混合：passive sqli + active xss
//
// Passive 流程（每个 profile 独立跑）：
//  1. POST /credential/batch 一次预录所有 passive profile 全部 host 的凭证（启动期）
//  2. POST /scan/passive 懒创建 engagement（同 host 幂等）
//  3. 读 sample 文件 → net.Dial 直连 proxify 写 raw bytes（不解析 headers/body）
//  4. 轮询 finding 表 + agent_run 收手 → ≥minFindings 为 PASS
//
// Active 流程（按选中顺序串行跑）：
//  1. POST /scan/active body={"brief":"<自然语言任务简报>"} → 拿 (engagement_id, agent_run_id)
//  2. 轮询同 engagement 的 finding + agent_run → ≥minFindings 为 PASS
//
// 内置 passive profile（13 个，全部 minFindings=1）：
//   - bac                ：本地 vulnapp 多身份正常流量（4 样本，BAC/IDOR/越权）
//   - sqli               ：远程 DVWA SQLi（2 样本，sqli + sqli_blind）
//   - xss                ：远程 DVWA XSS（3 样本，reflected + stored + DOM）
//   - brute              ：远程 DVWA 暴力破解
//   - path-traversal     ：远程 DVWA 路径遍历（OWASP CWE-22）
//   - unrestricted-upload：远程 DVWA 任意文件上传（OWASP CWE-434）
//   - csrf               ：远程 DVWA CSRF（OWASP CWE-352）
//   - api                ：远程 DVWA API 端点漏洞
//   - cryptography       ：远程 DVWA 密码学漏洞（OWASP CWE-310/327）
//   - redirect           ：远程 DVWA 开放重定向（OWASP CWE-601）
//   - authbypass         ：远程 DVWA 认证绕过（OWASP CWE-287）
//   - csp                ：远程 DVWA CSP 配置问题（OWASP CWE-1021）
//   - exec               ：远程 DVWA 命令注入（OWASP CWE-77/78）
//
// 内置 active profile（1 个 demo）：
//   - active:xss         ：远程 DVWA login.php → 自然语言指令"账号 admin/password，只测 XSS"
//
// 注：e2e 数据已证实 LLM 对常规漏洞（sqli/xss/path-traversal/upload/brute）自身知识充分，
// 删 vuln SKILL 后表现不降反升。passive profile 保留作为镜像/架构回归测试的流量基线。
//
// Passive 触发器只发起"用户正常流量"——具体漏洞由 hunter agent 用 credentials/run_command
// 自由组合工具挖掘（无预设流程）；active 模式则把 brief 自然语言直接喂 LLM 自主扫描。
//
// 想加新漏洞类型：
//   - passive：profiles map 加一行 + 写 examples/sample_<vuln>_raw.json
//   - active： activeProfiles map 加一行（带 brief 自然语言描述）
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// profile 描述一个 passive 模式 e2e 验收剧本（BAC / SQLi）：sample 文件 + 身份集 + 验收门槛。
//
// 验收门槛极简——minFindings 是 LLM 能力回归底线：本 profile dispatch 后只要本
// engagement 至少多出 minFindings 条 finding 就算 PASS。LLM 输出本就有抖动，
// "挖出 1 条 vs 3 条"不是稳定指标；唯一要捕捉的退化场景是"原本能挖出现在
// 完全挖不到"，minFindings=1 就够覆盖。severity 等级分布不再参与判定。
type profile struct {
	name           string
	defaultSamples string
	minFindings    int
	// credsForHost 接收样本所属 host 返回该 host 的身份列表（profile 自决定身份组）。
	credsForHost func(host string) []credentialEntry
}

// activeProfile 描述一个 active 模式 e2e 验收剧本：自然语言任务简报 + 验收门槛。
//
// 与 passive 的 profile 不同——active 没有 sample 流量文件，直接把 brief 自然
// 语言（含目标 URL/凭据/测试方向）整段 POST /scan/active 喂给 hunter LLM，由
// LLM 自行识别 + 自主扫描。
type activeProfile struct {
	name        string
	brief       string
	minFindings int
}

// activeProfiles 是 active 模式 e2e 验收剧本集，args 用前缀 active:<name> 选择。
//
// 当前只内置 1 个 demo (xss)——用户实际用 active 模式时，按需追加新 profile 即可。
var activeProfiles = map[string]activeProfile{
	"xss": {
		name:        "xss",
		brief:       "测试网站 http://111.229.193.40:34280/login.php，账号 admin/password，要测试 File Upload、XSS、File Inclusion 漏洞",
		minFindings: 3,
	},
}

var profiles = map[string]profile{
	"bac": {
		name:           "bac",
		defaultSamples: "examples/sample_bac_raw.json",
		minFindings:    1,
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
		minFindings:    1,
		// DVWA 远程靶场（111.229.193.40:34280）：仅 admin 身份。
		// PHPSESSID + security=low 双 cookie 拼成一行；旧 gordonb 身份的 cookie 在新靶机上无效，
		// 需要时让用户在远程 DVWA 重新登录拿 cookie 再补回来。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"xss": {
		name:           "xss",
		defaultSamples: "examples/sample_xss_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场，admin 同凭证；3 条样本覆盖 reflected (xss_r) / stored (xss_s) / DOM (xss_d) 三种场景。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"brute": {
		name:           "brute",
		defaultSamples: "examples/sample_brute_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/brute/ 是登录表单类暴力破解漏洞。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"path-traversal": {
		name:           "path-traversal",
		defaultSamples: "examples/sample_path-traversal_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/fi/?page= 是路径遍历 / 任意文件读取漏洞（OWASP CWE-22）。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"unrestricted-upload": {
		name:           "unrestricted-upload",
		defaultSamples: "examples/sample_unrestricted-upload_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/upload/ 是 Unrestricted File Upload（OWASP CWE-434）。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"csrf": {
		name:           "csrf",
		defaultSamples: "examples/sample_csrf_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/csrf/ 是 CSRF（OWASP CWE-352）：
		// 关键特征是 sample 流量本身用 GET 改密码，缺少 anti-CSRF token——主漏洞证据
		// 已写在流量入口里。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"api": {
		name:           "api",
		defaultSamples: "examples/sample_api_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/api/ 是 API 类漏洞入口（具体漏洞类型由 hunter
		// agent 探测：可能是 IDOR / 信息泄露 / 弱认证 / 注入等）。Referer 来自 cryptography
		// 页面意味着这是从其他漏洞链路跳过来的 API 端点。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"cryptography": {
		name:           "cryptography",
		defaultSamples: "examples/sample_cryptography_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/cryptography/ 是密码学相关漏洞类（OWASP CWE-310/327）：
		// 弱加密算法 / 硬编码密钥 / 弱随机数 / IV 复用 / 弱哈希等。具体漏洞由 hunter agent
		// 通过 read_vuln_skill + 读源码 / 多请求差分等手段判定。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"redirect": {
		name:           "redirect",
		defaultSamples: "examples/sample_redirect_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/open_redirect/ 是开放重定向（OWASP CWE-601）：
		// URL 参数控制跳转目标但未做域白名单校验，可被钓鱼利用。hunter agent 通过构造
		// redirect=<外部域> 参数 + 看 Location header 是否原样返回判定。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"authbypass": {
		name:           "authbypass",
		defaultSamples: "examples/sample_authbypass_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/authbypass/ 是认证绕过类（OWASP CWE-287/863）：
		// 鉴权逻辑缺陷可直接越过登录访问受保护资源。hunter agent 通过 anonymous /
		// 修改 cookie / Header 篡改等多手法判定。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"csp": {
		name:           "csp",
		defaultSamples: "examples/sample_csp_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/csp/ 是 Content-Security-Policy 配置问题
		// （OWASP CWE-1021）：过宽 CSP（含 unsafe-inline / unsafe-eval / 通配符 source）
		// 削弱 XSS 防护。hunter agent 通过读 Content-Security-Policy 响应头判定。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
				}},
			}
		},
	},
	"exec": {
		name:           "exec",
		defaultSamples: "examples/sample_exec_raw.json",
		minFindings:    1,
		// 同 DVWA 远程靶场。/vulnerabilities/exec/ 是命令注入（OWASP CWE-77/78）：
		// 用户输入未经 escape 拼到 shell 命令。hunter agent 通过 ;ls / `id` / |whoami
		// 等 payload + 看 stdout 回显判定。
		credsForHost: func(_ string) []credentialEntry {
			return []credentialEntry{
				{Name: "admin", Role: "admin", Credentials: []map[string]string{
					{"type": "headers", "key": "Cookie", "value": "PHPSESSID=668a0c0d068f02c275791bb82ce24ec6; security=low"},
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

	// args 用前缀区分两种模式: "active:xss" → active；其他 → passive。
	passiveSel, activeSel, err := selectProfiles(os.Args[1:])
	if err != nil {
		logger.Fatal().Err(err).Msg("select profiles")
	}
	if len(passiveSel) == 0 && len(activeSel) == 0 {
		logger.Fatal().Msg("无 profile 可跑（passive + active 均空）")
	}

	// 共享 PG pool（passive / active 均用）
	pool, err := db.NewPgPool(ctx, pgDSN, 5, 1, 0, 0)
	if err != nil {
		logger.Fatal().Err(err).Msg("pg")
	}
	defer pool.Close()

	// ---- Passive 流水线 ----
	if len(passiveSel) > 0 {
		proxyHostPort, err := extractHostPort(proxyURL)
		if err != nil {
			logger.Fatal().Err(err).Msg("parse proxy addr")
		}
		plans, err := buildPlans(passiveSel, vulnBase)
		if err != nil {
			logger.Fatal().Err(err).Msg("build plans")
		}
		// 启动期一次预录所有 passive profile（不只是被选的）的全部 host 凭证。
		if err := enrollAllCreds(apiBase, apiKey, vulnBase); err != nil {
			logger.Fatal().Err(err).Msg("enroll all credentials")
		}
		logger.Info().Msg("all credentials enrolled (across every known passive profile)")

		if err := runAllUnified(ctx, plans, proxyHostPort, apiBase, apiKey, pool, logger); err != nil {
			logger.Error().Err(err).Msg("e2e passive FAIL")
			fmt.Printf("✗ e2e passive FAIL: %v\n", err)
			os.Exit(1)
		}
		names := make([]string, len(plans))
		for i, p := range plans {
			names[i] = p.prof.name
		}
		fmt.Printf("✓ e2e passive PASS profile=[%s]\n", strings.Join(names, ","))
	}

	// ---- Active 流水线 ----
	if len(activeSel) > 0 {
		if err := runActiveProfiles(ctx, activeSel, apiBase, apiKey, pool, logger); err != nil {
			logger.Error().Err(err).Msg("e2e active FAIL")
			fmt.Printf("✗ e2e active FAIL: %v\n", err)
			os.Exit(1)
		}
		names := make([]string, len(activeSel))
		for i, p := range activeSel {
			names[i] = p.name
		}
		fmt.Printf("✓ e2e active PASS profile=[%s]\n", strings.Join(names, ","))
	}
}

// selectProfiles 解析 CLI args 拆成 (passive, active) 两组。
//
// args 前缀语义：
//   - "active:<name>" → 选 activeProfiles[name]
//   - 其他            → 选 profiles[name]（passive）
//
// 空 args = 跑全部 passive profile（active 必须显式 `active:xxx` 选，避免无意中
// 触发耗资源的真实站点扫描）。未知 profile 立即报错，避免静默忽略。
func selectProfiles(args []string) ([]profile, []activeProfile, error) {
	if len(args) == 0 {
		all := make([]profile, 0, len(profiles))
		for _, p := range profiles {
			all = append(all, p)
		}
		sort.Slice(all, func(i, j int) bool { return all[i].name < all[j].name })
		return all, nil, nil
	}
	passive := make([]profile, 0, len(args))
	active := make([]activeProfile, 0, len(args))
	seenPassive := map[string]struct{}{}
	seenActive := map[string]struct{}{}
	for _, a := range args {
		raw := strings.ToLower(strings.TrimSpace(a))
		if raw == "" {
			continue
		}
		if strings.HasPrefix(raw, "active:") {
			key := strings.TrimPrefix(raw, "active:")
			ap, ok := activeProfiles[key]
			if !ok {
				known := make([]string, 0, len(activeProfiles))
				for k := range activeProfiles {
					known = append(known, k)
				}
				sort.Strings(known)
				return nil, nil, fmt.Errorf("未知 active profile %q（可选: active:%s）", a, strings.Join(known, " | active:"))
			}
			if _, dup := seenActive[key]; dup {
				continue
			}
			seenActive[key] = struct{}{}
			active = append(active, ap)
			continue
		}
		p, ok := profiles[raw]
		if !ok {
			known := make([]string, 0, len(profiles))
			for k := range profiles {
				known = append(known, k)
			}
			sort.Strings(known)
			return nil, nil, fmt.Errorf("未知 profile %q（可选: %s 或 active:<name>）", a, strings.Join(known, ", "))
		}
		if _, dup := seenPassive[raw]; dup {
			continue
		}
		seenPassive[raw] = struct{}{}
		passive = append(passive, p)
	}
	return passive, active, nil
}

// runActiveProfiles 顺序跑被选的 active profile：调 POST /scan/active → 轮询
// finding 数。与 passive 流水线共用 PG pool / pollDeadline。
//
// 不并发跑——active 任务普遍长（默认 4h），并发既无意义（仍占满 sandbox/LLM 配额）
// 又会让日志难读。多 profile 按选中顺序串行。
func runActiveProfiles(ctx context.Context, profs []activeProfile, apiBase, apiKey string, pool *pgxpool.Pool, logger zerolog.Logger) error {
	store := finding.NewStore(pool)
	agentRunStore := agentrun.NewStore(pool)

	for _, ap := range profs {
		eid, taskID, err := createActiveScan(apiBase, apiKey, ap.brief)
		if err != nil {
			return fmt.Errorf("active profile %s: createActiveScan: %w", ap.name, err)
		}
		logger.Info().
			Str("profile", ap.name).
			Str("engagement_id", eid).
			Str("agent_run_id", taskID).
			Msg("active scan dispatched")

		startedAt := time.Now()
		deadline := time.Now().Add(pollDeadline() + 10*time.Minute)
		observed := false
		var lastFindings []finding.VulnFinding
		var lastTotal, lastUnfinished int
		for time.Now().Before(deadline) {
			runs, runErr := agentRunStore.ListByEngagement(ctx, eid, 100)
			unfinished, totalRuns := 0, 0
			if runErr == nil {
				for _, r := range runs {
					if !r.CreatedAt.After(startedAt) {
						continue
					}
					totalRuns++
					if r.Status == "pending" || r.Status == "running" {
						unfinished++
					}
				}
			}
			all, findErr := store.ListByEngagement(ctx, eid)
			var matched []finding.VulnFinding
			if findErr == nil {
				matched = filterAfter(all, startedAt)
			}
			lastFindings = matched
			lastTotal = len(matched)
			lastUnfinished = unfinished
			if totalRuns > 0 {
				observed = true
			}

			logger.Info().
				Str("profile", ap.name).
				Int("findings", lastTotal).
				Int("min_required", ap.minFindings).
				Int("unfinished_runs", unfinished).
				Int("total_runs", totalRuns).
				Bool("observed", observed).
				Msg("active poll")

			if observed && unfinished == 0 && lastTotal >= ap.minFindings {
				logger.Info().
					Str("profile", ap.name).
					Int("findings", lastTotal).
					Msg("active profile PASS")
				fmt.Printf("✓ active profile=%s PASS: findings=%d (min=%d)\n", ap.name, lastTotal, ap.minFindings)
				for _, f := range lastFindings {
					sum := f.Summary
					if i := strings.IndexByte(sum, '\n'); i >= 0 {
						sum = sum[:i]
					}
					if len(sum) > 100 {
						sum = sum[:100] + "..."
					}
					fmt.Printf("  - [%s] %s\n", f.Severity, sum)
				}
				goto nextProfile
			}
			time.Sleep(pollInterval)
		}

		// 超时未达标
		return fmt.Errorf("active profile %s 超时未 PASS: findings=%d (min=%d), unfinished_runs=%d",
			ap.name, lastTotal, ap.minFindings, lastUnfinished)
	nextProfile:
	}
	return nil
}

// buildPlans 为每个被选 profile 加载样本并解析其 host（仅 e2e 内部用，详见 resolveSampleHost）。
func buildPlans(selected []profile, vulnBase string) ([]profilePlan, error) {
	out := make([]profilePlan, 0, len(selected))
	for _, p := range selected {
		samples, err := loadRawSamples(p.defaultSamples)
		if err != nil {
			return nil, fmt.Errorf("load samples for %s: %w", p.name, err)
		}
		host, err := resolveSampleHost(vulnBase, samples)
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
		host, err := resolveSampleHost(vulnBase, samples)
		if err != nil {
			return fmt.Errorf("resolve host for %s: %w", p.name, err)
		}
		hostCreds[host] = append(hostCreds[host], p.credsForHost(host)...)
	}
	return saveCredsBatch(apiBase, apiKey, hostCreds)
}

// runAllUnified 并发跑所有 plan：一次性 dispatch 全部 profile 的全部 sample，
// 然后统一 poll 等所有 agent_run done + 总 finding 数满足各 profile minFindings 之和。
//
// 与旧串行版本（每 profile 独立 dispatch + 独立 poll）相比：
//   - 总耗时 ≈ max(各 task 时长)，而非 sum(...)
//   - 失去 per-profile PASS 粒度，整体 PASS/FAIL；finding 列表按 severity+summary
//     输出方便人工归属判断
//
// 多 host 场景：每个独特 host 一个 engagement，统计跨所有 engagement 聚合。
// 同 host 多 profile（典型如 DVWA 跑 path+upload+sqli）共享同一 engagement。
//
// 防假阳性两道防线（沿用旧设计）：
//  1. unifiedStartedAt 基线：finding/agent_run 都按 created_at > 基线过滤；
//  2. observedAtLeastOneRun 哨兵：必须先观测到 total_runs > 0，
//     再看 unfinished_runs==0 才允许判 PASS——防 ingestor 异步未落库的假阳性。
func runAllUnified(ctx context.Context, plans []profilePlan, proxyHostPort, apiBase, apiKey string, pool *pgxpool.Pool, logger zerolog.Logger) error {
	// 1. 为每个独特 host 建/复用 engagement
	eidByHost := map[string]string{}
	for _, plan := range plans {
		if _, ok := eidByHost[plan.host]; ok {
			continue
		}
		eid, err := createPassiveScan(apiBase, apiKey, plan.host)
		if err != nil {
			return fmt.Errorf("create engagement for host %s: %w", plan.host, err)
		}
		eidByHost[plan.host] = eid
		logger.Info().Str("host", plan.host).Str("engagement_id", eid).Msg("engagement ready")
	}

	// 2. 统计 sum(minFindings) 与 total sample 数
	totalMinFindings := 0
	totalSamples := 0
	for _, plan := range plans {
		totalMinFindings += plan.prof.minFindings
		totalSamples += len(plan.samples)
	}

	unifiedStartedAt := time.Now()

	// 3. 并发 dispatch 所有 profile 的所有 sample
	logger.Info().
		Int("profiles", len(plans)).
		Int("samples", totalSamples).
		Int("hosts", len(eidByHost)).
		Msg("dispatching all samples concurrently")
	var wg sync.WaitGroup
	var dispatchErr int32
	for _, plan := range plans {
		for i, raw := range plan.samples {
			wg.Add(1)
			go func(profName, r string, idx int) {
				defer wg.Done()
				if err := dispatchRaw(proxyHostPort, r); err != nil {
					atomic.AddInt32(&dispatchErr, 1)
					logger.Error().Str("profile", profName).Int("idx", idx).Err(err).Msg("dispatch failed")
					return
				}
				logger.Info().Str("profile", profName).Int("idx", idx).Msg("raw dispatched")
			}(plan.prof.name, raw, i)
		}
	}
	wg.Wait()
	if atomic.LoadInt32(&dispatchErr) > 0 {
		return fmt.Errorf("dispatch failed: %d/%d", atomic.LoadInt32(&dispatchErr), totalSamples)
	}
	logger.Info().Msg("all samples dispatched; unified poll starting")

	// 4. 统一 poll：等所有 host 的 agent_run done + 总 finding 数满足
	store := finding.NewStore(pool)
	agentRunStore := agentrun.NewStore(pool)
	// 多 profile 并发跑，deadline 给单 profile 上限 + 适度放大兜底大 LLM 抖动
	deadline := time.Now().Add(pollDeadline() + 10*time.Minute)
	observed := false
	var lastTotalFindings, lastUnfinished, lastTotalRuns int
	var lastFindings []finding.VulnFinding
	for time.Now().Before(deadline) {
		totalRuns, unfinished, totalFindings := 0, 0, 0
		var allFindings []finding.VulnFinding
		for _, eid := range eidByHost {
			if runs, runErr := agentRunStore.ListByEngagement(ctx, eid, 100); runErr == nil {
				for _, r := range runs {
					if !r.CreatedAt.After(unifiedStartedAt) {
						continue
					}
					totalRuns++
					if r.Status == "pending" || r.Status == "running" {
						unfinished++
					}
				}
			}
			if all, findErr := store.ListByEngagement(ctx, eid); findErr == nil {
				matched := filterAfter(all, unifiedStartedAt)
				totalFindings += len(matched)
				allFindings = append(allFindings, matched...)
			}
		}
		if totalRuns > 0 {
			observed = true
		}
		lastTotalFindings = totalFindings
		lastUnfinished = unfinished
		lastTotalRuns = totalRuns
		lastFindings = allFindings

		logger.Info().
			Int("findings", totalFindings).
			Int("min_required", totalMinFindings).
			Int("unfinished_runs", unfinished).
			Int("total_runs", totalRuns).
			Bool("observed", observed).
			Msg("unified poll")

		findingsOK := totalFindings >= totalMinFindings
		runsOK := unfinished == 0 && observed
		if findingsOK && runsOK {
			logger.Info().Int("findings", totalFindings).Msg("e2e unified PASS")
			fmt.Printf("✓ unified PASS: findings=%d (min=%d), agent_runs=%d\n", totalFindings, totalMinFindings, totalRuns)
			for _, f := range allFindings {
				sum := f.Summary
				if i := strings.IndexByte(sum, '\n'); i >= 0 {
					sum = sum[:i]
				}
				if len(sum) > 100 {
					sum = sum[:100]
				}
				fmt.Printf("  [%s] %s\n", f.Severity, sum)
			}
			return nil
		}
		time.Sleep(pollInterval)
	}
	// 超时：打印当前状态便于排查
	for _, f := range lastFindings {
		sum := f.Summary
		if i := strings.IndexByte(sum, '\n'); i >= 0 {
			sum = sum[:i]
		}
		if len(sum) > 80 {
			sum = sum[:80]
		}
		fmt.Printf("  [%s] %s\n", f.Severity, sum)
	}
	return fmt.Errorf("unified timeout: findings=%d/%d, unfinished_runs=%d, total_runs=%d", lastTotalFindings, totalMinFindings, lastUnfinished, lastTotalRuns)
}

// resolveSampleHost 决定本 profile 样本流量所属的 host（用于建凭证 / 给 hunter
// task 注入）。engagement 不 per-host，此 host 仅供 e2e 内部建凭证、校验
// finding.host 对得上用。
//
//	优先级：env LIUSHA_E2E_SCOPE_HOST > 首条样本的 Host: 头去端口 > vulnBase URL 的 host
//
// 这样不同 profile 用不同目标（如 SQLi 用本地 DVWA、BAC 用本地 vulnapp）时无需切 LIUSHA_VULNAPP_BASE。
func resolveSampleHost(vulnBase string, samples []string) (string, error) {
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

// filterAfter 把 finding 列表按 created_at > baseline 过滤。
//
// 多 profile 共享同 engagement 时（同 host），engagement 上累计的 finding 包含前
// profile 的战果，直接数会让后续 profile 假阳性 PASS（实测 cryptography 在 api
// 之后跑，poll 第一次就看到 count=1 立即 PASS，但本流量真正的 agent_run 还在
// 创建中——典型语义混淆 bug）。用时间戳基线把范围切到本 profile dispatch 之后。
func filterAfter(all []finding.VulnFinding, baseline time.Time) []finding.VulnFinding {
	out := make([]finding.VulnFinding, 0, len(all))
	for _, f := range all {
		if f.CreatedAt.After(baseline) {
			out = append(out, f)
		}
	}
	return out
}

// countKinds：按 severity 分组——仅用于 poll log 信息展示，不参与 PASS 判定
// （以前 profile.minKinds 是判定字段，简化后已废弃；这里保留是因为肉眼看 log
// 知道"挖出的 finding 都是什么 severity"对调试有用，比纯 count 信息量大）。
func countKinds(fs []finding.VulnFinding) map[string]int {
	out := make(map[string]int, len(fs))
	for _, f := range fs {
		out[f.Severity]++
	}
	return out
}
