//go:build integration

// Schema 契约冒烟：覆盖没有专属集成测试的 store，对真实迁移后的库做最小读写往返。
// 目的：store 的列序 / 列名与 schema 错位时在此立刻爆炸，而非运行期。
package dbtest

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/cachestore"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/cronschedule"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/override"
	settingstore "github.com/V3teran/liusha/internal/settingstore"
	"github.com/V3teran/liusha/internal/skillstore"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/toolinvocation"
)

func TestStoresSchemaSmoke(t *testing.T) {
	pool := NewPgPool(t)
	ctx := context.Background()

	// agent 自定义层（agent_override）：写入一个字段并回读生效值
	cfgStore := configstore.New(pool, smokeCache(t), configstore.ToolCatalogFrom(nil))
	if _, err := cfgStore.PatchAgent(ctx, "planner", override.Document{"complexity": json.RawMessage(`"simple"`)}); err != nil {
		t.Fatalf("agent patch: %v", err)
	}
	if got, err := cfgStore.Agent(ctx, "planner"); err != nil || got.Complexity != "simple" {
		t.Fatalf("agent 生效值: %v %+v", err, got)
	}

	// assignment/task/agent_run 链
	asgID := SeedAssignment(t, pool)
	tasks := task.NewStore(pool)
	tk, err := tasks.Create(ctx, task.NewParams{AssignmentID: asgID, Goal: "smoke"})
	if err != nil {
		t.Fatalf("task create: %v", err)
	}
	runs := agentrun.NewStore(pool)
	runID, err := runs.Create(ctx, agentrun.NewParams{TaskID: tk.ID})
	if err != nil {
		t.Fatalf("agent_run create: %v", err)
	}

	// tool_invocation（FK 指向 run）
	ti := toolinvocation.NewStore(pool)
	if _, err := ti.Append(ctx, toolinvocation.Invocation{
		AgentRunID: runID, TaskID: tk.ID, ToolName: "command_run",
		Args: []byte(`{}`), OutputSize: 2, Output: "ok", DurationMs: 3,
	}); err != nil {
		t.Fatalf("tool_invocation append: %v", err)
	}

	// llm_invocation（模型日志；Append 满即丢缓冲，GetByID 前先 Flush）
	inv := llminvocation.NewStore(pool)
	inv.Append(llminvocation.Invocation{
		AgentRunID: &runID, TaskID: &tk.ID, Provider: "deepseek", Model: "deepseek-chat",
		Role: "executor", Messages: []byte("[]"), Result: []byte("{}"),
	})
	inv.Flush(ctx)

	// audit
	aud := audit.NewStore(pool)
	if _, err := aud.Append(ctx, audit.Event{
		Actor: "api", Action: "task.create", TargetKind: "task", TargetID: tk.ID,
	}); err != nil {
		t.Fatalf("audit append: %v", err)
	}

	// 冒烟验探索图 observation 的七值类别落库。
	graph := explorationgraph.NewStore(pool)
	obsNode := explorationgraph.Node{
		ID:       uuid.NewString(),
		TaskID:   tk.ID,
		Kind:     core.KindObservation,
		Content:  json.RawMessage(`{"statement":"smoke","category":"note","reasoning":"r"}`),
		Priority: explorationgraph.PriorityMedium,
	}
	if _, err := graph.CreateNode(ctx, obsNode); err != nil {
		t.Fatalf("observation create: %v", err)
	}

	// 自建技能（skill 表）
	if _, err := cfgStore.CreateSkill(ctx, skillstore.Skill{
		Code: "smoke", Category: "tool", Name: "smoke", Description: "d", Body: "b", Enabled: true,
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

	// corpus：写入一条知识并记下任务的印证（印证表外键指向 task）
	kb := corpus.NewService(corpus.NewStore(pool), corpus.StaticModels(corpus.Models{}), nil, zerolog.Nop())
	entry, err := kb.Create(ctx, corpus.Draft{Title: "smoke", Content: "c", Source: corpus.SourceExpert})
	if err != nil {
		t.Fatalf("corpus create: %v", err)
	}
	if err := kb.Confirm(ctx, entry.ID, tk.ID); err != nil {
		t.Fatalf("corpus confirm: %v", err)
	}

	// 系统设置自定义层（setting_override，group_key CHECK: compaction/runtime/proxy_filter）。
	// 出厂值取自配置文件模板（与生产同一装配）；空配置造不出合法的出厂值（校验会拒绝）。
	cfg, err := config.Load(filepath.Join("..", "config", "config.yaml"))
	if err != nil {
		t.Fatalf("load config template: %v", err)
	}
	set := settingstore.New(pool, smokeCache(t), settingstore.DefaultsFrom(cfg))
	if err := set.Patch(ctx, settingstore.GroupRuntime, override.Document{"step_llm_timeout_seconds": json.RawMessage(`240`)}); err != nil {
		t.Fatalf("setting patch runtime: %v", err)
	}
	if got, err := set.Runtime(ctx); err != nil || got.StepLLMTimeoutSeconds != 240 {
		t.Fatalf("setting runtime 生效值: %v %+v", err, got)
	}
}

// smokeCache 冒烟测试用的共享 cachestore（miniredis 后端，避免 nil-rdb panic）。
func smokeCache(t *testing.T) *cachestore.Cache {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return cachestore.New(rdb, 0)
}
