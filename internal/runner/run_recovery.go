package runner

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"
)

// run_recovery.go：僵尸 run、孤儿 pending run 回收与搁浅任务重投。
//
// 缺口：active 任务以 MaxRetry(0) 入队（防 4h × 25 retry = 4 天死循环），
// runner 被硬杀（pkill / 断电 / OOM）时 asynq 判任务失败直接进 archived
// 永不重投——agent_run 停在 running、task 停在 active 成僵尸。动作级停摆由
// 执行者看门狗处理，新 run 开始时回收上一 run 留下的在途动作；本监控管 run 与任务。
//
// 本监控补另一半，三条规则：
//
//  1. 僵尸 run：running run + LLM/工具活动停摆超过 timeout（6 分钟 ≈ 2 倍单步
//     LLM 看门狗）→ 旧 run 标记 failed → 以全新
//     agent_run 重投 planner（认知循环从头跑，探索图历史保留）。暂停中或
//     已终态的任务例外：只回收僵尸 run 行（含进程死在终态落库之后的脏行），
//     不重投——恢复分别由继续操作 / 终态判定驱动。
//  2. 孤儿 pending run：pending 超过 timeout，队列里却已没有它（入队后 Redis 数据
//     丢失；领取后、开跑前进程被硬杀，租约过期后因不重试直接归档）→ 标 aborted（从未
//     执行，不计中断次数），由规则 3 在同一轮重投。按队列实况判定，队列积压不会误判；
//     队列查不了时不动。
//  3. 搁浅任务：active 且未暂停，却没有任何 pending/running run（暂停退出后
//     的继续竞态、enqueue 失败等泄漏）→ 重投 planner。活跃任务的不变式是
//     「必有 run 在跑或排队」，此规则让不变式自愈。
//
// 重投带尝试上限（按 error run 计数——只数中断，不数用户主动的暂停/完成）：
// 连续多次中断不再自动恢复（防崩溃循环烧 LLM 账单），超限时任务落 aborted
// 由用户手动重新触发。重投与其它 run 生产者（继续/续接）的毫秒级竞态由
// agent_run 的部分唯一索引判定（每任务至多一个存活 run），先建者赢。

// runRecoveryResumeRole 是恢复/重投使用的 agent 角色。
const runRecoveryResumeRole = "planner"

// messagePublisher 把落库的时间线消息广播给对话的实时订阅者（*convstream.Publisher 满足）。
type messagePublisher interface {
	PublishMessage(ctx context.Context, conversationID string, msg any) error
}

// RunRecoveryMonitor 定期回收僵尸 run 并重投。
type RunRecoveryMonitor struct {
	pool          *pgxpool.Pool
	wc            *worker.Client
	agentRuns     *agentrun.Store
	tasks         *task.Store
	conversations *conversation.Store
	publisher     messagePublisher
	logger        zerolog.Logger
	interval      time.Duration
	timeout       time.Duration
	maxAttempts   int
}

// StartRunRecoveryMonitor 启动僵尸 run 回收循环（非阻塞）。interval、timeout、maxAttempts
// 取自配置（runner.run_recovery_*），加载配置时已校验均 > 0。
func StartRunRecoveryMonitor(ctx context.Context, pool *pgxpool.Pool, wc *worker.Client,
	agentRuns *agentrun.Store, tasks *task.Store, conversations *conversation.Store, publisher messagePublisher,
	logger zerolog.Logger, interval, timeout time.Duration, maxAttempts int) {
	m := &RunRecoveryMonitor{
		pool:          pool,
		wc:            wc,
		agentRuns:     agentRuns,
		tasks:         tasks,
		conversations: conversations,
		publisher:     publisher,
		logger:        logger.With().Str("component", "run_recovery_monitor").Logger(),
		interval:      interval,
		timeout:       timeout,
		maxAttempts:   maxAttempts,
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
// 僵尸判定（与 action 恢复同口径）：run 在跑（running）+ 该 task 的 LLM/工具
// 活动停摆超过 timeout + run 行自身超过 timeout 未被触碰（排除刚建行、首个
// LLM 调用在途的新 run）。不限任务状态：进程死在任务终态之后（停止已落库、
// handler 没跑到回写）同样留下 running 脏行——终态任务只收行，不重投。
func (m *RunRecoveryMonitor) recoverOnce(ctx context.Context) error {
	rows, err := m.pool.Query(ctx, `
		SELECT ar.id::text, ar.task_id::text, t.paused, t.status::text,
		       COALESCE((SELECT c.id::text FROM conversation c WHERE c.task_id = t.id LIMIT 1), ''),
		       (SELECT count(*) FROM agent_run a2
		         WHERE a2.task_id = ar.task_id AND a2.status = 'error')
		FROM agent_run ar
		JOIN task t ON t.id = ar.task_id
		WHERE ar.status = 'running'
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
		runID, taskID, conversationID string
		paused                        bool
		terminal                      bool
		attempts                      int
	}
	var zombies []zombie
	for rows.Next() {
		var z zombie
		var status string
		if err := rows.Scan(&z.runID, &z.taskID, &z.paused, &status, &z.conversationID, &z.attempts); err != nil {
			return err
		}
		z.terminal = status != "active"
		zombies = append(zombies, z)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, z := range zombies {
		// 暂停中/已终态的任务：只回收僵尸 run 行（审计面不留 running 脏态），
		// 不重投、不计中断次数——恢复分别由继续操作 / 任务终态判定驱动。
		if z.paused || z.terminal {
			m.logger.Info().
				Str("agent_run_id", z.runID).
				Str("task_id", z.taskID).
				Bool("paused", z.paused).
				Bool("terminal", z.terminal).
				Msg("暂停/终态任务存在僵尸 run，仅回收 run 行（不重投）")
			if err := m.agentRuns.SetAborted(ctx, z.runID); err != nil {
				m.logger.Error().Err(err).Str("agent_run_id", z.runID).Msg("僵尸 run 行回收失败")
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
			m.abortAfterRetries(ctx, z.taskID, z.conversationID, z.attempts)
			continue
		}

		m.redispatchPlanner(ctx, z.taskID, z.conversationID)
	}
	if err := m.recoverOrphanPending(ctx); err != nil {
		m.logger.Error().Err(err).Msg("孤儿 pending run 回收失败")
	}
	return m.recoverStranded(ctx)
}

// recoverOrphanPending 回收队列里已不存在的 pending run。只看 pending 超过 timeout 的行：
// 生产者先建 run 行再入队，刚建的行可能尚未入队。回收只认 pending（AbortPending），与 handler
// 领取互斥；任务随后由搁浅规则重投（暂停中、已终态的任务只收行，与僵尸 run 同口径）。
func (m *RunRecoveryMonitor) recoverOrphanPending(ctx context.Context) error {
	rows, err := m.pool.Query(ctx, `
		SELECT id::text, task_id::text FROM agent_run
		WHERE status = 'pending' AND updated_at < NOW() - $1 * INTERVAL '1 second'
	`, int(m.timeout.Seconds()))
	if err != nil {
		return err
	}
	type pendingRun struct{ runID, taskID string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pendingRun, error) {
		var p pendingRun
		return p, r.Scan(&p.runID, &p.taskID)
	})
	if err != nil {
		return err
	}
	for _, p := range list {
		queued, err := m.wc.RunQueued(p.runID)
		if err != nil {
			m.logger.Warn().Err(err).Str("agent_run_id", p.runID).Msg("查询 run 的队列状态失败，本轮不回收")
			continue
		}
		if queued {
			continue // 仍在排队（积压）或正在被领取，交给 handler
		}
		aborted, err := m.agentRuns.AbortPending(ctx, p.runID)
		if err != nil {
			m.logger.Error().Err(err).Str("agent_run_id", p.runID).Msg("孤儿 pending run 回收失败")
			continue
		}
		if aborted {
			m.logger.Warn().Str("agent_run_id", p.runID).Str("task_id", p.taskID).
				Msg("pending run 已不在队列中（从未执行），回收后由搁浅规则重投")
		}
	}
	return nil
}

// recoverStranded 重投搁浅任务：active 且未暂停，却没有任何 pending/running
// run。暂停退出 → 继续的竞态窗口、enqueue 失败等泄漏都收口在此——「active
// 任务必有 run 在跑或排队」的不变式由此自愈。刚建 task 与 run 行之间的毫秒级
// 空窗由 30s 巡检间隔天然错过，不计入。
func (m *RunRecoveryMonitor) recoverStranded(ctx context.Context) error {
	rows, err := m.pool.Query(ctx, `
		SELECT t.id::text,
		       COALESCE((SELECT c.id::text FROM conversation c WHERE c.task_id = t.id LIMIT 1), ''),
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
		taskID, conversationID string
		attempts               int
	}
	var list []stranded
	for rows.Next() {
		var s stranded
		if err := rows.Scan(&s.taskID, &s.conversationID, &s.attempts); err != nil {
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
			m.abortAfterRetries(ctx, s.taskID, s.conversationID, s.attempts)
			continue
		}

		m.redispatchPlanner(ctx, s.taskID, s.conversationID)
	}
	return nil
}

// abortAfterRetries 中断次数达上限：任务落 aborted，并往对话时间线落终止节点（与用户停止、
// 心跳回收、运行出错同一口径——否则对话停在「工作中」的最后一条消息，看不出任务已被终止
// 及原因）。节点只由真正完成迁移的一方落，任务已被别处终止时不重复。
func (m *RunRecoveryMonitor) abortAfterRetries(ctx context.Context, taskID, conversationID string, attempts int) {
	reason := "runner 连续中断 " + strconv.Itoa(attempts) + " 次，已停止自动恢复；请手动重新下发任务"
	m.logger.Warn().Str("task_id", taskID).Int("attempts", attempts).Int("limit", m.maxAttempts).
		Msg("中断次数达上限，任务落 aborted（请手动重新触发）")
	applied, err := m.tasks.Abort(ctx, taskID, reason)
	if err != nil {
		m.logger.Error().Err(err).Str("task_id", taskID).Msg("任务落 aborted 失败")
		return
	}
	if applied {
		appendTaskStatus(ctx, m.conversations, m.publisher, m.logger, conversationID,
			conversation.TaskStatusAborted, "任务已终止", map[string]any{"reason": reason})
	}
}

// redispatchPlanner 以全新 agent_run 重投 planner（探索图历史保留，认知循环
// 从头规划）；失败仅告警，下一轮巡检重试。该 task 已有存活 run 时（与继续/
// 续接的毫秒级竞态，部分唯一索引判定）跳过——先建者赢。
func (m *RunRecoveryMonitor) redispatchPlanner(ctx context.Context, taskID, conversationID string) {
	newID, err := m.agentRuns.Create(ctx, agentrun.NewParams{
		TaskID: taskID,
		Role:   runRecoveryResumeRole,
	})
	if err != nil {
		if errors.Is(err, agentrun.ErrLiveRunExists) {
			m.logger.Info().Str("task_id", taskID).
				Msg("已有存活 run，跳过重投（并发生产者先建）")
			return
		}
		m.logger.Error().Err(err).Str("task_id", taskID).Msg("创建恢复 run 失败")
		return
	}
	if _, _, err := m.wc.Enqueue(ctx, worker.RoleAgent, worker.Payload{
		AgentRunID:     newID,
		TaskID:         taskID,
		ConversationID: conversationID,
		TaskRole:       worker.RoleAgent,
	}); err != nil {
		m.logger.Error().Err(err).Str("task_id", taskID).Str("new_agent_run_id", newID).
			Msg("恢复 run 入队失败")
		// 未入队的 pending 行永远不会被消费，却会让本巡检把任务视为「有存活 run」而
		// 不再重投。标 aborted（从未执行，不计入中断次数），下一轮巡检自然重试。
		if aerr := m.agentRuns.SetAborted(context.WithoutCancel(ctx), newID); aerr != nil {
			m.logger.Error().Err(aerr).Str("new_agent_run_id", newID).Msg("撤销未入队的恢复 run 失败")
		}
		return
	}
	m.logger.Info().
		Str("task_id", taskID).
		Str("new_agent_run_id", newID).
		Msg("planner 重投完成")
}
