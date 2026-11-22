//go:build integration

package configstore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/builtin"
	"github.com/V3teran/liusha/internal/builtinsync"
	"github.com/V3teran/liusha/internal/cronschedule"
	"github.com/V3teran/liusha/internal/override"
	"github.com/V3teran/liusha/internal/scenariostore"
	"github.com/V3teran/liusha/internal/task"
)

func TestScenarios_BuiltinsSeededAndDefaultUsable(t *testing.T) {
	s, ctx := newTestStore(t)
	all, err := s.Scenarios(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || !all[0].IsBuiltin || !all[1].IsBuiltin {
		t.Fatalf("出厂同步后应有两个内置场景: %+v", all)
	}
	pentest, _ := builtin.ScenarioByCode(scenariostore.DefaultCode)
	sc, err := s.UsableScenario(ctx, scenariostore.DefaultCode)
	if err != nil || sc.Body != pentest.Body {
		t.Fatalf("缺省场景应可用且为出厂正文: %+v %v", sc, err)
	}
	if _, err := s.UsableScenario(ctx, "nope"); !errors.Is(err, scenariostore.ErrUnavailable) {
		t.Fatalf("未知场景应按不合法拒绝（400）: %v", err)
	}
}

func TestScenarios_CustomLifecycleAndConflicts(t *testing.T) {
	s, ctx := newTestStore(t)
	if _, err := s.CreateScenario(ctx, scenariostore.Scenario{Code: "ctf", Name: "x", Body: "y"}); !errors.Is(err, scenariostore.ErrConflict) {
		t.Fatalf("与内置同名应冲突: %v", err)
	}
	if _, err := s.CreateScenario(ctx, scenariostore.Scenario{Code: "audit", Name: "代码审计", Body: strings.Repeat("长", scenariostore.MaxBodyRunes+1)}); !errors.Is(err, override.ErrInvalid) {
		t.Fatalf("正文超长应被拒: %v", err)
	}
	if _, err := s.CreateScenario(ctx, scenariostore.Scenario{Code: "audit", Name: strings.Repeat("名", scenariostore.MaxNameRunes+1), Body: "b"}); !errors.Is(err, override.ErrInvalid) {
		t.Fatalf("名称超长应被拒: %v", err)
	}
	if _, err := s.CreateScenario(ctx, scenariostore.Scenario{Code: "audit", Name: "代码审计", Body: "审计要点", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchScenario(ctx, "audit", override.Document{"enabled": mustJSON2(false)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UsableScenario(ctx, "audit"); !errors.Is(err, scenariostore.ErrUnavailable) {
		t.Fatalf("已停用场景不可用于新任务: %v", err)
	}
	if _, err := s.ResetScenario(ctx, "audit"); !errors.Is(err, scenariostore.ErrCustom) {
		t.Fatalf("自建场景没有出厂可恢复: %v", err)
	}
	if err := s.DeleteScenario(ctx, "audit"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Scenario(ctx, "audit"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后应不存在: %v", err)
	}
}

func TestScenarios_EditAndResetBuiltin(t *testing.T) {
	s, ctx := newTestStore(t)
	ctf, _ := builtin.ScenarioByCode("ctf")
	if _, err := s.PatchScenario(ctx, "ctf", override.Document{"body": mustJSON("我的 CTF 规约")}); err != nil {
		t.Fatal(err)
	}
	d, _ := s.ScenarioDetail(ctx, "ctf")
	if _, ok := d.Overrides["body"]; !ok || d.Defaults == nil || d.Defaults.Body != ctf.Body {
		t.Fatalf("改过的正文应标为已自定义并附出厂内容: %+v", d)
	}
	if _, err := s.ResetScenario(ctx, "ctf", "body"); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.ScenarioDetail(ctx, "ctf"); len(d.Overrides) != 0 || d.Scenario.Body != ctf.Body {
		t.Fatalf("恢复出厂后应为出厂正文: %+v", d)
	}
}

// 被定时模板（含停用中的）引用的场景不可删除、停用；模板只能引用存在且启用的场景。
func TestScenarios_ScheduleReferenceGuards(t *testing.T) {
	s, pool := newTestStoreWithPool(t)
	ctx := context.Background()
	schedules := cronschedule.NewStore(pool)
	if _, err := s.CreateScenario(ctx, scenariostore.Scenario{Code: "audit", Name: "代码审计", Body: "审计要点", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	newSched := func(scenario string) (cronschedule.Schedule, error) {
		return schedules.Create(ctx, cronschedule.NewParams{
			CronExpr: "0 3 * * *", Title: "每日审计", Items: []cronschedule.Item{{Brief: "http://127.0.0.1:8080"}}, Scenario: scenario,
		})
	}
	if _, err := newSched("nope"); !errors.Is(err, scenariostore.ErrUnavailable) {
		t.Fatalf("引用不存在的场景应被拒: %v", err)
	}
	sched, err := newSched("audit")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schedules.SetEnabled(ctx, sched.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteScenario(ctx, "audit"); !errors.Is(err, scenariostore.ErrInUse) || !strings.Contains(err.Error(), "每日审计") {
		t.Fatalf("被（停用中的）模板引用时不可删除，错误应列出模板: %v", err)
	}
	if _, err := s.PatchScenario(ctx, "audit", override.Document{"enabled": mustJSON2(false)}); !errors.Is(err, scenariostore.ErrInUse) {
		t.Fatalf("被模板引用时不可停用: %v", err)
	}
	if err := schedules.Delete(ctx, sched.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchScenario(ctx, "audit", override.Document{"enabled": mustJSON2(false)}); err != nil {
		t.Fatal(err)
	}
	if _, err := newSched("audit"); !errors.Is(err, scenariostore.ErrUnavailable) {
		t.Fatalf("引用已停用的场景应被拒: %v", err)
	}
}

// 并发「停用场景 ‖ 新建引用它的模板」：同一把场景锁串行，结局只能是其中一方成功——
// 不会出现模板引用了已停用的场景。
func TestScenarios_DisableRacesScheduleCreate(t *testing.T) {
	for i := 0; i < 5; i++ {
		s, pool := newTestStoreWithPool(t)
		ctx := context.Background()
		schedules := cronschedule.NewStore(pool)
		if _, err := s.CreateScenario(ctx, scenariostore.Scenario{Code: "audit", Name: "审计", Body: "b", Enabled: true}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var disableErr, createErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, disableErr = s.PatchScenario(ctx, "audit", override.Document{"enabled": mustJSON2(false)})
		}()
		go func() {
			defer wg.Done()
			_, createErr = schedules.Create(ctx, cronschedule.NewParams{CronExpr: "0 3 * * *", Title: "t", Scenario: "audit"})
		}()
		wg.Wait()
		if (disableErr == nil) == (createErr == nil) {
			t.Fatalf("应恰有一方成功: 停用=%v 建模板=%v", disableErr, createErr)
		}
	}
}

// 任务读自身快照：场景之后被改、删都不影响已建任务。
func TestScenarios_TaskSnapshotSurvivesEditAndDelete(t *testing.T) {
	s, pool := newTestStoreWithPool(t)
	ctx := context.Background()
	if _, err := s.CreateScenario(ctx, scenariostore.Scenario{Code: "audit", Name: "审计", Body: "原正文", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	asg, err := assignment.NewStore(pool).Create(ctx, assignment.NewParams{Source: assignment.SourceDirect, Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	tasks := task.NewStore(pool)
	tk, err := tasks.Create(ctx, task.NewParams{AssignmentID: asg.ID, Goal: "b", Scenario: &task.Scenario{Code: "audit", Name: "审计", Body: "原正文"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchScenario(ctx, "audit", override.Document{"body": mustJSON("新正文")}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteScenario(ctx, "audit"); err != nil {
		t.Fatal(err)
	}
	got, err := tasks.GetByID(ctx, tk.ID)
	if err != nil || got.Scenario == nil || got.Scenario.Body != "原正文" || got.Scenario.Name != "审计" {
		t.Fatalf("任务应保留建任务时的快照: %+v %v", got.Scenario, err)
	}
	sm, err := tasks.GetSummary(ctx, tk.ID)
	if err != nil || sm.ScenarioName != "审计" || !sm.ScenarioDeleted {
		t.Fatalf("摘要应带场景名并标注已删除: %+v %v", sm, err)
	}
	plain, err := tasks.Create(ctx, task.NewParams{AssignmentID: asg.ID, Goal: "b"})
	if err != nil || plain.Scenario != nil {
		t.Fatalf("无场景的任务快照为空: %+v %v", plain.Scenario, err)
	}
}

func mustJSON2(v bool) []byte {
	if v {
		return []byte("true")
	}
	return []byte("false")
}

// 出厂已下线、没改过的内置场景：仍被定时模板引用 → 转为自建保留（模板只引用存在的场景）；
// 没被引用 → 删除。
func TestScenarios_RetiredBuiltinKeptWhileReferenced(t *testing.T) {
	s, pool := newTestStoreWithPool(t)
	ctx := context.Background()
	h := builtinsync.Hash("下线", "", "正文")
	execSQL(t, s, `INSERT INTO scenario (code, name, body, is_builtin, seed_hash) VALUES
		('gone-used', '下线', '正文', true, $1), ('gone-free', '下线', '正文', true, $1)`, h)
	if _, err := cronschedule.NewStore(pool).Create(ctx, cronschedule.NewParams{CronExpr: "0 3 * * *", Title: "引用者", Scenario: "gone-used"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.scenarios.SyncBuiltins(ctx); err != nil {
		t.Fatal(err)
	}
	_ = s.cache.Invalidate(ctx, keyScenarios)
	if sc, err := s.Scenario(ctx, "gone-used"); err != nil || sc.IsBuiltin {
		t.Fatalf("被引用的下线场景应转为自建保留: %+v %v", sc, err)
	}
	if _, err := s.Scenario(ctx, "gone-free"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("没被引用、没改过的下线场景应删除: %v", err)
	}
}

// 内置场景可删除：从列表消失、出厂同步不补回、可恢复成当前出厂内容；不是已删除的内置场景不可恢复。
func TestScenarios_DeleteBuiltinSurvivesSyncAndRestores(t *testing.T) {
	s, ctx := newTestStore(t)
	ctf, _ := builtin.ScenarioByCode("ctf")
	if _, err := s.PatchScenario(ctx, "ctf", override.Document{"body": mustJSON("我改过的")}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteScenario(ctx, "ctf"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Scenario(ctx, "ctf"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后不应再列出: %v", err)
	}
	if _, err := s.UsableScenario(ctx, "ctf"); !errors.Is(err, scenariostore.ErrUnavailable) {
		t.Fatalf("删除后不能用于新任务: %v", err)
	}
	if _, err := s.PatchScenario(ctx, "ctf", override.Document{"name": mustJSON("x")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("已删除的场景不可编辑: %v", err)
	}
	syncBuiltins(t, s)
	if _, err := s.Scenario(ctx, "ctf"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("出厂同步不应补回用户删除的内置场景: %v", err)
	}
	deleted, _ := s.DeletedScenarios(ctx)
	if len(deleted) != 1 || deleted[0].Code != "ctf" || deleted[0].Body != ctf.Body {
		t.Fatalf("应列出可恢复的 ctf（内容为当前出厂）: %+v", deleted)
	}
	restored, err := s.RestoreScenario(ctx, "ctf")
	if err != nil || restored.Body != ctf.Body || !restored.Enabled || !restored.IsBuiltin {
		t.Fatalf("恢复后应为当前出厂内容并启用: %+v %v", restored, err)
	}
	if d, _ := s.ScenarioDetail(ctx, "ctf"); len(d.Overrides) != 0 {
		t.Fatalf("恢复后跟随出厂，不算自定义: %v", d.Overrides)
	}
	if _, err := s.RestoreScenario(ctx, "ctf"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未删除的场景不可恢复: %v", err)
	}
}

// 缺省场景可停用、删除；之后缺省即无场景，恢复后缺省回来。
func TestScenarios_DefaultUnavailableMeansNone(t *testing.T) {
	s, ctx := newTestStore(t)
	if def, err := s.DefaultScenario(ctx); err != nil || def == nil || def.Code != scenariostore.DefaultCode {
		t.Fatalf("缺省场景应可用: %+v %v", def, err)
	}
	if _, err := s.PatchScenario(ctx, scenariostore.DefaultCode, override.Document{"enabled": mustJSON2(false)}); err != nil {
		t.Fatalf("缺省场景可停用: %v", err)
	}
	if def, err := s.DefaultScenario(ctx); err != nil || def != nil {
		t.Fatalf("停用后缺省应为无场景: %+v %v", def, err)
	}
	if err := s.DeleteScenario(ctx, scenariostore.DefaultCode); err != nil {
		t.Fatalf("缺省场景可删除: %v", err)
	}
	if def, err := s.DefaultScenario(ctx); err != nil || def != nil {
		t.Fatalf("删除后缺省应为无场景: %+v %v", def, err)
	}
	if _, err := s.RestoreScenario(ctx, scenariostore.DefaultCode); err != nil {
		t.Fatal(err)
	}
	if def, _ := s.DefaultScenario(ctx); def == nil {
		t.Fatal("恢复后缺省场景应回来")
	}
}
