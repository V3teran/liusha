//go:build integration

// Schema 契约冒烟：覆盖此前无集成测试的 store，对真实迁移后的库做最小读写往返。
// 目的：列改名/类型统一（0157-0159）后，任何列序/列名错位在此立刻爆炸，而非运行期。
package dbtest

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/V3teran/liusha/internal/cachestore"

	"github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/config"
	settingstore "github.com/V3teran/liusha/internal/config/setting"
	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/cronschedule"
	"github.com/V3teran/liusha/internal/insight"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/skillstore"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/toolinvocation"
)

func TestStoresSchemaSmoke(t *testing.T) {
	pool := NewPgPool(t)
	ctx := context.Background()

	// agent（实体表）
	agents := agent.NewStore(pool)
	cx := "medium"
	if _, err := agents.Upsert(ctx, "planner", "planner", "smoke", "p", agent.UpdateParams{Complexity: &cx}); err != nil {
		t.Fatalf("agent upsert: %v", err)
	}
	if got, err := agents.GetByCode(ctx, "planner"); err != nil || got.Complexity != "medium" {
		t.Fatalf("agent GetByCode: %v %+v", err, got)
	}

	// assignment/task/agent_run 链
	asgID := SeedAssignment(t, pool)
	tasks := task.NewStore(pool)
	tk, err := tasks.Create(ctx, task.NewParams{AssignmentID: asgID, Brief: "smoke"})
	if err != nil {
		t.Fatalf("task create: %v", err)
	}
	runs := agentrun.NewStore(pool)
	runID, err := runs.Create(ctx, agentrun.NewParams{TaskID: tk.ID, Role: "planner", Input: []byte(`{}`)})
	if err != nil {
		t.Fatalf("agent_run create: %v", err)
	}

	// tool_invocation（FK 指向 run）
	ti := toolinvocation.NewStore(pool)
	if _, err := ti.Append(ctx, toolinvocation.Invocation{
		AgentRunID: runID, TaskID: tk.ID, ToolName: "run_command",
		Args: []byte(`{}`), OutputSize: 2, OutputPreview: "ok", DurationMs: 3, Done: true,
	}); err != nil {
		t.Fatalf("tool_invocation append: %v", err)
	}

	// llm_invocation（LLM 审计；Append 满即丢缓冲，GetByID 前先 Flush）
	inv := llminvocation.NewStoreWithConfig(pool, config.InvocationConfig{})
	if _, err := inv.Append(ctx, llminvocation.Invocation{
		AgentRunID: &runID, TaskID: &tk.ID, Provider: "deepseek", Model: "deepseek-chat",
		Role: "executor", Messages: []byte("[]"), Result: []byte("{}"),
	}); err != nil {
		t.Fatalf("llm_invocation append: %v", err)
	}
	inv.Flush(ctx)

	// audit
	aud := audit.NewStore(pool)
	if _, err := aud.Append(ctx, audit.Event{
		Actor: "api", Action: "task.create", TargetKind: "task", TargetID: tk.ID,
	}); err != nil {
		t.Fatalf("audit append: %v", err)
	}

	// insight（0156 列名 + CHECK 枚举）
	ins := insight.NewStore(pool)
	if err := ins.Append(ctx, tk.ID, insight.Insight{
		Category: insight.CategoryNote, Priority: insight.PriorityMedium, Confidence: insight.ConfidencePossible,
		Summary: "smoke", Tags: []string{"t"}, SourceTaskID: tk.ID, SourceAgentRunID: runID,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("insight append: %v", err)
	}
	listed, err := ins.List(ctx, tk.ID, 10)
	if err != nil || len(listed) != 1 {
		t.Fatalf("insight list: %v len=%d", err, len(listed))
	}

	// skillstore
	sk := skillstore.NewStore(pool)
	if _, err := sk.Create(ctx, skillstore.Skill{
		Code: "smoke", Category: "tooling", Name: "smoke", Description: "d", Body: "b",
	}); err != nil {
		t.Fatalf("skill create: %v", err)
	}

	// cron_schedule
	cron := cronschedule.NewStore(pool)
	if _, err := cron.Create(ctx, cronschedule.NewParams{
		CronExpr: "0 3 * * *", Title: "smoke",
	}); err != nil {
		t.Fatalf("cron create: %v", err)
	}

	// corpus（Add 需要 embedding 维度匹配 pgvector 列；传空向量走 SQL 侧 NULL?——
	// Add 签名要求 embedding，给一个 pgvector 默认维度向量）
	cp := corpus.NewStore(pool)
	vec := make([]float32, 1024) // corpus.embedding 列固定 1024 维
	if _, err := cp.Add(ctx, corpus.Entry{
		Title: "smoke", Content: "c", Tags: []string{"t"}, Source: "expert",
		SourceTaskID: tk.ID, Embedding: vec, ContentHash: "h-smoke",
	}); err != nil {
		t.Fatalf("corpus add: %v", err)
	}

	// system_setting（group_key CHECK: compaction/runtime/proxy_filter）
	set := settingstore.New(pool, smokeCache(t))
	if err := set.SaveRuntime(ctx, settingstore.RuntimeSettings{FindingsLimitInPrompt: 20}); err != nil {
		t.Fatalf("setting save runtime: %v", err)
	}
	if got, err := set.GetRuntimeRaw(ctx); err != nil || got.FindingsLimitInPrompt != 20 {
		t.Fatalf("setting get runtime: %v %+v", err, got)
	}

	// task_control_event（ID 由 DB 生成，Create 返回）
	cp2 := controlplane.NewStore(pool)
	if _, err := cp2.Create(ctx, tk.ID, controlplane.CommandPause, []byte(`{}`)); err != nil {
		t.Fatalf("control event create: %v", err)
	}
	evs, err := cp2.ListPending(ctx, tk.ID)
	if err != nil || len(evs) != 1 {
		t.Fatalf("control list pending: %v len=%d", err, len(evs))
	}
}

// smokeCache 冒烟测试用的共享 cachestore（miniredis 后端，避免 nil-rdb panic）。
func smokeCache(t *testing.T) *cachestore.Cache {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return cachestore.New(rdb, 0)
}
