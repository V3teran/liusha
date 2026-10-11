package explorationgraph

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
)

// RecoveryMonitor 定期检测并恢复卡住的 action
type RecoveryMonitor struct {
	store    *Store
	logger   zerolog.Logger
	interval time.Duration
	timeout  time.Duration
}

// NewRecoveryMonitor 创建恢复监控器
func NewRecoveryMonitor(store *Store, logger zerolog.Logger, interval, timeout time.Duration) *RecoveryMonitor {
	return &RecoveryMonitor{
		store:    store,
		logger:   logger.With().Str("component", "recovery_monitor").Logger(),
		interval: interval,
		timeout:  timeout,
	}
}

// Start 启动监控循环（阻塞）
func (m *RecoveryMonitor) Start(ctx context.Context) error {
	m.logger.Info().
		Dur("interval", m.interval).
		Dur("timeout", m.timeout).
		Msg("启动 action 恢复监控")

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	// 立即执行一次
	if err := m.recover(ctx); err != nil {
		m.logger.Error().Err(err).Msg("初始恢复检查失败")
	}

	for {
		select {
		case <-ctx.Done():
			m.logger.Info().Msg("停止 action 恢复监控")
			return ctx.Err()
		case <-ticker.C:
			if err := m.recover(ctx); err != nil {
				m.logger.Error().Err(err).Msg("恢复检查失败")
			}
		}
	}
}

// recover 检测并恢复卡住的 action。
//
// 活性判定用 task 级真实工作信号（最近的 LLM 调用或工具调用），不用
// action.updated_at——action 进入 running 后执行期的迭代/工具活动不触碰
// 该行，按它判死会把长 ReAct 的健康 action 误杀。task 在窗口内有任何
// LLM/工具活动即视为在推进；两者都停摆超过 timeout 才判死。
func (m *RecoveryMonitor) recover(ctx context.Context) error {
	if m.store.pool == nil {
		return fmt.Errorf("recovery requires direct DB access (pool is nil)")
	}

	query := `
		UPDATE exploration_node
		SET
			state = 'failed',
			metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object(
				'failure_reason', '执行超时（' || $1::text || '秒无 LLM/工具活动）',
				'recovered_at', NOW(),
				'previous_state', 'running'
			)
		WHERE kind = 'action'
		  AND state = 'running'
		  AND NOT EXISTS (
			SELECT 1 FROM llm_invocation li
			WHERE li.task_id = exploration_node.task_id
			  AND li.created_at > NOW() - $1 * INTERVAL '1 second'
		  )
		  AND NOT EXISTS (
			SELECT 1 FROM tool_invocation ti
			WHERE ti.task_id = exploration_node.task_id
			  AND ti.created_at > NOW() - $1 * INTERVAL '1 second'
		  )
		RETURNING id, task_id
	`

	rows, err := m.store.pool.Query(ctx, query, int(m.timeout.Seconds()))
	if err != nil {
		return fmt.Errorf("query stale actions: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var actionID, taskID string
		if err := rows.Scan(&actionID, &taskID); err != nil {
			m.logger.Error().Err(err).Msg("scan recovered action failed")
			continue
		}

		m.logger.Warn().
			Str("action_id", actionID).
			Str("task_id", taskID).
			Dur("timeout", m.timeout).
			Msg("恢复卡住的 action（task 无 LLM/工具活动）")
		count++
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate recovered actions: %w", err)
	}

	if count > 0 {
		m.logger.Info().Int("count", count).Msg("本轮恢复了卡住的 action")
	}

	return nil
}
