package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/override"
	"github.com/V3teran/liusha/internal/skillstore"
)

type fakeSkills struct {
	*fakeConfig
	skills   []configstore.SkillDetail
	deleted  string
	restored string
	trashed  []skillstore.Skill // 已删除、可恢复的内置技能
}

func (f *fakeSkills) SkillDetails(context.Context) ([]configstore.SkillDetail, error) {
	return f.skills, nil
}

func (f *fakeSkills) SkillDetail(_ context.Context, code string) (configstore.SkillDetail, error) {
	for _, d := range f.skills {
		if d.Skill.Code == code {
			return d, nil
		}
	}
	return configstore.SkillDetail{}, fmt.Errorf("技能 %q %w", code, configstore.ErrNotFound)
}

func (f *fakeSkills) CreateSkill(_ context.Context, sk skillstore.Skill) (skillstore.Skill, error) {
	return sk, nil
}

func (f *fakeSkills) PatchSkill(_ context.Context, code string, _ override.Document) (skillstore.Skill, error) {
	return skillstore.Skill{Code: code}, nil
}

func (f *fakeSkills) ResetSkill(_ context.Context, code string, _ ...string) (skillstore.Skill, error) {
	d, err := f.SkillDetail(context.Background(), code)
	if err != nil {
		return skillstore.Skill{}, err
	}
	if !d.Skill.IsBuiltin {
		return skillstore.Skill{}, configstore.ErrCustomSkill
	}
	return d.Skill, nil
}

func (f *fakeSkills) DeleteSkill(_ context.Context, code string) error {
	if _, err := f.SkillDetail(context.Background(), code); err != nil {
		return err
	}
	f.deleted = code
	return nil
}

func (f *fakeSkills) DeletedSkills(context.Context) ([]skillstore.Skill, error) {
	return f.trashed, nil
}

func (f *fakeSkills) RestoreSkill(_ context.Context, code string) (skillstore.Skill, error) {
	for _, sk := range f.trashed {
		if sk.Code == code {
			f.restored = code
			return sk, nil
		}
	}
	return skillstore.Skill{}, fmt.Errorf("已删除的内置技能 %q %w", code, configstore.ErrNotFound)
}

func skillRouter(f *fakeSkills) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerSkillRoutes(r, Deps{SkillStore: f})
	return r
}

func newFakeSkills() *fakeSkills {
	return &fakeSkills{
		fakeConfig: &fakeConfig{agents: []agent.Agent{
			{Code: "executor", Name: "执行者", Config: agent.Config{Skills: []string{"*"}}},
			{Code: "planner", Name: "规划者", Config: agent.Config{Skills: []string{}}},
		}},
		skills: []configstore.SkillDetail{
			{Skill: skillstore.Skill{Code: "bac", Name: "BAC", IsBuiltin: true, Enabled: true},
				Overrides: map[string]override.State{"body": {Kind: "replace"}}},
			{Skill: skillstore.Skill{Code: "mine", Name: "我的", Enabled: true}, Overrides: map[string]override.State{}},
		},
	}
}

func TestListSkills_MarksCustomized(t *testing.T) {
	r := skillRouter(newFakeSkills())
	w := serve(r, http.MethodGet, "/skills", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"customized":["body"]`) {
		t.Fatalf("列表项应标出已自定义字段: %d %s", w.Code, w.Body.String())
	}
}

func TestGetSkill_IncludesAgentInvolvement(t *testing.T) {
	r := skillRouter(newFakeSkills())
	w := serve(r, http.MethodGet, "/skills/bac", "")
	body := w.Body.String()
	if w.Code != http.StatusOK ||
		!strings.Contains(body, `{"code":"executor","involved":true,"name":"执行者"}`) ||
		!strings.Contains(body, `{"code":"planner","involved":false,"name":"规划者"}`) {
		t.Fatalf("详情应附各 agent 装配态（* 即全部）: %d %s", w.Code, body)
	}
}

func TestAssignSkill_TogglesAgentSkills(t *testing.T) {
	f := newFakeSkills()
	r := skillRouter(f)
	if w := serve(r, http.MethodPut, "/skills/bac/agents/planner", `{"involved":true}`); w.Code != http.StatusOK {
		t.Fatalf("得 %d (%s)", w.Code, w.Body.String())
	}
	if f.toggled.code != "planner" || f.toggled.field != agent.FieldSkills || f.toggled.elem != "bac" || !f.toggled.on {
		t.Fatalf("应勾选 planner 的 skills 名单项 bac: %+v", f.toggled)
	}
	if w := serve(r, http.MethodPut, "/skills/nope/agents/planner", `{"involved":true}`); w.Code != http.StatusNotFound {
		t.Fatalf("技能不存在应 404，得 %d", w.Code)
	}
}

func TestDeleteAndResetSkill_ByOrigin(t *testing.T) {
	f := newFakeSkills()
	r := skillRouter(f)
	if w := serve(r, http.MethodDelete, "/skills/bac", ""); w.Code != http.StatusNoContent || f.deleted != "bac" {
		t.Fatalf("内置技能也可删除: %d %q", w.Code, f.deleted)
	}
	if w := serve(r, http.MethodDelete, "/skills/mine", ""); w.Code != http.StatusNoContent || f.deleted != "mine" {
		t.Fatalf("删除自建技能: %d %q", w.Code, f.deleted)
	}
	if w := serve(r, http.MethodDelete, "/skills/mine/overrides", ""); w.Code != http.StatusConflict {
		t.Fatalf("自建技能恢复出厂应 409，得 %d", w.Code)
	}
}

// 已删除的内置技能：?deleted=true 列出，restore 恢复；不是已删除的内置技能恢复 → 404。
func TestDeletedSkills_ListAndRestore(t *testing.T) {
	f := newFakeSkills()
	f.trashed = []skillstore.Skill{{Code: "bac", Name: "BAC", IsBuiltin: true, Enabled: true}}
	r := skillRouter(f)
	if w := serve(r, http.MethodGet, "/skills?deleted=true", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code":"bac"`) {
		t.Fatalf("应列出已删除的内置技能: %d %s", w.Code, w.Body.String())
	}
	if w := serve(r, http.MethodPost, "/skills/bac/restore", ""); w.Code != http.StatusOK || f.restored != "bac" {
		t.Fatalf("恢复: %d %q", w.Code, f.restored)
	}
	if w := serve(r, http.MethodPost, "/skills/mine/restore", ""); w.Code != http.StatusNotFound {
		t.Fatalf("不是已删除的内置技能恢复应 404，得 %d", w.Code)
	}
}
