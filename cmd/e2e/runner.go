package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/hunter"
)

// profilePlan 是单个 profile 的运行计划：解析后的 host + 加载好的样本。
type profilePlan struct {
	prof      profile
	host      string
	samples   []string
	samplePth string
}

// runActiveProfiles 顺序跑被选的 active profile：调 POST /scan/active → 轮询
// finding 数。与 passive 流水线共用 PG pool / pollDeadline。
//
// 不并发跑——active 任务普遍长（默认 4h），并发既无意义（仍占满 sandbox/LLM 配额）
// 又会让日志难读。多 profile 按选中顺序串行。
func runActiveProfiles(ctx context.Context, profs []activeProfile, apiBase, apiKey string, pool *pgxpool.Pool, logger zerolog.Logger) error {
	store := finding.NewStore(pool)
	agentRunStore := hunter.NewStore(pool)

	for _, ap := range profs {
		// active:adhoc 是占位 profile——brief 在源码中为空，强制从 LIUSHA_E2E_BRIEF 环境
		// 变量读取（避免把含密码的 brief 写进 git）。LIUSHA_E2E_MIN_FINDINGS 可覆盖门槛。
		if ap.name == "adhoc" {
			brief := strings.TrimSpace(os.Getenv("LIUSHA_E2E_BRIEF"))
			if brief == "" {
				return fmt.Errorf("active:adhoc 需要 LIUSHA_E2E_BRIEF 环境变量提供 brief（含目标 URL/凭证/扫描方向）")
			}
			ap.brief = brief
			if mfStr := strings.TrimSpace(os.Getenv("LIUSHA_E2E_MIN_FINDINGS")); mfStr != "" {
				var mf int
				if _, err := fmt.Sscanf(mfStr, "%d", &mf); err == nil && mf > 0 {
					ap.minFindings = mf
				}
			}
			logger.Info().
				Str("profile", ap.name).
				Int("brief_len", len(ap.brief)).
				Int("min_findings", ap.minFindings).
				Msg("adhoc brief 从 env 注入（不入仓库）")
		}

		eid, hunterID, err := createActiveScan(apiBase, apiKey, ap.brief)
		if err != nil {
			return fmt.Errorf("active profile %s: createActiveScan: %w", ap.name, err)
		}
		logger.Info().
			Str("profile", ap.name).
			Str("owner_id", eid).
			Str("hunter_id", hunterID).
			Msg("active scan dispatched")

		startedAt := time.Now()
		deadline := time.Now().Add(pollDeadline() + 10*time.Minute)
		observed := false
		var lastFindings []finding.VulnFinding
		var lastTotal, lastUnfinished int
		for time.Now().Before(deadline) {
			runs, runErr := agentRunStore.ListByOwner(ctx, "active_scan", eid, 100)
			unfinished, totalRuns := 0, 0
			if runErr != nil {
				// 之前 silent swallow：导致 e2e 看不到 commander 但不知为何。必须 log 出来。
				logger.Warn().Err(runErr).Str("eid", eid).Msg("ListByOwner(hunter) failed — totalRuns 强制 0 是误报")
			} else {
				// active 每次都新建 session——eid 已唯一定位本次 run 全集（commander + spawn 的 strikers）。
				// 不再用 startedAt 时间窗过滤 agent_run：dispatched 返回前 server 端 PG now()
				// 已先于 Go time.Now() 触发，commander run.CreatedAt < startedAt → After() = false
				// → commander 被误滤 → total_runs=0 → observed 永远 false → e2e 超时不 PASS。
				for _, r := range runs {
					totalRuns++
					if r.Status == "pending" || r.Status == "running" {
						unfinished++
					}
				}
			}
			all, findErr := store.ListByOwner(ctx, "active_scan", eid)
			var matched []finding.VulnFinding
			if findErr != nil {
				logger.Warn().Err(findErr).Str("eid", eid).Msg("ListByOwner(finding) failed — findings 强制 0 是误报")
			} else {
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
// 多 host 场景：每个独特 host 一个 owner (passive_session)，统计跨所有 owner 聚合。
// 同 host 多 profile（典型如 DVWA 跑 path+upload+sqli）共享同一 owner。
//
// 防假阳性两道防线（沿用旧设计）：
//  1. unifiedStartedAt 基线：finding/agent_run 都按 created_at > 基线过滤；
//  2. observedAtLeastOneRun 哨兵：必须先观测到 total_runs > 0，
//     再看 unfinished_runs==0 才允许判 PASS——防 ingestor 异步未落库的假阳性。
func runAllUnified(ctx context.Context, plans []profilePlan, proxyHostPort, apiBase, apiKey string, pool *pgxpool.Pool, logger zerolog.Logger) error {
	// 1. 为每个独特 host 建/复用 session
	eidByHost := map[string]string{}
	for _, plan := range plans {
		if _, ok := eidByHost[plan.host]; ok {
			continue
		}
		eid, err := createPassiveScan(apiBase, apiKey, plan.host)
		if err != nil {
			return fmt.Errorf("create session for host %s: %w", plan.host, err)
		}
		eidByHost[plan.host] = eid
		logger.Info().Str("host", plan.host).Str("owner_id", eid).Msg("session ready")
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
	agentRunStore := hunter.NewStore(pool)
	// 多 profile 并发跑，deadline 给单 profile 上限 + 适度放大兜底大 LLM 抖动
	deadline := time.Now().Add(pollDeadline() + 10*time.Minute)
	observed := false
	var lastTotalFindings, lastUnfinished, lastTotalRuns int
	var lastFindings []finding.VulnFinding
	for time.Now().Before(deadline) {
		totalRuns, unfinished, totalFindings := 0, 0, 0
		var allFindings []finding.VulnFinding
		for _, eid := range eidByHost {
			// passive 模式：hunter / finding 的 owner_type 是 passive_session（v1.1 per-host 流量驱动模型）。
			// 历史 active_scan 字面量是 v1.1 重构遗漏——passive runner 一定要查 passive_session。
			if runs, runErr := agentRunStore.ListByOwner(ctx, "passive_session", eid, 100); runErr == nil {
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
			if all, findErr := store.ListByOwner(ctx, "passive_session", eid); findErr == nil {
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
// task 注入）。owner (passive_session) 不 per-host，此 host 仅供 e2e 内部建凭证、校验
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
