package finding

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/builtin"
	"github.com/V3teran/liusha/internal/evaluator"
)

// 词表内的取值大小写与空白从宽，词表之外的与空值归入「其他」。
func TestNormalizeClass(t *testing.T) {
	for raw, want := range map[string]string{
		"sql_injection":             "sql_injection",
		" SQL_Injection ":           "sql_injection",
		"flag":                      "flag",
		"other":                     ClassOther,
		"hidden_directory_exposure": ClassOther,
		"other_race_condition":      ClassOther,
		"":                          ClassOther,
	} {
		if got := NormalizeClass(raw); got != want {
			t.Errorf("NormalizeClass(%q) = %q，期望 %q", raw, got, want)
		}
	}
}

// 词表项各有展示名，取值唯一；不在词表里的按「其他」。CWE 写成 CWE-<编号>。
func TestClassVocabulary(t *testing.T) {
	seen := map[string]bool{}
	cwe := regexp.MustCompile(`^CWE-[1-9][0-9]*$`)
	for _, c := range Classes() {
		if seen[c.Code] {
			t.Errorf("取值重复: %s", c.Code)
		}
		seen[c.Code] = true
		if c.Name == "" {
			t.Errorf("%s 缺展示名", c.Code)
		}
		if c.CWE != "" && !cwe.MatchString(c.CWE) {
			t.Errorf("%s 的 CWE 写法不对: %q", c.Code, c.CWE)
		}
	}
	if Class("nonexistent").Code != ClassOther || Class("sql_injection").CWE != "CWE-89" {
		t.Fatal("按取值查词表不符")
	}
}

// 内置场景向评估者列出的类别与台账词表一致：渗透测试逐项写出「展示名=取值」并以 other 兜底，
// CTF 规定 flag。两边不一致时，评估者按场景给出的类别会被台账归入「其他」。
func TestBuiltinScenariosListVocabulary(t *testing.T) {
	pentest, ok := builtin.ScenarioByCode("pentest")
	if !ok {
		t.Fatal("缺内置场景 pentest")
	}
	ctf, ok := builtin.ScenarioByCode("ctf")
	if !ok {
		t.Fatal("缺内置场景 ctf")
	}
	listed := regexp.MustCompile(`([^，：\s][^，：]*?)=([a-z_]+)`).FindAllStringSubmatch(pentest.Body, -1)
	got := map[string]string{}
	for _, m := range listed {
		got[m[2]] = strings.TrimSpace(m[1])
	}
	for _, c := range Classes() {
		switch c.Code {
		case "flag":
			if !strings.Contains(ctf.Body, "类别填 flag") {
				t.Error("CTF 场景应规定 flag 的类别填 flag")
			}
		case ClassOther:
			if !strings.Contains(pentest.Body, "`other`") {
				t.Error("渗透测试场景应以 other 兜底")
			}
		default:
			if got[c.Code] != c.Name {
				t.Errorf("渗透测试场景应列出「%s=%s」，实际为 %q", c.Name, c.Code, got[c.Code])
			}
			delete(got, c.Code)
		}
	}
	for code := range got {
		t.Errorf("渗透测试场景列出了台账词表之外的类别 %s", code)
	}
}

// 迁移 0241 把存量规整进词表时列出的取值与词表一致（迁移只能写字面值）。
func TestMigrationListsVocabulary(t *testing.T) {
	sql, err := os.ReadFile("../../db/migrations/0241_finding_class_vocabulary.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, m := range regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(string(sql), -1) {
		listed = append(listed, m[1])
	}
	for _, c := range Classes() {
		if !slices.Contains(listed, c.Code) {
			t.Errorf("迁移 0241 漏了词表取值 %s", c.Code)
		}
	}
}

// 交付按词表规整类型：词表内的原样，词表之外的与未给出的归入「其他」。
func TestClaimSink_NormalizesClass(t *testing.T) {
	for raw, want := range map[string]string{"sql_injection": "sql_injection", "made_up": ClassOther, "": ClassOther} {
		store := &fakeFindingStore{}
		err := NewClaimSink(store, "run-1").Record(context.Background(), evaluator.ConfirmedClaim{
			TaskID: "t-1", ResultNodeID: "n-1", Statement: "s", HostHint: "example.com", Class: raw,
			Evidence: json.RawMessage(`{}`),
		})
		if err != nil {
			t.Fatalf("Record: %v", err)
		}
		if got := store.saved[0].Class; got != want {
			t.Errorf("交付 %q 落为 %q，期望 %q", raw, got, want)
		}
	}
}
