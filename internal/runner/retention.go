package runner

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/traffic"
)

// RetentionInterval 是记录表留存清理的巡检周期（每日一次）。
const RetentionInterval = 24 * time.Hour

// StartRetentionSweep 启动记录表的统一留存清理：每日删除各表到期行（对应天数配置 0 = 永久
// 留存，不清理）。runner 单实例部署，天然承担单例清理职责。各表的删除是批删语义
// （DeleteOlderThan 每批至多 5000 行），这里循环调到删完——首次开启留存时存量可能几十万
// 行，一条大 DELETE 会长时间持锁。llm_invocation 的内存 buffer 先 Flush，避免 buffer 内的
// 新记录与删除窗口竞争。
// 范围只覆盖「运行过程记录」（诊断/流水）；任务证据与用户数据（消息、探索图、漏洞、知识）
// 与审计链 audit_log 不按时间清理。
func StartRetentionSweep(
	ctx context.Context,
	llm *llminvocation.Store,
	tools *toolinvocation.Store,
	proxy *traffic.ProxyStore,
	agent *traffic.AgentStore,
	llmDays, toolDays, trafficDays int,
	logger zerolog.Logger,
) {
	entries := []struct {
		name  string
		days  int
		first bool // llm_invocation 删除前先 Flush（它是唯一有内存写缓冲的表）
		fn    func(ctx context.Context, cutoff time.Time) (int64, error)
	}{
		{name: "llm_invocation", days: llmDays, first: true, fn: llm.DeleteOlderThan},
		{name: "tool_invocation", days: toolDays, fn: tools.DeleteOlderThan},
		{name: "proxy_traffic", days: trafficDays, fn: proxy.DeleteOlderThan},
		{name: "agent_traffic", days: trafficDays, fn: agent.DeleteOlderThan},
	}

	go func() {
		logger.Info().Dur("interval", RetentionInterval).Msg("记录表留存清理已启动")
		sweep := func() {
			for _, e := range entries {
				if e.days <= 0 {
					continue
				}
				if e.first {
					if err := llm.Flush(ctx); err != nil {
						logger.Warn().Err(err).Msg("留存清理前 flush 失败（仍继续删除到期行）")
					}
				}
				cutoff := time.Now().AddDate(0, 0, -e.days)
				var total int64
				for {
					n, err := e.fn(ctx, cutoff)
					if err != nil {
						logger.Warn().Err(err).Str("table", e.name).Msg("留存清理失败（下轮重试）")
						total = -1
						break
					}
					total += n
					if n == 0 {
						break
					}
				}
				if total > 0 {
					logger.Info().Int64("deleted", total).Str("table", e.name).Msg("到期记录已清理")
				}
			}
		}

		sweep()
		ticker := time.NewTicker(RetentionInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweep()
			}
		}
	}()
}
