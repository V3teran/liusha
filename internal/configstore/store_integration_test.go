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
	"github.com/redis/go-redis/v9"

	"github.com/V3teran/liusha/internal/builtin"
	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/override"
	"github.com/V3teran/liusha/internal/skillstore"
)

func newTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	mr := miniredis.RunT(t)
	cache := cachestore.New(redis.NewClient(&redis.Options{Addr: mr.Addr()}), 0)
	return New(pool, cache, ToolCatalogFrom(nil)), context.Background()
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

func TestPatchAgent_ValidatesBeforeWriting(t *testing.T) {
	s, ctx := newTestStore(t)
	cases := []override.Document{
		{"charter": json.RawMessage(`"   "`)},
		{"complexity": json.RawMessage(`"huge"`)},
		{"max_iterations": json.RawMessage(`0`)},
		{"skills": onOff([]string{"no-such-skill"}, nil)},
		{"name": json.RawMessage(`"改名"`)},
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
	if err := s.DeleteSkill(ctx, "bac"); !errors.Is(err, ErrBuiltinSkill) {
		t.Fatalf("内置技能不可删除，得 %v", err)
	}
	if err := s.DeleteSkill(ctx, "mine"); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.AgentDetail(ctx, "planner"); len(d.Overrides) != 0 {
		t.Fatalf("删除自建技能后 agent 不应残留对它的引用: %v", d.Overrides)
	}
}

func TestBuiltinSkill_OverrideResetAndShadow(t *testing.T) {
	s, ctx := newTestStore(t)
	bac, _ := builtin.SkillByCode("bac")
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

	// 同名自建技能遮蔽内置版本（只可能来自升级前的数据或新增内置技能撞名）。
	skills := s.skills
	if _, err := skills.CreateCustom(ctx, skillstore.Skill{Code: "browser-use", Category: "tool", Name: "我的浏览器手册", Body: "b", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	_ = s.cache.Invalidate(ctx, keyCustomSkills)
	d, err := s.SkillDetail(ctx, "browser-use")
	if err != nil || d.Skill.IsBuiltin || !d.Shadows || d.Skill.Name != "我的浏览器手册" {
		t.Fatalf("同名时自建技能优先并标注遮蔽: %+v %v", d, err)
	}
}

// 已下线、但用户自定义过的内置技能转为同 code 的自建技能，内容取最后一次保存的样子。
func TestAdoptRetiredSkills(t *testing.T) {
	s, ctx := newTestStore(t)
	saved, _ := json.Marshal(skillstore.Skill{Code: "gone", Category: "tool", Name: "已下线", Description: "d", Body: "用户改过的正文", Enabled: true, IsBuiltin: true})
	if _, err := s.skills.UpdateOverrides(ctx, "gone", func(override.Overrides) (override.Overrides, skillstore.Skill, error) {
		var sk skillstore.Skill
		_ = json.Unmarshal(saved, &sk)
		return override.Overrides{"body": {Value: json.RawMessage(`"用户改过的正文"`)}}, sk, nil
	}); err != nil {
		t.Fatal(err)
	}
	adopted, err := s.AdoptRetiredSkills(ctx)
	if err != nil || !slices.Equal(adopted, []string{"gone"}) {
		t.Fatalf("应转换 gone: %v %v", adopted, err)
	}
	sk, err := s.Skill(ctx, "gone")
	if err != nil || sk.IsBuiltin || sk.Body != "用户改过的正文" {
		t.Fatalf("转换后为自建技能且保留内容: %+v %v", sk, err)
	}
	if again, _ := s.AdoptRetiredSkills(ctx); len(again) != 0 {
		t.Fatalf("应幂等: %v", again)
	}
	if ovs, _ := s.skills.Overrides(ctx); len(ovs) != 0 {
		t.Fatalf("转换后不应残留用户层: %v", ovs)
	}
}
