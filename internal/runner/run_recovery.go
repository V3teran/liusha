package runner

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"
)

// run_recovery.go：僵尸 run 回收与搁浅任务重投。
//
// 缺口：active 任务以 MaxRetry(0) 入队（防 4h × 25 retry = 4 天死循环），
// runner 被硬杀（pkill / 断电 / OOM）时 asynq 判任务失败直接进 archived
// 永不重投——agent_run 停在 running、task 停在 active 成僵尸；探索图的
// action 恢复监控只修图状态，不管这两个行。
//
// 本监控补另一半，两条规则：
//
//  1. 僵尸 run：running run + active task + LLM/工具活动停摆超过 timeout（与
//     action 恢复同口径，6 分钟 ≈ 2 倍单步 LLM 看门狗）→ 旧 run 标记 failed →
//     以全新 agent_run 重投 planner（认知循环从头跑，探索图历史保留）。
//     暂停中的任务例外：只回收僵尸 run 行，不重投——暂停是有意停摆，恢复由
//     用户的继续操作驱动。
//  2. 搁浅任务：active 且未暂停，却没有任何 pending/running run（暂停退出后
//     的继续竞态、enqueue 失败等泄漏）→ 重投 planner。活跃任务的不变式是
//     「必有 run 在跑或排队」，此规则让不变式自愈。
//
// 重投带尝试上限（按 error run 计数——只数中断，不数用户主动的暂停/完成）：
// 连续多次中断不再自动恢复（防崩溃循环烧 LLM 账单），超限时任务落 aborted
// 由用户手动重新触发。

const (
	// runRecoveryMaxAttempts 是单任务自动重投上限（按 error run 计数）。
	runRecoveryMaxAttempts = 4
	// runRecoveryResumeRole 是恢复/重投使用的 agent 角色。
	runRecoveryResumeRole = "planner"
)

// RunRecoveryMonitor 定期回收僵尸 run 并重投。
type RunRecoveryMonitor struct {
	pool        *pgxpool.Pool
	wc          *worker.Client
	agentRuns   *agentrun.Store
	tasks       *task.Store
	logger      zerolog.Logger
	interval    time.Duration
	timeout     time.Duration
	maxAttempts int
}

// StartRunRecoveryMonitor 启动僵尸 run 回收循环（非阻塞）。
// interval/timeout <=0 或 maxAttempts<=0 时用内置默认。
func StartRunRecoveryMonitor(ctx context.Context, pool *pgxpool.Pool, wc *worker.Client,
	agentRuns *agentrun.Store, tasks *task.Store, logger zerolog.Logger,
	interval, timeout time.Duration, maxAttempts int) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if timeout <= 0 {
		timeout = 6 * time.Minute
	}
	if maxAttempts <= 0 {
		maxAttempts = runRecoveryMaxAttempts
	}
	m := &RunRecoveryMonitor{
		pool:        pool,
		wc:          wc,
		agentRuns:   agentRuns,
		tasks:       tasks,
		logger:      logger.With().Str("component", "run_recovery_monitor").Logger(),
		interval:    interval,
		timeout:     timeout,
		maxAttempts: maxAttempts,
	}
	go func() {
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := m.recoverOnce(ctx); err != nil {
					m.logger.Error().Err(err).Msg("僵尸 run 回收失败")
				}
			}
		}
	}()
	m.logger.Info().
		Dur("interval", interval).
		Dur("timeout", timeout).
		Int("max_attempts", m.maxAttempts).
		Msg("僵尸 run 回收监控已启动")
}

// recoverOnce 扫描并回收僵尸 run、重投搁浅任务。
//
// 僵尸判定（与 action 恢复同口径）：run 在跑（running）+ 所属 task 还在
// active + 该 task 的 LLM/工具活动停摆超过 timeout + run 行自身超过 timeout
// 未被触碰（排除刚建行、首个 LLM 调用在途的新 run）。
func (m *RunRecoveryMonitor) recoverOnce(ctx context.Context) error {
	rows, err := m.pool.Query(ctx, `
		SELECT ar.id::text, ar.task_id::text, t.brief, t.paused,
		       (SELECT c.id::text FROM conversation c WHERE c.task_id = t.id LIMIT 1),
		       (SELECT count(*) FROM agent_run a2
		         WHERE a2.task_id = ar.task_id AND a2.status = 'error')
		FROM agent_run ar
		JOIN task t ON t.id = ar.task_id
		WHERE ar.status = 'running'
		  AND t.status = 'active'
		  AND ar.updated_at < NOW() - $1 * INTERVAL '1 second'
		  AND NOT EXISTS (
			SELECT 1 FROM llm_invocation li
			WHERE li.task_id = t.id AND li.created_at > NOW() - $1 * INTERVAL '1 second')
		  AND NOT EXISTS (
			SELECT 1 FROM tool_invocation ti
			WHERE ti.task_id = t.id AND ti.created_at > NOW() - $1 * INTERVAL '1 second')
	`, int(m.timeout.Seconds()))
	if err != nil {
		return err
	}
	defer rows.Close()

	type zombie struct {
		runID, taskID, brief, conversationID string
		paused                               bool
		attempts                             int
	}
	var zombies []zombie
	for rows.Next() {
		var z zombie
		if err := rows.Scan(&z.runID, &z.taskID, &z.brief, &z.paused, &z.conversationID, &z.attempts); err != nil {
			return err
		}
		zombies = append(zombies, z)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, z := range zombies {
		// 暂停中的任务：只回收僵尸 run 行（审计面不留 running 脏态），不重投、
		// 不计中断次数——恢复由用户的继续操作驱动。
		if z.paused {
			m.logger.Info().
				Str("agent_run_id", z.runID).
				Str("task_id", z.taskID).
				Msg("暂停中的任务存在僵尸 run，仅回收 run 行（不重投）")
			if err := m.agentRuns.SetAborted(ctx, z.runID); err != nil {
				m.logger.Error().Err(err).Str("agent_run_id", z.runID).Msg("暂停任务僵尸 run 回收失败")
			}
			continue
		}

		m.logger.Warn().
			Str("agent_run_id", z.runID).
			Str("task_id", z.taskID).
			Int("attempts", z.attempts).
			Dur("timeout", m.timeout).
			Msg("检测到僵尸 run（活动停摆超时），回收并重投")

		// 旧 run 标记失败（UI 与审计如实反映中断）。
		if err := m.agentRuns.SetError(ctx, z.runID,
			"runner 中断：活动停摆超过 "+m.timeout.String()+"，已自动回收并重投"); err != nil {
			m.logger.Error().Err(err).Str("agent_run_id", z.runID).Msg("旧 run 标记失败")
		}

		// 尝试上限：连续中断防崩溃循环（每次重投都从 planner 重新规划）。
		if z.attempts >= m.maxAttempts {
			m.logger.Warn().Str("task_id", z.taskID).
				Int("attempts", z.attempts).Int("limit", m.maxAttempts).
				Msg("中断次数达上限，任务落 aborted（请手动重新触发）")
			if err := m.tasks.Abort(ctx, z.taskID,
				"runner 连续中断 "+strconv.Itoa(z.attempts)+" 次，已停止自动恢复；请手动重新下发任务"); err != nil {
				m.logger.Error().Err(err).Msg("任务落 aborted 失败")
			}
			continue
		}

		m.redispatchPlanner(ctx, z.taskID, z.brief, z.conversationID)
	}
	return m.recoverStranded(ctx)
}

// recoverStranded 重投搁浅任务：active 且未暂停，却没有任何 pending/running
// run。暂停退出 → 继续的竞态窗口、enqueue 失败等泄漏都收口在此——「active
// 任务必有 run 在跑或排队」的不变式由此自愈。刚建 task 与 run 行之间的毫秒级
// 空窗由 30s 巡检间隔天然错过，不计入。
func (m *RunRecoveryMonitor) recoverStranded(ctx context.Context) error {
	rows, err := m.pool.Query(ctx, `
		SELECT t.id::text, t.brief,
		       (SELECT c.id::text FROM conversation c WHERE c.task_id = t.id LIMIT 1),
		       (SELECT count(*) FROM agent_run a2
		         WHERE a2.task_id = t.id AND a2.status = 'error')
		FROM task t
		WHERE t.status = 'active'
		  AND NOT t.paused
		  AND NOT EXISTS (
			SELECT 1 FROM agent_run r
			WHERE r.task_id = t.id AND r.status IN ('pending','running'))
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type stranded struct {
		taskID, brief, conversationID string
		attempts                      int
	}
	var list []stranded
	for rows.Next() {
		var s stranded
		if err := rows.Scan(&s.taskID, &s.brief, &s.conversationID, &s.attempts); err != nil {
			return err
		}
		list = append(list, s)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, s := range list {
		m.logger.Warn().
			Str("task_id", s.taskID).
			Int("attempts", s.attempts).
			Msg("检测到搁浅任务（active 但无存活 run），重投 planner")

		if s.attempts >= m.maxAttempts {
			m.logger.Warn().Str("task_id", s.taskID).
				Int("attempts", s.attempts).Int("limit", m.maxAttempts).
				Msg("中断次数达上限，搁浅任务落 aborted（请手动重新触发）")
			if err := m.tasks.Abort(ctx, s.taskID,
				"runner 连续中断 "+strconv.Itoa(s.attempts)+" 次，已停止自动恢复；请手动重新下发任务"); err != nil {
				m.logger.Error().Err(err).Msg("任务落 aborted 失败")
			}
			continue
		}

		m.redispatchPlanner(ctx, s.taskID, s.brief, s.conversationID)
	}
	return nil
}

// redispatchPlanner 以全新 agent_run 重投 planner（探索图历史保留，认知循环
// 从头规划）；失败仅告警，下一轮巡检重试。
func (m *RunRecoveryMonitor) redispatchPlanner(ctx context.Context, taskID, brief, conversationID string) {
	input, _ := json.Marshal(map[string]string{"brief": brief})
	newID, err := m.agentRuns.Create(ctx, agentrun.NewParams{
		TaskID: taskID,
		Role:   runRecoveryResumeRole,
		Input:  input,
	})
	if err != nil {
		m.logger.Error().Err(err).Str("task_id", taskID).Msg("创建恢复 run 失败")
		return
	}
	if _, _, err := m.wc.Enqueue(ctx, worker.RoleAgent, worker.Payload{
		AgentRunID:     newID,
		TaskID:         taskID,
		ConversationID: conversationID,
		TaskRole:       worker.RoleAgent,
		Input:          input,
	}); err != nil {
		m.logger.Error().Err(err).Str("task_id", taskID).Str("new_agent_run_id", newID).
			Msg("恢复 run 入队失败")
		return
	}
	m.logger.Info().
		Str("task_id", taskID).
		Str("new_agent_run_id", newID).
		Msg("planner 重投完成")
}
