//go:build integration

package finding_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/finding"
)

// 用 dev DB（已跑过 0081 migration）测 triage 新路径：ListAll 全局台账（JOIN task 带 mode +
// 按维度筛选）与 UpdateStatus（状态流转 + triaged_at 打点 + 枚举校验）。
// 需 LIUSHA_POSTGRES_DSN；未设则 skip。
//
//	LIUSHA_POSTGRES_DSN='postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable' \
//	  go test -tags integration ./internal/finding/
func TestFindingStore_TriageRoundTrip(t *testing.T) {
	dsn := os.Getenv("LIUSHA_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("LIUSHA_POSTGRES_DSN 未设，跳过 finding store 集成测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.NewPgPool(ctx, dsn, 5, 1, 5, 0)
	if err != nil {
		t.Fatalf("连库: %v", err)
	}
	defer pool.Close()
	store := finding.NewStore(pool)

	// 建 FK 链：assignment → task（active 模式）。测试尾部 CASCADE 清理。
	var assignmentID, taskID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO assignment (mode, source) VALUES ('active','manual') RETURNING id`,
	).Scan(&assignmentID); err != nil {
		t.Fatalf("建 assignment: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO task (mode, assignment_id, target_host) VALUES ('active',$1::uuid,'triage-test.local') RETURNING id`,
		assignmentID,
	).Scan(&taskID); err != nil {
		t.Fatalf("建 task: %v", err)
	}
	// CASCADE：删 assignment 连带 task + finding（task_id FK ON DELETE CASCADE）。
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM assignment WHERE id=$1::uuid`, assignmentID)
	}()

	// 存一条 finding（默认 status=open）+ evidence。
	saved, err := store.Save(ctx, finding.VulnFinding{
		TaskID:   taskID,
		Host:     "triage-test.local",
		Severity: "high",
		Summary:  "triage 集成测试用漏洞",
		CWEID:    "CWE-89",
		Target:   []byte(`{"path":"/t","method":"GET"}`),
		Evidence: []byte(`{"repro_cmd":"curl x","observation":"ok"}`),
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.Status != "open" {
		t.Fatalf("新建 finding status 应为 open，得 %q", saved.Status)
	}
	if saved.TriagedAt != nil {
		t.Fatalf("未处置 finding triaged_at 应为 nil，得 %v", saved.TriagedAt)
	}

	// ListAll 按 host 筛应命中本条，Mode 派生 active，evidence 透传。
	rows, err := store.ListAll(ctx, finding.LedgerFilter{Host: "triage-test.local"})
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListAll host 筛应 1 条，得 %d", len(rows))
	}
	if rows[0].Mode != "active" {
		t.Fatalf("Mode 应派生为 active，得 %q", rows[0].Mode)
	}
	if len(rows[0].Evidence) == 0 || string(rows[0].Evidence) == "{}" {
		t.Fatalf("evidence 应透传，得 %q", string(rows[0].Evidence))
	}

	// 不去重：第二个 task 挖到同一漏洞（host+cwe+path 相同）→ 台账平铺应 2 条独立（各自 triage）。
	var taskID2 string
	if err := pool.QueryRow(ctx,
		`INSERT INTO task (mode, assignment_id, target_host) VALUES ('active',$1::uuid,'triage-test.local') RETURNING id`,
		assignmentID,
	).Scan(&taskID2); err != nil {
		t.Fatalf("建 task2: %v", err)
	}
	if _, err := store.Save(ctx, finding.VulnFinding{
		TaskID: taskID2, Host: "triage-test.local", Severity: "high",
		Summary: "同漏洞不同次扫描", CWEID: "CWE-89", Target: []byte(`{"path":"/t","method":"GET"}`),
	}); err != nil {
		t.Fatalf("Save task2: %v", err)
	}
	flat, err := store.ListAll(ctx, finding.LedgerFilter{Host: "triage-test.local"})
	if err != nil {
		t.Fatalf("ListAll 平铺: %v", err)
	}
	if len(flat) != 2 {
		t.Fatalf("同漏洞两次扫描应平铺 2 条独立，得 %d", len(flat))
	}

	// UpdateTriage → confirmed + 覆盖 severity(high→critical) + note，返回更新后的行。
	updated, err := store.UpdateTriage(ctx, saved.ID, "confirmed", "critical", "人工核实为真")
	if err != nil {
		t.Fatalf("UpdateTriage: %v", err)
	}
	if updated.Status != "confirmed" {
		t.Fatalf("返回行状态应 confirmed，得 %q", updated.Status)
	}
	if updated.Severity != "critical" {
		t.Fatalf("severity 应被人工覆盖为 critical，得 %q", updated.Severity)
	}
	if updated.TriageNote != "人工核实为真" {
		t.Fatalf("返回行备注应回写，得 %q", updated.TriageNote)
	}
	if updated.TriagedAt == nil {
		t.Fatal("返回行 triaged_at 应非 nil")
	}

	// severity 传空应保留原值（不误清）。
	kept, err := store.UpdateTriage(ctx, saved.ID, "fixed", "", "")
	if err != nil {
		t.Fatalf("UpdateTriage 空 severity: %v", err)
	}
	if kept.Severity != "critical" {
		t.Fatalf("空 severity 应保留上次的 critical，得 %q", kept.Severity)
	}

	// 非法 status 应报错（DB CHECK 前的前置校验）。
	if _, err := store.UpdateTriage(ctx, saved.ID, "bogus", "", ""); err == nil {
		t.Fatal("非法 status 应报错，但没有")
	}

	// 不存在 id 应报错。
	if _, err := store.UpdateTriage(ctx, "00000000-0000-0000-0000-000000000000", "fixed", "", ""); err == nil {
		t.Fatal("不存在 id 应报错，但没有")
	}
}
