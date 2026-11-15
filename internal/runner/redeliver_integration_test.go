//go:build integration

package runner

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/task"
)

// 坐实了却没进台账的结果（曾被按接口退化的去重键吞掉，或交付一直失败）按正常交付的同一套
// 载荷补交付：摘要、严重度、主机、复现配方、验证证据都取自结果节点与坐实它的那次验证；
// 按结果节点幂等，重复执行不新增。
func TestRedeliverUndeliveredFindings(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	tasks := task.NewStore(pool)
	graph := explorationgraph.NewStore(pool)
	findings := finding.NewStore(pool)

	tk, err := tasks.Create(ctx, task.NewParams{AssignmentID: dbtest.SeedAssignment(t, pool), Goal: "扫描 http://t.local/search"})
	if err != nil {
		t.Fatal(err)
	}
	hypothesis := uuid.NewString()
	pending := explorationgraph.VerificationConfirmed
	if _, err := graph.CreateNode(ctx, explorationgraph.Node{
		ID: hypothesis, TaskID: tk.ID, Kind: core.KindObservation,
		Content:            json.RawMessage(`{"description":"q 参数反射"}`),
		VerificationStatus: &pending, SourceType: explorationgraph.SourceExecutor,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	repro := json.RawMessage(`{"domain":"web","recipe":{"request":{"method":"GET","url":"http://t.local/search?q=%3Csvg%3E"}},"assert":{}}`)
	verified := time.Now().Add(-time.Minute)
	if _, err := graph.RecordVerification(ctx, explorationgraph.Verification{
		ID: uuid.NewString(), TaskID: tk.ID, NodeID: hypothesis, Repro: repro,
		Outcome:    explorationgraph.VerificationConfirmed,
		Evidence:   json.RawMessage(`{"url":"http://t.local/search?q=%3Csvg%3E","verification_tier":"full_gate","reasoning":"回显未编码"}`),
		DurationMs: 1200, CreatedAt: verified,
	}); err != nil {
		t.Fatal(err)
	}
	result := uuid.NewString()
	if err := graph.CreateNodeWithEdges(ctx, explorationgraph.Node{
		ID: result, TaskID: tk.ID, Kind: core.KindResult, Priority: explorationgraph.PriorityHigh,
		Content:    json.RawMessage(`{"statement":"/search 的 q 参数存在反射型 XSS","impact":"可执行任意脚本","suggestion":"输出编码"}`),
		SourceType: explorationgraph.SourceEvaluator, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}, []explorationgraph.Edge{{TaskID: tk.ID, SrcID: hypothesis, Rel: explorationgraph.RelGenerates, DstID: result, CreatedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}

	logger := zerolog.New(io.Discard)
	for i := 0; i < 2; i++ {
		if err := RedeliverUndeliveredFindings(ctx, findings, graph, tasks, logger); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := findings.ListByTask(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("应补交付且只有一条，得 %d 条", len(rows))
	}
	f := rows[0]
	if f.ResultNodeID != result || f.Summary != "/search 的 q 参数存在反射型 XSS" || f.Severity != "high" || f.Host != "t.local" {
		t.Errorf("补交付的载荷应取自结果节点与验证记录，得 %+v", f)
	}
	if f.Remediation != "输出编码" || len(f.Repro) == 0 || f.AgentRunID != nil {
		t.Errorf("处理建议、复现配方应随交付落库，补交付没有归属的 run：remediation=%q repro=%s run=%v", f.Remediation, f.Repro, f.AgentRunID)
	}
	var eval struct {
		Tier      string `json:"verification_tier"`
		Reasoning string `json:"judge_reasoning"`
	}
	if err := json.Unmarshal(f.Evaluation, &eval); err != nil || eval.Tier != "full_gate" || eval.Reasoning != "回显未编码" {
		t.Errorf("验证档位与判定理由应随交付落库，得 %s", f.Evaluation)
	}
}
