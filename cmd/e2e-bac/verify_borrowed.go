// Package main 中的 verify_borrowed.go 提供 4 项黑客松借鉴落地断言；
// 在 finding 轮询通过后由 main() 调用，验证 spec §11 的"借鉴改动确实生效"指标：
//
//  1. engagement.memory_facts/ideas/hints 三层 jsonb 都有写入（创新 F8 三层记忆）
//  2. memory_hints 至少 1 条 from_skill='vuln/web/bac' 的 distill hint（创新 F8 distill）
//  3. llm_call.role 至少 1 次 'observer' 或 'distill'（创新 F11 多模型路由）
//  4. agent_task.result.terminate_by 不含 'observer_abort'/'done_force'（异常终止守卫存在但未被触发）
//
// 任一断言失败返回带具体维度的 error；调用方负责打印 + 退出码 2。
package main

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// verifyBorrowedAdoptions 跑 4 条 SQL，全部通过返回 nil；任何一条失败立即返回带场景描述的 error。
// 阈值固定（"非空"/"≥1"/"=0"），不接受参数——对 e2e 唯一目标"借鉴是否落地"来说最简明。
func verifyBorrowedAdoptions(ctx context.Context, pool *pgxpool.Pool, eid string) error {
	// 1. memory 三层都非空（jsonb '{}' 序列化为 2 字节，>2 才算有内容）
	var f, i, h []byte
	if err := pool.QueryRow(ctx,
		`SELECT memory_facts, memory_ideas, memory_hints FROM engagement WHERE id=$1`, eid).
		Scan(&f, &i, &h); err != nil {
		return fmt.Errorf("read memory: %w", err)
	}
	if len(f) <= 2 || len(i) <= 2 || len(h) <= 2 {
		return fmt.Errorf("memory 三层有空：facts=%s ideas=%s hints=%s", f, i, h)
	}

	// 2. 至少一条 BAC skill 写入的 distill hint（用 jsonb_array_elements 解开 hints 数组）
	var distillHints int
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM engagement, jsonb_array_elements(memory_hints->'hints') h
		WHERE engagement.id=$1 AND h->>'from_skill' = 'vuln/web/bac'`, eid).Scan(&distillHints); err != nil {
		return fmt.Errorf("count distill hints: %w", err)
	}
	if distillHints == 0 {
		return fmt.Errorf("无 BAC distill hint")
	}

	// 3. 至少一次 observer/distill 角色调用，证明 router 把请求按 role 分流到了不同模型
	var routedCalls int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM llm_call WHERE engagement_id=$1 AND role IN ('observer','distill')`, eid).
		Scan(&routedCalls); err != nil {
		return fmt.Errorf("count routed calls: %w", err)
	}
	if routedCalls == 0 {
		return fmt.Errorf("无 observer/distill llm_call，多模型路由未生效")
	}

	// 4. 不应触发 observer_abort/done_force —— 守卫机制存在但 e2e 数据集走的是正常终止路径
	var abnormal int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent_task WHERE engagement_id=$1 AND result->>'terminate_by' IN ('observer_abort','done_force')`, eid).
		Scan(&abnormal); err != nil {
		return fmt.Errorf("count abnormal terminations: %w", err)
	}
	if abnormal > 0 {
		return fmt.Errorf("出现 observer_abort/done_force 共 %d 次，e2e 期望 0 次", abnormal)
	}

	return nil
}
