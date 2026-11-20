package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// challengeResult 是单题求解结果。
type challengeResult struct {
	UniqueCode      string
	Completed       bool // 全部 flag 已正确提交
	CorrectFlags    int
	TotalFlags      int
	CumulativeScore int
}

// solver 驱动单题求解：start 靶场 → 建 liusha task → 轮询产出提取并提交 flag → terminate/close。
type solver struct {
	bench     *benchClient
	liusha    *liushaClient
	extractor *flagExtractor
	logger    zerolog.Logger

	pollInterval time.Duration // 轮询 liusha 产出的间隔
	taskDeadline time.Duration // 单题 liusha task 轮询上限（0 = 不限）
	idleWindow   time.Duration // LLM/工具活动停摆多久判定该题跑不动，提前收手
}

// startAttempts 是启动靶场的尝试次数上限：平台把 503 / 500 定义为网络波动、可重试，
// 一次失败就跳题会白白丢掉整道题；持续失败则放弃本题，不无限占用并发名额。
const startAttempts = 5

// buildGoal 构造交给 liusha 的任务目标。只给靶场地址、题面与 flag 数量，不注入任何
// 题解或历史答案——闭卷合规由此处保证。题型不限于 Web，目标不写题型。flag 的回报与
// 坐实由 ctf 场景说明：执行者把每个 flag 原样写进一条待验证假设观察，经复现门坐实成
// result；适配器只取坐实的 result 提交。
//
// 授权范围只能是平台给出的地址（调用方保证非空）：VPN 网段里还有其他题的靶场，目标须
// 写明测试范围。
//
// 结果必须是单行：POST /api/tasks 的契约是「每行一个任务」，多行会把一道题拆成多个任务。
// 题目说明自带的换行同样折叠为空格。
func buildGoal(ch challenge, addrs []string) string {
	parts := []string{
		"在以下授权 CTF 靶场中解题，找到并捕获该题的 flag。",
		"靶场入口地址（VPN 内直连）：" + strings.Join(addrs, "、") + "，测试范围仅限上述地址。",
	}
	if d := strings.TrimSpace(ch.Description); d != "" {
		parts = append(parts, "题目说明："+d)
	}
	// 数量是收敛依据，平台给了就写（含 1）：坐实数到齐即收敛，普通题（1 真 + 若干诱饵）
	// 拿到真 flag 就收——诱饵被证伪不计数。平台未给（0/未知）才不写，收敛交给判断。
	if ch.FlagCount >= 1 {
		parts = append(parts, fmt.Sprintf("本题共有 %d 个 flag，全部捕获后即完成。", ch.FlagCount))
	}
	// flag 的回报与坐实方式（hypothesis、原样含 flag、诱饵排除）由 ctf 场景说明，不写进目标。
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

// solve 求解一道题。返回的 error 若为 benchError 且 taskEnded()/tokenInvalid() 为真，
// 表示整个跑分任务应停止（由调用方判定并取消全局）。
func (s *solver) solve(ctx context.Context, ch challenge) (challengeResult, error) {
	res := challengeResult{UniqueCode: ch.UniqueCode, TotalFlags: ch.FlagCount}
	log := s.logger.With().Str("challenge", ch.UniqueCode).Logger()

	// 题目结束后务必 close，释放活跃名额（幂等）。启动失败也要 close：容器可能已在拉起。
	defer func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := s.bench.close(cctx, ch.UniqueCode); err != nil {
			log.Warn().Err(err).Msg("close 靶场失败")
		}
	}()

	// 1. 启动靶场容器（幂等：已启动返回现有地址）。
	start, err := s.startChallenge(ctx, &log, ch.UniqueCode)
	if err != nil {
		return res, fmt.Errorf("start %s: %w", ch.UniqueCode, err)
	}
	if len(start.ContainerAddr) == 0 {
		return res, fmt.Errorf("start %s 未返回靶场地址，不下发任务", ch.UniqueCode)
	}
	log.Info().Strs("addr", start.ContainerAddr).Msg("靶场已启动")

	// 2. 建 liusha task。
	goal := buildGoal(ch, start.ContainerAddr)
	taskID, err := s.liusha.createTask(ctx, "TSecBench "+ch.UniqueCode, "TSecBench 自动跑分", goal)
	if err != nil {
		return res, fmt.Errorf("建 liusha 任务: %w", err)
	}
	log.Info().Str("task_id", taskID).Msg("liusha 任务已创建")

	// 任务结束后优雅终止 liusha task（不再烧 token）。
	defer func() {
		tctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.liusha.control(tctx, taskID, "terminate"); err != nil &&
			!strings.Contains(err.Error(), "HTTP 409") { // 409 = 已是终态，正常
			log.Warn().Err(err).Msg("terminate liusha 任务失败")
		}
	}()

	// 3. 轮询产出、提取并提交 flag。
	return s.harvest(ctx, &log, ch, taskID, res)
}

// startChallenge 启动靶场，可重试错误（503 / 500）按轮询间隔重试，至多 startAttempts 次；
// 其余错误（活跃满、题目不存在、任务结束）原样返回，由调用方分类处置。
func (s *solver) startChallenge(ctx context.Context, log *zerolog.Logger, uniqueCode string) (startResp, error) {
	for attempt := 1; ; attempt++ {
		start, err := s.bench.start(ctx, uniqueCode)
		var be *benchError
		if err == nil || !errors.As(err, &be) || !be.retriable() || attempt == startAttempts {
			return start, err
		}
		log.Warn().Err(be).Int("attempt", attempt).Msg("启动靶场遇可重试错误，稍后重试")
		select {
		case <-ctx.Done():
			return start, ctx.Err()
		case <-time.After(s.pollInterval):
		}
	}
}

// harvest 周期性拉 liusha 产出、提取 flag 候选并提交，直到通关 / task 结束 / 超时 / 停摆。
func (s *solver) harvest(ctx context.Context, log *zerolog.Logger, ch challenge, taskID string, res challengeResult) (challengeResult, error) {
	var deadline time.Time
	if s.taskDeadline > 0 {
		deadline = time.Now().Add(s.taskDeadline)
	}
	submitted := make(map[string]bool) // 已成功/重复提交的 flag，避免重复请求
	var lastActivity int
	activityChangedAt := time.Now()

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	for {
		if done, fatal := s.submitRound(ctx, log, ch.UniqueCode, taskID, submitted, &res); done || fatal != nil {
			return res, fatal
		}

		// 任务已结束但还没拿全 flag：本轮提取在查状态之前，期间写下的产出会漏掉，
		// 所以看到终态后再提取一轮才收手。
		st, serr := s.liusha.status(ctx, taskID)
		if serr == nil && st.Status != "active" {
			if done, fatal := s.submitRound(ctx, log, ch.UniqueCode, taskID, submitted, &res); done || fatal != nil {
				return res, fatal
			}
			log.Info().Str("status", st.Status).Int("correct", res.CorrectFlags).
				Int("total", res.TotalFlags).Msg("liusha 任务已结束，停止本题")
			return res, nil
		}

		// 活动停摆判定：LLM/工具调用长时间不增长，说明该题跑不动，提前收手换题。
		if n, uerr := s.liusha.usage(ctx, taskID); uerr == nil {
			if n != lastActivity {
				lastActivity, activityChangedAt = n, time.Now()
			} else if s.idleWindow > 0 && time.Since(activityChangedAt) >= s.idleWindow {
				log.Info().Msg("liusha 任务长时间无活动，提前收手换题")
				return res, nil
			}
		}

		if !deadline.IsZero() && time.Now().After(deadline) {
			log.Info().Dur("deadline", s.taskDeadline).Msg("单题轮询超时，换题")
			return res, nil
		}

		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-ticker.C:
		}
	}
}

// submitRound 拉一轮产出并提交其中尚未提交的 flag 候选。返回 (本题是否通关, 需全局停止的致命错误)。
func (s *solver) submitRound(ctx context.Context, log *zerolog.Logger, uniqueCode, taskID string, submitted map[string]bool, res *challengeResult) (bool, error) {
	candidates, err := s.collectCandidates(ctx, log, taskID)
	if err != nil {
		log.Warn().Err(err).Msg("拉取 liusha 产出失败，继续轮询")
	}
	for _, cand := range candidates {
		if submitted[cand] {
			continue
		}
		done, fatal := s.trySubmit(ctx, log, uniqueCode, cand, submitted, res)
		if fatal != nil {
			return false, fatal
		}
		if done {
			res.Completed = true
			log.Info().Int("score", res.CumulativeScore).Msg("✓ 本题通关")
			return true, nil
		}
	}
	return false, nil
}

// collectCandidates 从探索图 result 节点的主张陈述提取去重 flag 候选。flag 经复现门坐实成
// result 才算数：诱饵与编造已被门排除，这里只做提取。只读陈述：复现门坐实的是陈述，
// 正文里的影响、建议是执行者的附言，可能提到见过的诱饵。
func (s *solver) collectCandidates(ctx context.Context, log *zerolog.Logger, taskID string) ([]string, error) {
	nodes, err := s.liusha.graphNodes(ctx, taskID)
	var texts []string
	for _, n := range nodes {
		if n.Kind != "result" {
			continue
		}
		st, serr := resultStatement(n.Content)
		if serr != nil {
			log.Warn().Err(serr).Msg("result 正文无法解析，跳过该节点")
			continue
		}
		texts = append(texts, st)
	}
	return s.extractor.scan(texts...), err
}

// resultStatement 取 result 正文里的主张陈述（解码后的字符串）。必须按 JSON 解码：直接扫
// 原文会拿到转义形式（flag{a\u0026b}、带反斜杠转义），提交的就不是 flag 原文。
func resultStatement(content json.RawMessage) (string, error) {
	var c struct {
		Statement string `json:"statement"`
	}
	if err := json.Unmarshal(content, &c); err != nil {
		return "", fmt.Errorf("解析 result 正文: %w", err)
	}
	return c.Statement, nil
}

// trySubmit 提交单个候选 flag，更新已提交集合与得分。返回 (该题是否通关, 需全局停止的致命错误)。
func (s *solver) trySubmit(ctx context.Context, log *zerolog.Logger, uniqueCode, flag string, submitted map[string]bool, res *challengeResult) (bool, error) {
	resp, err := s.bench.submit(ctx, uniqueCode, flag)
	if err != nil {
		var be *benchError
		if errors.As(err, &be) {
			switch {
			case be.tokenInvalid(), be.taskEnded():
				return false, be // 致命：整个跑分停
			case be.Code == codeDuplicate:
				submitted[flag] = true // 已算过，跳
				return false, nil
			case be.Code == codeChallengeNotFound:
				log.Warn().Str("flag", flag).Msg("challenge_not_found，跳过该题")
				return false, nil
			case be.retriable():
				log.Warn().Err(be).Msg("提交遇可重试错误，下轮再试")
				return false, nil
			}
		}
		log.Warn().Err(err).Str("flag", truncate(flag, 64)).Msg("提交 flag 失败")
		return false, nil
	}
	submitted[flag] = true
	if !resp.Correct {
		log.Debug().Str("flag", truncate(flag, 64)).Msg("flag 错误")
		return false, nil
	}
	res.CorrectFlags = resp.CorrectFlagCount
	res.CumulativeScore = resp.CumulativeScore
	log.Info().Str("flag", flag).Int("awarded", resp.Awarded).
		Int("correct", resp.CorrectFlagCount).Int("total", resp.TotalFlagCount).Msg("✓ flag 正确")
	return resp.CorrectFlagCount == resp.TotalFlagCount && resp.TotalFlagCount > 0, nil
}
