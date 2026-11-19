//go:build integration

package configstore

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/V3teran/liusha/internal/builtin"
	"github.com/V3teran/liusha/internal/builtinsync"
	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/override"
	"github.com/V3teran/liusha/internal/skillstore"
)

func newTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, _ := newTestStoreWithPool(t)
	return s, context.Background()
}

func newTestStoreWithPool(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	mr := miniredis.RunT(t)
	cache := cachestore.New(redis.NewClient(&redis.Options{Addr: mr.Addr()}), 0)
	s := New(pool, cache, ToolCatalogFrom(nil))
	syncBuiltins(t, s)
	return s, pool
}

// execSQL 直接改表（模拟升级前的数据形态）。
func execSQL(t *testing.T, s *Store, sql string, args ...any) {
	t.Helper()
	ctx := context.Background()
	tx, err := s.agents.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// syncBuiltins 执行出厂同步（生产形态由 liusha migrate 在迁移后执行）。
func syncBuiltins(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.skills.SyncBuiltins(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.scenarios.SyncBuiltins(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.cache.Invalidate(ctx, keySkills, keyScenarios); err != nil {
		t.Fatal(err)
	}
}

func onOff(on, off []string) json.RawMessage {
	b, _ := json.Marshal(override.SetChange{On: override.Strings(on), Off: override.Strings(off)})
	return b
}

// 同一 agent 的并发装卸（含首次写入时尚无行的瞬间）不得丢失任何一次修改。
func TestToggleAgent_ConcurrentWritesAllLand(t *testing.T) {
	s, ctx := newTestStore(t)
	planner, _ := builtin.AgentByCode("planner")
	var targets []string
	for _, n := range ToolCatalogFrom(nil).FunctionTools {
		if !slices.Contains(planner.FunctionTools, n) {
			targets = append(targets, n)
		}
	}
	if len(targets) < 5 {
		t.Fatalf("可装配工具太少: %v", targets)
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(targets))
	for _, name := range targets {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			if _, err := s.ToggleAgent(ctx, "planner", "function_tools", name, true); err != nil {
				errs <- err
			}
		}(name)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("并发装配失败: %v", err)
	}
	p, err := s.Agent(ctx, "planner")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range targets {
		if !slices.Contains(p.FunctionTools, name) {
			t.Errorf("并发装配丢失 %s: %v", name, p.FunctionTools)
		}
	}
}

// 用户层清空即删行：并发的「恢复出厂」与修改交错时，删行窗口不得让另一方查不到行而
// 报资源不存在（固定角色 planner 永远存在）。
func TestResetAgent_ConcurrentWithRowDeletionNeverReportsMissing(t *testing.T) {
	s, ctx := newTestStore(t)
	for round := 0; round < 20; round++ {
		if _, err := s.PatchAgent(ctx, "planner", override.Document{"description": json.RawMessage(`"我的规划者"`)}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := s.ResetAgent(ctx, "planner"); err != nil {
					errs <- err
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("第 %d 轮并发恢复出厂失败: %v", round, err)
		}
	}
}

func TestPatchAgent_ValidatesBeforeWriting(t *testing.T) {
	s, ctx := newTestStore(t)
	cases := []override.Document{
		{"charter": json.RawMessage(`"   "`)},
		{"complexity": json.RawMessage(`"huge"`)},
		{"max_iterations": json.RawMessage(`0`)},
		{"skills": onOff([]string{"no-such-skill"}, nil)},
		{"name": json.RawMessage(`"改名"`)},
		{"max_iterations": json.RawMessage(`"wrong"`)}, // 合法 JSON、类型错误
		{"charter": json.RawMessage(`42`)},
	}
	for _, c := range cases {
		if _, err := s.PatchAgent(ctx, "planner", c); !errors.Is(err, override.ErrInvalid) {
			t.Errorf("%v 应被拒（ErrInvalid），得 %v", c, err)
		}
	}
	if d, _ := s.AgentDetail(ctx, "planner"); len(d.Overrides) != 0 {
		t.Fatalf("被拒的提交不应留下自定义: %v", d.Overrides)
	}
	if _, err := s.Agent(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未知 agent 应 ErrNotFound，得 %v", err)
	}
}

// 写入与出厂相同即不算自定义；恢复默认清除自定义；详情给出基线与出厂值。
func TestPatchAndResetAgent_RoundTrip(t *testing.T) {
	s, ctx := newTestStore(t)
	planner, _ := builtin.AgentByCode("planner")
	if _, err := s.PatchAgent(ctx, "planner", override.Document{"charter": json.RawMessage(`"我的章程"`)}); err != nil {
		t.Fatal(err)
	}
	d, _ := s.AgentDetail(ctx, "planner")
	st, ok := d.Overrides["charter"]
	if !ok || d.Agent.Charter != "我的章程" {
		t.Fatalf("应记录章程自定义: %+v", d.Overrides)
	}
	var baseline string
	_ = json.Unmarshal(st.Baseline, &baseline)
	if baseline != planner.Charter || st.DefaultChanged {
		t.Fatalf("基线应为当前出厂章程: changed=%v", st.DefaultChanged)
	}
	b, _ := json.Marshal(planner.Description)
	if _, err := s.PatchAgent(ctx, "planner", override.Document{"description": b}); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.AgentDetail(ctx, "planner"); len(d.Overrides) != 1 {
		t.Fatalf("与出厂相同的调度摘要不应算自定义: %v", d.Overrides)
	}
	if _, err := s.ResetAgent(ctx, "planner"); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Agent(ctx, "planner"); p.Charter != planner.Charter {
		t.Fatal("恢复默认后章程应回到出厂值")
	}
}

func TestSkills_CustomLifecycleAndReferenceCleanup(t *testing.T) {
	s, ctx := newTestStore(t)
	if _, err := s.CreateSkill(ctx, skillstore.Skill{Code: "bac", Category: "vuln", Name: "x", Body: "y"}); !errors.Is(err, skillstore.ErrConflict) {
		t.Fatalf("与内置技能同名应冲突，得 %v", err)
	}
	if _, err := s.CreateSkill(ctx, skillstore.Skill{Code: "Bad Code", Category: "vuln", Name: "x", Body: "y"}); !errors.Is(err, override.ErrInvalid) {
		t.Fatalf("非法标识应被拒，得 %v", err)
	}
	if _, err := s.CreateSkill(ctx, skillstore.Skill{Code: "mine", Category: "vuln", Name: "我的", Body: "正文", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchAgent(ctx, "planner", override.Document{"skills": onOff([]string{"mine"}, nil)}); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Agent(ctx, "planner"); !slices.Contains(p.Skills, "mine") {
		t.Fatalf("planner 应装配 mine: %v", p.Skills)
	}
	if err := s.DeleteSkill(ctx, "mine"); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.AgentDetail(ctx, "planner"); len(d.Overrides) != 0 {
		t.Fatalf("删除自建技能后 agent 不应残留对它的引用: %v", d.Overrides)
	}
}

// 删除自建技能与清理各 agent 对它的引用是一个原子操作：提交后的缓存失效失败不得留下
// 「技能已删、引用残留」——那样重删时技能已不存在、清理再无入口，日后同名重建的技能会
// 被残留绑定自动装配回去。
func TestDeleteSkill_ReferencesCleanedEvenIfCacheInvalidationFails(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	mr := miniredis.RunT(t)
	s := New(pool, cachestore.New(redis.NewClient(&redis.Options{Addr: mr.Addr()}), 0), ToolCatalogFrom(nil))
	ctx := context.Background()
	if _, err := s.CreateSkill(ctx, skillstore.Skill{Code: "mine", Category: "vuln", Name: "我的", Body: "正文", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchAgent(ctx, "planner", override.Document{"skills": onOff([]string{"mine"}, nil)}); err != nil {
		t.Fatal(err)
	}

	mr.Close() // 缓存失效广播失败
	if err := s.DeleteSkill(ctx, "mine"); err == nil {
		t.Fatal("缓存失效失败应上报")
	}

	ovs, err := s.agents.Overrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ovs["planner"]) != 0 {
		t.Fatalf("技能已删，planner 不应残留对它的引用: %v", ovs["planner"])
	}
	recs, err := s.skills.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(recs, func(r skillstore.Record) bool { return r.Code == "mine" }) {
		t.Fatal("技能应已删除")
	}
}

func TestBuiltinSkill_OverrideResetAndShadow(t *testing.T) {
	s, ctx := newTestStore(t)
	bac, _ := builtin.SkillByCode("bac")
	if _, err := s.PatchSkill(ctx, "bac", override.Document{"enabled": json.RawMessage(`"yes"`)}); !errors.Is(err, override.ErrInvalid) {
		t.Fatalf("字段类型错误应按配置不合法拒绝（ErrInvalid），得 %v", err)
	}
	if _, err := s.PatchSkill(ctx, "bac", override.Document{"enabled": json.RawMessage(`false`)}); err != nil {
		t.Fatal(err)
	}
	enabled, _ := s.EnabledSkills(ctx)
	if slices.ContainsFunc(enabled, func(sk skillstore.Skill) bool { return sk.Code == "bac" }) {
		t.Fatal("停用的内置技能不应出现在启用列表")
	}
	if _, err := s.ResetSkill(ctx, "bac"); err != nil {
		t.Fatal(err)
	}
	if sk, _ := s.Skill(ctx, "bac"); !sk.Enabled || sk.Body != bac.Body {
		t.Fatalf("恢复出厂后应启用且为出厂正文: %+v", sk)
	}

	if d, _ := s.SkillDetail(ctx, "bac"); len(d.Overrides) != 0 {
		t.Fatalf("恢复出厂后不应再有偏离出厂的字段: %v", d.Overrides)
	}
}

// 出厂同步：没改过的跟随新版（保留启停）、改过的不动、改回原样算没改、自建同 code 遮蔽内置、
// 下线的内置技能改过转自建、没改过删除。
func TestSyncBuiltinSkills(t *testing.T) {
	s, ctx := newTestStore(t)
	pool := s.skills
	exec := func(sql string, args ...any) { t.Helper(); execSQL(t, s, sql, args...) }
	bac, _ := builtin.SkillByCode("bac")
	browser, _ := builtin.SkillByCode("browser-use")
	oldHash := builtinsync.Hash(bac.Name, bac.Description, "旧版正文")

	// bac：模拟「上一版出厂 + 用户只停用」——内容是旧出厂、基线是旧出厂哈希。
	exec(`UPDATE skill SET body='旧版正文', enabled=false, seed_hash=$1 WHERE code='bac'`, oldHash)
	// browser-use：用户改过正文（基线是当前出厂）。
	exec(`UPDATE skill SET body='我改过的' WHERE code='browser-use'`)
	// 下线的内置技能：gone-clean 没改过，gone-edited 改过。
	goneHash := builtinsync.Hash("下线", "", "正文")
	exec(`INSERT INTO skill (code, category, name, body, is_builtin, seed_hash) VALUES
		('gone-clean', 'tool', '下线', '正文', true, $1),
		('gone-edited', 'tool', '下线', '改过的正文', true, $1)`, goneHash)

	done, err := pool.SyncBuiltins(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 3 {
		t.Fatalf("应有 3 个动作（bac 更新、gone-clean 删除、gone-edited 转自建），实际 %v", done)
	}
	_ = s.cache.Invalidate(ctx, keySkills)

	if sk, _ := s.Skill(ctx, "bac"); sk.Body != bac.Body || sk.Enabled {
		t.Fatalf("没改过的应跟随新版且保留停用: %+v", sk)
	}
	if sk, _ := s.Skill(ctx, "browser-use"); sk.Body != "我改过的" || !sk.IsBuiltin {
		t.Fatalf("改过的应保持用户内容: %+v", sk)
	}
	if d, _ := s.SkillDetail(ctx, "browser-use"); d.Overrides["body"].Kind == "" || d.Overrides["body"].DefaultChanged {
		t.Fatalf("改过的正文应标为已自定义、出厂未更新: %+v", d.Overrides)
	}
	if _, err := s.Skill(ctx, "gone-clean"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("没改过的下线技能应删除: %v", err)
	}
	if sk, err := s.Skill(ctx, "gone-edited"); err != nil || sk.IsBuiltin || sk.Body != "改过的正文" {
		t.Fatalf("改过的下线技能应转自建并保留内容: %+v %v", sk, err)
	}

	// 改回原样（与当前出厂相同）即不算自定义，此后照常跟随新版。
	if _, err := s.PatchSkill(ctx, "browser-use", override.Document{"body": mustJSON(browser.Body)}); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.SkillDetail(ctx, "browser-use"); len(d.Overrides) != 0 {
		t.Fatalf("改回出厂后不应有偏离出厂的字段: %v", d.Overrides)
	}
	exec(`UPDATE skill SET body='旧版', seed_hash=$1 WHERE code='browser-use'`, builtinsync.Hash(browser.Name, browser.Description, "旧版"))
	if _, err := pool.SyncBuiltins(ctx); err != nil {
		t.Fatal(err)
	}
	_ = s.cache.Invalidate(ctx, keySkills)
	if sk, _ := s.Skill(ctx, "browser-use"); sk.Body != browser.Body {
		t.Fatalf("没改过的应跟随新版: %+v", sk)
	}

	// 自建同 code：出厂同步不动它，内置版本被遮蔽。
	exec(`DELETE FROM skill WHERE code='bac'`)
	exec(`INSERT INTO skill (code, category, name, body) VALUES ('bac', 'vuln', '我的 BAC', '自建正文')`)
	if done, _ := pool.SyncBuiltins(ctx); len(done) != 0 {
		t.Fatalf("自建同 code 时同步不应有动作: %v", done)
	}
	_ = s.cache.Invalidate(ctx, keySkills)
	d, err := s.SkillDetail(ctx, "bac")
	if err != nil || d.Skill.IsBuiltin || !d.Shadows || d.Skill.Name != "我的 BAC" {
		t.Fatalf("自建同 code 应优先并标注遮蔽: %+v %v", d, err)
	}
}

// 内置技能可删除：从列表与启用列表消失、清掉各 agent 的引用、出厂同步不补回、可恢复。
func TestSkills_DeleteBuiltinSurvivesSyncAndRestores(t *testing.T) {
	s, ctx := newTestStore(t)
	bac, _ := builtin.SkillByCode("bac")
	if _, err := s.PatchAgent(ctx, "planner", override.Document{"skills": onOff([]string{"bac"}, nil)}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSkill(ctx, "bac"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Skill(ctx, "bac"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除后不应再列出: %v", err)
	}
	if enabled, _ := s.EnabledSkills(ctx); slices.ContainsFunc(enabled, func(sk skillstore.Skill) bool { return sk.Code == "bac" }) {
		t.Fatal("删除的技能不应进技能索引")
	}
	if d, _ := s.AgentDetail(ctx, "planner"); len(d.Overrides) != 0 {
		t.Fatalf("删除技能后 agent 不应残留对它的引用: %v", d.Overrides)
	}
	syncBuiltins(t, s)
	if _, err := s.Skill(ctx, "bac"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("出厂同步不应补回用户删除的内置技能: %v", err)
	}
	if deleted, _ := s.DeletedSkills(ctx); len(deleted) != 1 || deleted[0].Code != "bac" {
		t.Fatalf("应列出可恢复的 bac: %+v", deleted)
	}
	if sk, err := s.RestoreSkill(ctx, "bac"); err != nil || sk.Body != bac.Body || !sk.Enabled || sk.Category != bac.Category {
		t.Fatalf("恢复后应为当前出厂内容并启用: %+v %v", sk, err)
	}
}
