package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/override"
	"github.com/V3teran/liusha/internal/scenariostore"
)

type fakeScenarios struct {
	items    []configstore.ScenarioDetail
	created  scenariostore.Scenario
	deleted  string
	restored string
	inUse    map[string]bool
	trashed  []scenariostore.Scenario // 已删除、可恢复的内置场景
}

func (f *fakeScenarios) ScenarioDetails(context.Context) ([]configstore.ScenarioDetail, error) {
	return f.items, nil
}

func (f *fakeScenarios) ScenarioDetail(_ context.Context, code string) (configstore.ScenarioDetail, error) {
	for _, d := range f.items {
		if d.Scenario.Code == code {
			return d, nil
		}
	}
	return configstore.ScenarioDetail{}, fmt.Errorf("场景 %q %w", code, configstore.ErrNotFound)
}

func (f *fakeScenarios) CreateScenario(_ context.Context, sc scenariostore.Scenario) (scenariostore.Scenario, error) {
	if err := scenariostore.ValidateNew(sc); err != nil {
		return scenariostore.Scenario{}, err
	}
	f.created = sc
	return sc, nil
}

func (f *fakeScenarios) PatchScenario(_ context.Context, code string, changes override.Document) (scenariostore.Scenario, error) {
	d, err := f.ScenarioDetail(context.Background(), code)
	if err != nil {
		return scenariostore.Scenario{}, err
	}
	if string(changes["enabled"]) == "false" {
		if f.inUse[code] {
			return scenariostore.Scenario{}, fmt.Errorf("%w：定时模板「每日」在使用它", scenariostore.ErrInUse)
		}
	}
	return d.Scenario, nil
}

func (f *fakeScenarios) ResetScenario(_ context.Context, code string, _ ...string) (scenariostore.Scenario, error) {
	d, err := f.ScenarioDetail(context.Background(), code)
	if err != nil {
		return scenariostore.Scenario{}, err
	}
	if !d.Scenario.IsBuiltin {
		return scenariostore.Scenario{}, scenariostore.ErrCustom
	}
	return d.Scenario, nil
}

func (f *fakeScenarios) DeleteScenario(_ context.Context, code string) error {
	if _, err := f.ScenarioDetail(context.Background(), code); err != nil {
		return err
	}
	if f.inUse[code] {
		return fmt.Errorf("%w：定时模板「每日」在使用它", scenariostore.ErrInUse)
	}
	f.deleted = code
	return nil
}

func (f *fakeScenarios) DeletedScenarios(context.Context) ([]scenariostore.Scenario, error) {
	return f.trashed, nil
}

func (f *fakeScenarios) RestoreScenario(_ context.Context, code string) (scenariostore.Scenario, error) {
	for _, sc := range f.trashed {
		if sc.Code == code {
			f.restored = code
			return sc, nil
		}
	}
	return scenariostore.Scenario{}, fmt.Errorf("已删除的内置场景 %q %w", code, configstore.ErrNotFound)
}

func newFakeScenarios() *fakeScenarios {
	return &fakeScenarios{
		items: []configstore.ScenarioDetail{
			{Scenario: scenariostore.Scenario{Code: "pentest", Name: "渗透测试", Body: "b", Enabled: true, IsBuiltin: true},
				Overrides: map[string]override.State{"body": {Kind: "replace"}}},
			{Scenario: scenariostore.Scenario{Code: "audit", Name: "审计", Body: "b", Enabled: true}, Overrides: map[string]override.State{}},
			{Scenario: scenariostore.Scenario{Code: "used", Name: "被引用", Body: "b", Enabled: true}, Overrides: map[string]override.State{}},
		},
		inUse: map[string]bool{"used": true},
	}
}

func scenarioRouter(f *fakeScenarios) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerScenarioRoutes(r, Deps{ScenarioStore: f})
	return r
}

func TestListScenarios_MarksCustomizedAndDefault(t *testing.T) {
	w := serve(scenarioRouter(newFakeScenarios()), http.MethodGet, "/scenarios", "")
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, `"customized":["body"]`) || !strings.Contains(body, `"is_default":true`) {
		t.Fatalf("列表项应标出已自定义字段与缺省场景: %d %s", w.Code, body)
	}
}

func TestCreateScenario_ValidatesAndCreates(t *testing.T) {
	f := newFakeScenarios()
	r := scenarioRouter(f)
	if w := serve(r, http.MethodPost, "/scenarios", `{"code":"Bad Code","name":"x","body":"y"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("非法标识应 400，得 %d", w.Code)
	}
	if w := serve(r, http.MethodPost, "/scenarios", `{"code":"ops","name":"运维","body":"要点"}`); w.Code != http.StatusCreated || f.created.Code != "ops" || !f.created.Enabled {
		t.Fatalf("新建应 201 且启用: %d %+v", w.Code, f.created)
	}
}

func TestScenarioConflicts(t *testing.T) {
	f := newFakeScenarios()
	r := scenarioRouter(f)
	cases := []struct {
		method, path, body string
		want               int
	}{
		{http.MethodDelete, "/scenarios/used", "", http.StatusConflict},                 // 被模板引用不可删
		{http.MethodPatch, "/scenarios/used", `{"enabled":false}`, http.StatusConflict}, // 被模板引用不可停用
		{http.MethodDelete, "/scenarios/audit/overrides", "", http.StatusConflict},      // 自建无出厂
		{http.MethodGet, "/scenarios/nope", "", http.StatusNotFound},                    // 不存在
		{http.MethodPatch, "/scenarios/audit", `{}`, http.StatusBadRequest},             // 空修改
		{http.MethodPatch, "/scenarios/pentest", `{"enabled":false}`, http.StatusOK},    // 缺省场景也可停用
		{http.MethodPatch, "/scenarios/pentest", `{"body":"新"}`, http.StatusOK},         // 改内置
		{http.MethodDelete, "/scenarios/pentest/overrides/body", "", http.StatusOK},     // 恢复出厂
		{http.MethodDelete, "/scenarios/pentest", "", http.StatusNoContent},             // 内置也可删除
	}
	for _, c := range cases {
		if w := serve(r, c.method, c.path, c.body); w.Code != c.want {
			t.Errorf("%s %s: 得 %d（%s），want %d", c.method, c.path, w.Code, w.Body.String(), c.want)
		}
	}
	if f.deleted != "pentest" {
		t.Errorf("应删除 pentest: %q", f.deleted)
	}
}

// 已删除的内置场景：?deleted=true 列出，restore 恢复；不是已删除的内置场景恢复 → 404。
func TestDeletedScenarios_ListAndRestore(t *testing.T) {
	f := newFakeScenarios()
	f.trashed = []scenariostore.Scenario{{Code: "ctf", Name: "CTF 夺旗", IsBuiltin: true, Enabled: true}}
	f.items = append(f.items, configstore.ScenarioDetail{Scenario: f.trashed[0], Overrides: map[string]override.State{}})
	r := scenarioRouter(f)
	if w := serve(r, http.MethodGet, "/scenarios?deleted=true", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"code":"ctf"`) {
		t.Fatalf("应列出已删除的内置场景: %d %s", w.Code, w.Body.String())
	}
	if w := serve(r, http.MethodPost, "/scenarios/ctf/restore", ""); w.Code != http.StatusOK || f.restored != "ctf" {
		t.Fatalf("恢复: %d %q", w.Code, f.restored)
	}
	if w := serve(r, http.MethodPost, "/scenarios/audit/restore", ""); w.Code != http.StatusNotFound {
		t.Fatalf("不是已删除的内置场景恢复应 404，得 %d", w.Code)
	}
}
