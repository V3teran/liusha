// Package main 是 TSecBench（腾讯安全基准）跑分适配驱动：把 TSecBench 的标准跑分循环
// 翻译成 liusha 的 task 语义，驱动本机已有的 liusha 栈逐题解题、提交 flag。
//
// 本阶段只支持【本地 Agent 评测】模式（Tsecbench v1 练手），走 liusha HTTP API，不碰
// liusha 业务代码。托管上榜（v2）是下一阶段。
//
// 用法（全部配置经环境变量；连好靶场 VPN 后运行）：
//
//	BENCHMARK_BASE_URL=... BENCHMARK_TOKEN=... \
//	LIUSHA_ADMIN_INITIAL_PASSWORD=... \
//	LIUSHA_TSB_ONLY=web_01,web_02 \   # 可选：只跑这些题（逗号分隔 unique_code）
//	go run ./cmd/tsecbench
//
// 流程：VPN 预检 → 登录 liusha → 列题 → 按难度升序逐题（并发≤3）：start 靶场 → 建
// liusha task → 轮询产出提取并提交 flag → terminate+close → 汇总得分。
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog"
)

// defaultProbeURL 是 VPN 联通预检地址（只在靶场 VPN 内可达）。
const defaultProbeURL = "http://10.0.100.58"

// config 是适配器运行配置，全部来自环境变量。
type config struct {
	benchBase    string
	benchToken   string
	probeURL     string
	liushaBase   string
	liushaUser   string
	liushaPass   string
	flagPattern  string
	concurrency  int
	pollInterval time.Duration
	// 单题轮询上限按题面分值分档：分值是平台自己的工作量估计，难度标签不可靠
	// （1200 分的题被标为 medium）。实测解出用时随分值单调上升。
	deadlineStandard time.Duration // 低分档（≤200）
	deadlineExtended time.Duration // 中分档（201-600）
	deadlineDeep     time.Duration // 高分档（>600）
	idleWindow       time.Duration
	only             []string // 只跑这些题（unique_code）；空 = 全部未通关题
}

// difficultyRank 把难度映射为排序权重（easy 先做，先拿能拿的分）；未知难度最后。
func difficultyRank(d string) int {
	switch d {
	case "easy":
		return 0
	case "medium":
		return 1
	case "hard":
		return 2
	default:
		return 3
	}
}

// selectPending 过滤出未通关题，按（难度升序、level 升序、unique_code）排序。纯函数便于测试。
func selectPending(chs []challenge) []challenge {
	var out []challenge
	for _, c := range chs {
		if !c.IsCompleted {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := difficultyRank(out[i].Difficulty), difficultyRank(out[j].Difficulty)
		if ri != rj {
			return ri < rj
		}
		if out[i].Level != out[j].Level {
			return out[i].Level < out[j].Level
		}
		return out[i].UniqueCode < out[j].UniqueCode
	})
	return out
}

func main() {
	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).
		With().Timestamp().Logger()

	cfg, err := loadConfig()
	if err != nil {
		logger.Fatal().Err(err).Msg("配置错误")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg, logger); err != nil {
		logger.Fatal().Err(err).Msg("跑分终止")
	}
}

// loadConfig 从环境变量读取配置并校验必填项。
func loadConfig() (config, error) {
	cfg := config{
		benchBase:   os.Getenv("BENCHMARK_BASE_URL"),
		benchToken:  os.Getenv("BENCHMARK_TOKEN"),
		probeURL:    envOr("LIUSHA_TSB_PROBE_URL", defaultProbeURL),
		liushaBase:  envOr("LIUSHA_TSB_API_BASE", "http://127.0.0.1:8580"),
		liushaUser:  envOr("LIUSHA_TSB_USER", "admin"),
		liushaPass:  envOr("LIUSHA_TSB_PASS", os.Getenv("LIUSHA_ADMIN_INITIAL_PASSWORD")),
		flagPattern: os.Getenv("LIUSHA_TSB_FLAG_RE"),
		concurrency: envIntOr("LIUSHA_TSB_CONCURRENCY", 3),
		only:        splitList(os.Getenv("LIUSHA_TSB_ONLY")),
	}
	// 时长在任何外部操作之前校验：非正或非数字直接报错（时长非正会让轮询 panic），不悄悄回退缺省。
	for _, d := range []struct {
		dst      *time.Duration
		key      string
		fallback int
	}{
		{&cfg.pollInterval, "LIUSHA_TSB_POLL_SECONDS", 20},
		{&cfg.deadlineStandard, "LIUSHA_TSB_DEADLINE_STANDARD_SECONDS", 1800},
		{&cfg.deadlineExtended, "LIUSHA_TSB_DEADLINE_EXTENDED_SECONDS", 2700},
		{&cfg.deadlineDeep, "LIUSHA_TSB_DEADLINE_DEEP_SECONDS", 5400},
		{&cfg.idleWindow, "LIUSHA_TSB_IDLE_SECONDS", 300},
	} {
		n, err := envPositiveInt(d.key, d.fallback)
		if err != nil {
			return cfg, err
		}
		*d.dst = time.Duration(n) * time.Second
	}
	if cfg.benchBase == "" {
		return cfg, fmt.Errorf("未设置 BENCHMARK_BASE_URL（TSecBench 创建跑分任务后下发）")
	}
	if cfg.benchToken == "" {
		return cfg, fmt.Errorf("未设置 BENCHMARK_TOKEN（TSecBench 创建跑分任务后下发）")
	}
	if cfg.liushaPass == "" {
		return cfg, fmt.Errorf("未设置 liusha 登录密码（LIUSHA_TSB_PASS 或 LIUSHA_ADMIN_INITIAL_PASSWORD）")
	}
	if cfg.concurrency < 1 {
		cfg.concurrency = 1
	}
	if cfg.concurrency > 3 {
		cfg.concurrency = 3 // 平台硬上限：同时最多 3 道活跃题
	}
	return cfg, nil
}

// run 执行一次完整跑分。
func run(ctx context.Context, cfg config, logger zerolog.Logger) error {
	// 1. VPN 联通预检（强制前置，不通立即中断）。
	precheckClient := &http.Client{Timeout: 15 * time.Second}
	if err := vpnPrecheck(ctx, precheckClient, cfg.probeURL); err != nil {
		return fmt.Errorf("VPN 检测未通过，请检查靶场 VPN 网络配置: %w", err)
	}
	logger.Info().Str("probe", cfg.probeURL).Msg("✓ VPN 联通预检通过")

	// 2. 登录 liusha。
	lc, err := loginLiusha(ctx, cfg.liushaBase, cfg.liushaUser, cfg.liushaPass, nil)
	if err != nil {
		return fmt.Errorf("登录 liusha: %w", err)
	}
	logger.Info().Str("base", cfg.liushaBase).Msg("✓ 已登录 liusha")

	extractor, err := newFlagExtractor(cfg.flagPattern)
	if err != nil {
		return err
	}
	bc := newBenchClient(cfg.benchBase, cfg.benchToken, nil)

	// 3. 列题。
	chs, err := bc.listChallenges(ctx)
	if err != nil {
		return fmt.Errorf("列题: %w", err)
	}
	pending, missing := filterOnly(selectPending(chs), cfg.only)
	if len(missing) > 0 {
		logger.Warn().Strs("unique_code", missing).Msg("LIUSHA_TSB_ONLY 指定的题目不存在或已通关，忽略")
	}
	logger.Info().Int("total", len(chs)).Int("pending", len(pending)).Msg("题目列表已获取")
	if len(pending) == 0 {
		logger.Info().Msg("没有待解题目，收工")
		return nil
	}

	// 4. 释放上一轮（崩溃 / 中断）遗留的活跃靶场：它们占着平台的 3 个活跃名额，
	// 不释放则后面的 start 全部因活跃满失败。close 幂等，求解时 start 会重新拉起。
	releaseLeftovers(ctx, bc, logger, chs)

	// 5. 并发（≤concurrency）逐题求解。致命错误（token 失效 / 任务结束）取消全局。
	// 通关任务交给收敛观察：规划者要做一轮结果分析并停止探索（分钟级），到点未收敛
	// 由观察者兜底终止，所以进程收尾前要等观察协程全部落地。
	s := &solver{
		bench: bc, liusha: lc, extractor: extractor, logger: logger,
		pollInterval: cfg.pollInterval, idleWindow: cfg.idleWindow,
		deadlineStandard: cfg.deadlineStandard, deadlineExtended: cfg.deadlineExtended, deadlineDeep: cfg.deadlineDeep,
		convergeWait: 6 * time.Minute,
		convergePoll: 15 * time.Second,
	}
	results := schedule(ctx, s, cfg.concurrency, pending)
	logger.Info().Msg("等待通关任务的收敛观察收尾")
	s.watchWG.Wait()
	printSummary(logger, len(chs), results)
	return nil
}

// releaseLeftovers 关闭容器处于 pending / available 的题目。失败只记日志：遗留名额
// 不足 3 时仍能跑，真满了由 start 报错跳题。
func releaseLeftovers(ctx context.Context, bc *benchClient, logger zerolog.Logger, chs []challenge) {
	for _, c := range chs {
		if c.ContainerStatus != "pending" && c.ContainerStatus != "available" {
			continue
		}
		if _, err := bc.close(ctx, c.UniqueCode); err != nil {
			logger.Warn().Err(err).Str("challenge", c.UniqueCode).Msg("释放遗留靶场失败")
			continue
		}
		logger.Info().Str("challenge", c.UniqueCode).Msg("已释放遗留靶场")
	}
}

// schedule 以并发窗口逐题求解，返回各题结果。遇致命错误取消全局。
func schedule(ctx context.Context, s *solver, concurrency int, pending []challenge) []challengeResult {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make([]challengeResult, 0, len(pending))

	for _, ch := range pending {
		select {
		case <-ctx.Done():
			// 全局已取消（致命错误或信号），不再派发新题。
			wg.Wait()
			return results
		case sem <- struct{}{}:
		}
		// 致命错误的题先 cancel 再释放名额：两路同时就绪时 select 随机选，可能抢到名额，
		// 这里复查一次，全局已取消就不再派发。
		if ctx.Err() != nil {
			<-sem
			wg.Wait()
			return results
		}
		wg.Add(1)
		go func(ch challenge) {
			defer wg.Done()
			defer func() { <-sem }()
			res, err := s.solve(ctx, ch)
			mu.Lock()
			results = append(results, res)
			mu.Unlock()
			if err != nil {
				var be *benchError
				if errors.As(err, &be) && (be.tokenInvalid() || be.taskEnded()) {
					s.logger.Error().Err(be).Msg("致命：跑分任务结束 / token 失效，停止全局")
					cancel()
					return
				}
				s.logger.Warn().Err(err).Str("challenge", ch.UniqueCode).Msg("本题求解出错，跳过")
			}
		}(ch)
	}
	wg.Wait()
	return results
}

// printSummary 输出跑分总览。
func printSummary(logger zerolog.Logger, totalChallenges int, results []challengeResult) {
	solved, totalScore := 0, 0
	for _, r := range results {
		if r.Completed {
			solved++
		}
		totalScore += r.CumulativeScore
	}
	logger.Info().
		Int("challenges_total", totalChallenges).
		Int("attempted", len(results)).
		Int("solved", solved).
		Int("score", totalScore).
		Msg("==== 跑分结束 ====")
	fmt.Printf("跑分结束：尝试 %d 题，通关 %d 题，累计得分 %d\n", len(results), solved, totalScore)
}

// filterOnly 只保留 only 指定的题目（保持 pending 原有顺序）；only 为空时原样返回。
// missing 是指定了但不在 pending 中的题（不存在或已通关），供调用方提示。
func filterOnly(pending []challenge, only []string) (kept []challenge, missing []string) {
	if len(only) == 0 {
		return pending, nil
	}
	want := make(map[string]bool, len(only))
	for _, code := range only {
		want[code] = true
	}
	found := make(map[string]bool, len(only))
	for _, c := range pending {
		if want[c.UniqueCode] {
			kept = append(kept, c)
			found[c.UniqueCode] = true
		}
	}
	for _, code := range only {
		if !found[code] {
			missing = append(missing, code)
		}
	}
	return kept, missing
}

// ===== 小工具 =====

// splitList 把逗号分隔的列表拆成去空白、去空项的切片。
func splitList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// envPositiveInt 读取正整数环境变量；未设置取 fallback，设置了但不是正整数报错。
func envPositiveInt(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s 须为正整数，当前为 %q", key, v)
	}
	return n, nil
}
