//go:build integration

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
)

// TestVerifyBorrowed_AllPass 4 项条件全满足 → 返回 nil。
func TestVerifyBorrowed_AllPass(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)

	var eid string
	if err := pool.QueryRow(ctx, `
		INSERT INTO engagement (tenant_id, mode, scope_host, status, memory_facts, memory_ideas, memory_hints)
		VALUES ('default','proxy','t-allpass','active',
		        '{"evidence":[{"k":"v"}]}'::jsonb,
		        '{"hypotheses":[{"direction":"d"}]}'::jsonb,
		        '{"hints":[{"from_skill":"vuln/web/bac","content":"c","priority":1}]}'::jsonb)
		RETURNING id`).Scan(&eid); err != nil {
		t.Fatalf("seed engagement: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO llm_call (engagement_id, provider, model, role) VALUES ($1,'deepseek','deepseek-chat','observer')`, eid); err != nil {
		t.Fatalf("seed llm_call: %v", err)
	}

	// 一个正常完成的 task：terminate_by 不属于 ('observer_abort','done_force')，COUNT 命中 0。
	if _, err := pool.Exec(ctx,
		`INSERT INTO agent_task (engagement_id, role, status, result) VALUES ($1,'react','done','{"terminate_by":"done"}'::jsonb)`, eid); err != nil {
		t.Fatalf("seed agent_task: %v", err)
	}

	if err := verifyBorrowedAdoptions(ctx, pool, eid); err != nil {
		t.Fatalf("verifyBorrowedAdoptions 期望通过，got err: %v", err)
	}
}

// TestVerifyBorrowed_MemoryEmpty memory 三层全空 → 返回 error，且消息含 "memory"。
func TestVerifyBorrowed_MemoryEmpty(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)

	var eid string
	if err := pool.QueryRow(ctx, `
		INSERT INTO engagement (tenant_id, mode, scope_host, status)
		VALUES ('default','proxy','t-empty-mem','active') RETURNING id`).Scan(&eid); err != nil {
		t.Fatalf("seed engagement: %v", err)
	}

	err := verifyBorrowedAdoptions(ctx, pool, eid)
	if err == nil {
		t.Fatalf("memory 三层空，期望 err，got nil")
	}
	if !strings.Contains(err.Error(), "memory") {
		t.Fatalf("err 应含 'memory'，got %q", err.Error())
	}
}

// TestVerifyBorrowed_NoDistillHint memory 三层非空但无 BAC distill hint → 返回 error，含 "distill"。
func TestVerifyBorrowed_NoDistillHint(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)

	var eid string
	if err := pool.QueryRow(ctx, `
		INSERT INTO engagement (tenant_id, mode, scope_host, status, memory_facts, memory_ideas, memory_hints)
		VALUES ('default','proxy','t-no-hint','active',
		        '{"evidence":[{"k":"v"}]}'::jsonb,
		        '{"hypotheses":[{"direction":"d"}]}'::jsonb,
		        '{"hints":[{"from_skill":"vuln/web/sqli","content":"c","priority":1}]}'::jsonb)
		RETURNING id`).Scan(&eid); err != nil {
		t.Fatalf("seed engagement: %v", err)
	}

	err := verifyBorrowedAdoptions(ctx, pool, eid)
	if err == nil {
		t.Fatalf("无 BAC distill hint，期望 err，got nil")
	}
	if !strings.Contains(err.Error(), "distill") {
		t.Fatalf("err 应含 'distill'，got %q", err.Error())
	}
}

// TestVerifyBorrowed_NoRoutedCall 满足 memory + distill hint，但无 observer/distill llm_call → 返回 error，含 "多模型路由"。
func TestVerifyBorrowed_NoRoutedCall(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)

	var eid string
	if err := pool.QueryRow(ctx, `
		INSERT INTO engagement (tenant_id, mode, scope_host, status, memory_facts, memory_ideas, memory_hints)
		VALUES ('default','proxy','t-no-route','active',
		        '{"evidence":[{"k":"v"}]}'::jsonb,
		        '{"hypotheses":[{"direction":"d"}]}'::jsonb,
		        '{"hints":[{"from_skill":"vuln/web/bac","content":"c","priority":1}]}'::jsonb)
		RETURNING id`).Scan(&eid); err != nil {
		t.Fatalf("seed engagement: %v", err)
	}

	// 只插 react.main role，不含 observer/distill。
	if _, err := pool.Exec(ctx,
		`INSERT INTO llm_call (engagement_id, provider, model, role) VALUES ($1,'deepseek','deepseek-chat','react.main')`, eid); err != nil {
		t.Fatalf("seed llm_call: %v", err)
	}

	err := verifyBorrowedAdoptions(ctx, pool, eid)
	if err == nil {
		t.Fatalf("无 observer/distill llm_call，期望 err，got nil")
	}
	if !strings.Contains(err.Error(), "多模型路由") {
		t.Fatalf("err 应含 '多模型路由'，got %q", err.Error())
	}
}

// TestVerifyBorrowed_HasAbnormalTermination 满足前 3 项，但有 1 个 task.result.terminate_by='done_force' → 返回 error，含 "observer_abort/done_force"。
func TestVerifyBorrowed_HasAbnormalTermination(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)

	var eid string
	if err := pool.QueryRow(ctx, `
		INSERT INTO engagement (tenant_id, mode, scope_host, status, memory_facts, memory_ideas, memory_hints)
		VALUES ('default','proxy','t-abnormal','active',
		        '{"evidence":[{"k":"v"}]}'::jsonb,
		        '{"hypotheses":[{"direction":"d"}]}'::jsonb,
		        '{"hints":[{"from_skill":"vuln/web/bac","content":"c","priority":1}]}'::jsonb)
		RETURNING id`).Scan(&eid); err != nil {
		t.Fatalf("seed engagement: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO llm_call (engagement_id, provider, model, role) VALUES ($1,'deepseek','deepseek-chat','distill')`, eid); err != nil {
		t.Fatalf("seed llm_call: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO agent_task (engagement_id, role, status, result) VALUES ($1,'react','done','{"terminate_by":"done_force"}'::jsonb)`, eid); err != nil {
		t.Fatalf("seed agent_task: %v", err)
	}

	err := verifyBorrowedAdoptions(ctx, pool, eid)
	if err == nil {
		t.Fatalf("有 done_force terminate_by，期望 err，got nil")
	}
	if !strings.Contains(err.Error(), "observer_abort/done_force") {
		t.Fatalf("err 应含 'observer_abort/done_force'，got %q", err.Error())
	}
}
