package scenariostore

import (
	"errors"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/builtin"
	"github.com/V3teran/liusha/internal/override"
)

// 缺省场景随程序出厂：入口缺省与代理捕获都依赖它。
func TestDefaultCodeIsSeeded(t *testing.T) {
	if _, ok := builtin.ScenarioByCode(DefaultCode); !ok {
		t.Fatalf("缺省场景 %s 不在出厂内容里", DefaultCode)
	}
}

// 出厂场景本身须通过校验（否则同步写入后在页面上无法保存）。
func TestSeedsAreValid(t *testing.T) {
	for _, b := range builtin.Scenarios() {
		if err := ValidateNew(Scenario{Code: b.Code, Name: b.Name, Description: b.Description, Body: b.Body}); err != nil {
			t.Errorf("出厂场景 %s 不合法: %v", b.Code, err)
		}
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		sc   Scenario
		ok   bool
	}{
		{"合法", Scenario{Code: "ops", Name: "运维", Body: "要点"}, true},
		{"标识非法", Scenario{Code: "Ops X", Name: "运维", Body: "要点"}, false},
		{"名称为空", Scenario{Code: "ops", Name: " ", Body: "要点"}, false},
		{"正文为空", Scenario{Code: "ops", Name: "运维", Body: "\n"}, false},
		{"名称超长", Scenario{Code: "ops", Name: strings.Repeat("名", MaxNameRunes+1), Body: "要点"}, false},
		{"正文超长", Scenario{Code: "ops", Name: "运维", Body: strings.Repeat("字", MaxBodyRunes+1)}, false},
		{"正文恰好上限", Scenario{Code: "ops", Name: "运维", Body: strings.Repeat("字", MaxBodyRunes)}, true},
	}
	for _, c := range cases {
		err := ValidateNew(c.sc)
		if c.ok != (err == nil) || (err != nil && !errors.Is(err, override.ErrInvalid)) {
			t.Errorf("%s: err=%v", c.name, err)
		}
	}
}

// 不可用（停用 / 不存在）统一是 ErrUnavailable（400），且不与冲突（409）混淆。
func TestUsable(t *testing.T) {
	if err := Usable(Scenario{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{Usable(Scenario{Name: "审计"}), NotFound("nope")} {
		if !errors.Is(err, ErrUnavailable) || IsConflict(err) {
			t.Errorf("应为不可用而非冲突: %v", err)
		}
	}
}
