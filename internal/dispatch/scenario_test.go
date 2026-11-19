package dispatch

import (
	"context"
	"errors"
	"testing"

	"github.com/V3teran/liusha/internal/scenariostore"
)

type fakeScenarioResolver map[string]scenariostore.Scenario

func (f fakeScenarioResolver) UsableScenario(_ context.Context, code string) (scenariostore.Scenario, error) {
	sc, ok := f[code]
	if !ok {
		return scenariostore.Scenario{}, scenariostore.NotFound(code)
	}
	return sc, scenariostore.Usable(sc)
}

func (f fakeScenarioResolver) DefaultScenario(ctx context.Context) (*scenariostore.Scenario, error) {
	sc, err := f.UsableScenario(ctx, scenariostore.DefaultCode)
	if errors.Is(err, scenariostore.ErrUnavailable) {
		return nil, nil
	}
	return &sc, err
}

func ptr(s string) *string { return &s }

// 入口对场景的选择落定为任务快照：不指定 → 缺省场景；"" → 无场景；点名 → 必须可用，否则拒绝
// （不悄悄换成别的场景）。
func TestResolveScenario(t *testing.T) {
	ctx := context.Background()
	s := &Service{Scenarios: fakeScenarioResolver{
		"pentest": {Code: "pentest", Name: "渗透测试", Body: "正文", Enabled: true},
		"off":     {Code: "off", Name: "停用", Body: "b"},
	}}
	if sc, err := s.resolveScenario(ctx, nil); err != nil || sc == nil || sc.Code != "pentest" || sc.Body != "正文" {
		t.Fatalf("不指定应取缺省场景: %+v %v", sc, err)
	}
	if sc, err := s.resolveScenario(ctx, ptr("")); sc != nil || err != nil {
		t.Fatalf("空选择应为无场景: %+v %v", sc, err)
	}
	if sc, err := s.resolveScenario(ctx, ptr("pentest")); err != nil || sc == nil || sc.Name != "渗透测试" {
		t.Fatalf("应快照点名的场景: %+v %v", sc, err)
	}
	for _, code := range []string{"off", "nope"} {
		if _, err := s.resolveScenario(ctx, ptr(code)); !errors.Is(err, scenariostore.ErrUnavailable) {
			t.Fatalf("%s 应被拒（400）: %v", code, err)
		}
	}
}

// 缺省场景被停用或删除时，不指定场景即无场景（用户的操作决定，不另找兜底）；但点名它仍被拒。
func TestResolveScenario_DefaultUnavailableMeansNone(t *testing.T) {
	ctx := context.Background()
	for name, scenarios := range map[string]fakeScenarioResolver{
		"停用": {"pentest": {Code: "pentest", Name: "渗透测试", Body: "b"}},
		"删除": {},
	} {
		s := &Service{Scenarios: scenarios}
		if sc, err := s.resolveScenario(ctx, nil); sc != nil || err != nil {
			t.Errorf("%s：缺省应为无场景: %+v %v", name, sc, err)
		}
		if _, err := s.resolveScenario(ctx, ptr("pentest")); !errors.Is(err, scenariostore.ErrUnavailable) {
			t.Errorf("%s：点名缺省场景应被拒: %v", name, err)
		}
	}
}
