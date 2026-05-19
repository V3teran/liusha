// Package main 是 liusha 端到端验收触发器，覆盖 passive + active 两种模式。
//
// CLI 用法（args 用前缀区分模式）：
//
//	go run ./cmd/e2e                              # 不加参数 = 跑所有 passive profile
//	go run ./cmd/e2e bac                          # 只跑 passive bac（业务向访问控制）
//	go run ./cmd/e2e sqli                         # 只跑 passive sqli
//	go run ./cmd/e2e xss                          # 只跑 passive xss（reflected/stored/DOM）
//	go run ./cmd/e2e bac sqli xss                 # passive 多选
//	go run ./cmd/e2e active:full                  # 只跑 active full（开放性 brief 压测 LLM 自主 recon + swarm）
//	go run ./cmd/e2e sqli active:full             # 混合：passive sqli + active full
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
// 内置 active profile（1 个）：
//   - active:full        ：远程 DVWA login.php → 开放 brief"挖出尽可能多的漏洞"压测 swarm + 自主 recon
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
	"strconv"
	"strings"
	"time"

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



func main() {
	logger := logx.New("e2e")
	ctx := context.Background()

	apiBase := envOr("LIUSHA_API_BASE", "http://localhost:8080")
	apiKey := envOr("LIUSHA_API_KEY", "changeme-dev-key")
	pgDSN := envOr("LIUSHA_POSTGRES_DSN", "postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable")
	proxyURL := envOr("LIUSHA_PROXY_ADDR", "http://localhost:8888")
	vulnBase := envOr("LIUSHA_VULNAPP_BASE", "http://111.229.193.40:38001")

	// args 用前缀区分两种模式: "active:full" → active；其他 → passive。
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
