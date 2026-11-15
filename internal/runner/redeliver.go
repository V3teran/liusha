package runner

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/task"
)

// RedeliverUndeliveredFindings 补交付坐实了却没进台账的结果（runner 启动时执行一次）：交付
// 一直失败，或 0230 之前被按接口退化的去重键合并掉的发现。载荷与正常交付同一份（结果节点 +
// 坐实它的那次验证），按结果节点幂等，重复执行不新增；单个结果失败只记日志、不影响其余。
// 补交付没有归属的 run，也不往对话时间线补发发现卡（事后补齐，不是新发现）。
func RedeliverUndeliveredFindings(ctx context.Context, findings *finding.Store, graph *explorationgraph.Store,
	tasks *task.Store, logger zerolog.Logger) error {
	ids, err := findings.UndeliveredResults(ctx)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	sink := finding.NewClaimSink(findings, "").
		WithTaskHostFallback(func(ctx context.Context, taskID string) (string, error) {
			t, err := tasks.GetByID(ctx, taskID)
			if err != nil {
				return "", err
			}
			return t.TargetHost, nil
		})
	delivered := 0
	for _, id := range ids {
		if err := evaluator.Redeliver(ctx, graph, sink, id); err != nil {
			logger.Warn().Err(err).Str("result_node_id", id).Msg("坐实结果补交付失败（下次启动重试）")
			continue
		}
		delivered++
	}
	logger.Info().Int("delivered", delivered).Int("pending", len(ids)).Msg("坐实结果补交付完成")
	if delivered < len(ids) {
		return fmt.Errorf("坐实结果补交付：%d 个失败", len(ids)-delivered)
	}
	return nil
}
