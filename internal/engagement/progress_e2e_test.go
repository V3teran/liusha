//go:build integration

// 外部测试包打破循环依赖：engagement 包本身不能 import vulnfinding / flow / reactrun
// （那些包反向 import engagement 满足 WithCounter 接口）；engagement_test 可以。
package engagement_test

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/reactrun"
	"github.com/V3teran/liusha/internal/vulnfinding"
)

// TestEngagement_ProgressCounters_EndToEnd 验证：
//  1. 装配 WithCounter 后，3 个 store 的写路径 best-effort 维护 engagement.*_count
//  2. Abort 写入 ended_at + error_message + 用 SELECT count(*) 重算 *_count 精确兜底
func TestEngagement_ProgressCounters_EndToEnd(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)

	engs := engagement.NewStore(pool)
	tasks := reactrun.NewStore(pool).WithCounter(engs)
	flows := flow.NewStore(pool, 0, 0).WithCounter(engs)
	finds := vulnfinding.NewStore(pool).WithCounter(engs)

	e, err := engs.LookupOrCreate(ctx, "default", "h", engagement.ModeProxy)
	if err != nil {
		t.Fatal(err)
	}

	// 写 1 条 reactrun
	tid, err := tasks.Create(ctx, reactrun.NewParams{
		EngagementID: e.ID, Role: "orchestrator", Skill: "vuln-web-bac",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 写 1 条 flow
	fid, err := flows.Append(ctx, flow.Flow{
		EngagementID: e.ID, Method: "GET", URL: "/", StatusCode: 200,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 写 1 条 vuln_finding
	if _, _, err := finds.Save(ctx, vulnfinding.VulnFinding{
		EngagementID: e.ID, TaskID: &tid, SourceFlowID: &fid,
		Host: "h", Kind: "BAC", Title: "t", DedupKey: "k",
	}); err != nil {
		t.Fatal(err)
	}

	// active 期间增量计数
	got, err := engs.GetByID(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FlowCount != 1 || got.FindingCount != 1 || got.ReactRunCount != 1 {
		t.Fatalf("active counters: flow=%d finding=%d react=%d",
			got.FlowCount, got.FindingCount, got.ReactRunCount)
	}
	if got.EndedAt != nil {
		t.Fatalf("active EndedAt should be nil, got %v", got.EndedAt)
	}

	// Abort 带错误原因 + 重算精确兜底
	if err := engs.Abort(ctx, e.ID, "explode"); err != nil {
		t.Fatal(err)
	}
	got, err = engs.GetByID(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != engagement.StatusAborted {
		t.Fatalf("status: %s", got.Status)
	}
	if got.EndedAt == nil {
		t.Fatalf("EndedAt should be set after Abort")
	}
	if got.ErrorMessage != "explode" {
		t.Fatalf("ErrorMessage: %q", got.ErrorMessage)
	}
	if got.FlowCount != 1 || got.FindingCount != 1 || got.ReactRunCount != 1 {
		t.Fatalf("post-abort recompute: flow=%d finding=%d react=%d",
			got.FlowCount, got.FindingCount, got.ReactRunCount)
	}
}
