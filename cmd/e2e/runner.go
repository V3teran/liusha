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
	"github.com/V3teran/liusha/internal/task"
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
			runs, runErr := agentRunStore.ListByTask(ctx, eid, 100)
			unfinished, totalRuns := 0, 0
			if runErr != nil {
				// 之前 silent swallow：导致 e2e 看不到 orchestrator 但不知为何。必须 log 出来。
				logger.Warn().Err(runErr).Str("eid", eid).Msg("ListByTask(hunter) failed — totalRuns 强制 0 是误报")
			} else {
				// active 每次都新建 session——eid 已唯一定位本次 run 全集（orchestrator + spawn 的 exploitations）。
				// 不再用 startedAt 时间窗过滤 agent_run：dispatched 返回前 server 端 PG now()
				// 已先于 Go time.Now() 触发，orchestrator run.CreatedAt < startedAt → After() = false
				// → orchestrator 被误滤 → total_runs=0 → observed 永远 false → e2e 超时不 PASS。
				for _, r := range runs {
					totalRuns++
					if r.Status == "pending" || r.Status == "running" {
						unfinished++
					}
				}
			}
			all, findErr := store.ListByTask(ctx, eid)
			var matched []finding.VulnFinding
			if findErr != nil {
				logger.Warn().Err(findErr).Str("eid", eid).Msg("ListByTask(finding) failed — findings 强制 0 是误报")
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

// discoverPassiveTasks 列最近的 passive task，筛出 target_host ∈ hosts 且 created_at > baseline 的。
// 聚合器为目标 host 新建的 task 即由此被 e2e 发现（同 host 多批 → 多 task 全收）。
// limit 取 512 足够覆盖 e2e 场景（单次跑至多十几个 host × 数批）。
func discoverPassiveTasks(ctx context.Context, ts *task.Store, hosts map[string]struct{}, baseline time.Time) ([]string, error) {
	tasks, err := ts.List(ctx, task.ModePassive, 512)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, t := range tasks {
		if !t.CreatedAt.After(baseline) {
			continue
		}
		if _, ok := hosts[t.TargetHost]; ok {
			ids = append(ids, t.ID)
		}
	}
	return ids, nil
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
// v2 流量驱动模型（合表后）：passive task 不再预建，而是流量经代理落 proxy_traffic 后，
// ingestor 聚合器按 host 攒批（aggregate_batch_size 条 / window 秒）自动建 passive task。
// 故 e2e 先打流量、再按 host 从 task store 发现聚合器新建的 passive task，按 task_id 聚合轮询。
// 注：e2e 环境应把 aggregate_batch_size 调低（≤ 单 host 样本数），否则小样本攒不满一批不触发。
//
// 防假阳性两道防线（沿用旧设计）：
//  1. unifiedStartedAt 基线：task/finding/agent_run 都按 created_at > 基线过滤；
//  2. observedAtLeastOneRun 哨兵：必须先发现 task 且观测到 total_runs > 0，
//     再看 unfinished_runs==0 才允许判 PASS——防 ingestor 异步未落库的假阳性。
func runAllUnified(ctx context.Context, plans []profilePlan, proxyHostPort, apiBase, apiKey string, pool *pgxpool.Pool, logger zerolog.Logger) error {
	// 1. 统计 sum(minFindings)、total sample 数、去重后的目标 host 集合
	totalMinFindings := 0
	totalSamples := 0
	hosts := map[string]struct{}{}
	for _, plan := range plans {
		totalMinFindings += plan.prof.minFindings
		totalSamples += len(plan.samples)
		hosts[plan.host] = struct{}{}
	}

	unifiedStartedAt := time.Now()

	// 2. 并发 dispatch 所有 profile 的所有 sample（流量经代理 → proxy_traffic → 聚合器建 task）
	logger.Info().
		Int("profiles", len(plans)).
		Int("samples", totalSamples).
		Int("hosts", len(hosts)).
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
	logger.Info().Msg("all samples dispatched; 等待聚合器建 passive task + unified poll starting")

	// 3. 统一 poll：发现聚合器为各 host 新建的 passive task → 聚合其 agent_run done + finding 数
	store := finding.NewStore(pool)
	agentRunStore := hunter.NewStore(pool)
	taskStore := task.NewStore(pool)
	// 多 profile 并发跑，deadline 给单 profile 上限 + 适度放大兜底大 LLM 抖动
	deadline := time.Now().Add(pollDeadline() + 10*time.Minute)
	observed := false
	var lastTotalFindings, lastUnfinished, lastTotalRuns int
	var lastFindings []finding.VulnFinding
	for time.Now().Before(deadline) {
		// 发现基线后聚合器为目标 host 新建的 passive task（同 host 可多批 → 多 task，全收）。
		taskIDs, discErr := discoverPassiveTasks(ctx, taskStore, hosts, unifiedStartedAt)
		if discErr != nil {
			logger.Warn().Err(discErr).Msg("发现 passive task 失败 — 本轮按 0 task 计（等下轮重试）")
		}

		totalRuns, unfinished, totalFindings := 0, 0, 0
		var allFindings []finding.VulnFinding
		for _, tid := range taskIDs {
			if runs, runErr := agentRunStore.ListByTask(ctx, tid, 100); runErr == nil {
				for _, r := range runs {
					totalRuns++
					if r.Status == "pending" || r.Status == "running" {
						unfinished++
					}
				}
			}
			if all, findErr := store.ListByTask(ctx, tid); findErr == nil {
				totalFindings += len(all)
				allFindings = append(allFindings, all...)
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
			Int("passive_tasks", len(taskIDs)).
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

// resolveSampleHost 决定本 profile 样本流量所属的 host（用于建凭证 + 发现聚合器
// 为该 host 新建的 passive task）。此 host 供 e2e 内部建凭证、按 target_host 匹配
// 聚合器生成的 task、以及校验 finding.host 对得上用。
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
