package evaluator

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	evaluatorcontract "github.com/V3teran/liusha/internal/evaluator/contract"
)

// 指纹规范形：大小写、尾斜杠、空白从宽归一——同义报告同键；类别或作用点不同不得同键；
// 缺类别、缺对象或形状损坏不参与去重。
func TestCanonicalFingerprint(t *testing.T) {
	key := func(raw string) string {
		t.Helper()
		k, _ := canonicalFingerprint(json.RawMessage(raw))
		return k
	}
	a := key(`{"class":"Weak_Config","target":"GET http://T.Local:8080/admin/","point":" Name "}`)
	b := key(`{"class":"weak_config","target":"get http://t.local:8080/admin","point":"name"}`)
	if a == "" || a != b {
		t.Fatalf("同义指纹应同键: %q vs %q", a, b)
	}
	if c := key(`{"class":"other","target":"GET http://t.local:8080/admin","point":"name"}`); c == a {
		t.Fatal("不同类别不得同键")
	}
	if d := key(`{"class":"weak_config","target":"GET http://t.local:8080/admin","point":"id"}`); d == a {
		t.Fatal("不同作用点不得同键")
	}
	if e := key(`{"class":"weak_config","target":"GET http://t.local:8080/admin"}`); e == "" {
		t.Fatal("作用点可空")
	}

	for _, raw := range []string{``, `["x"]`, `{"target":"t"}`, `{"class":"c"}`, `{"class":" ","target":"t"}`} {
		if _, ok := canonicalFingerprint(json.RawMessage(raw)); ok {
			t.Errorf("%q 不应参与去重", raw)
		}
	}
}

// 判定契约逐一讲到指纹的每个字段：契约是评估者填写指纹的唯一依据，与归一形状不得漂移。
func TestContractDescribesFingerprint(t *testing.T) {
	typ := reflect.TypeOf(ClaimFingerprint{})
	for i := range typ.NumField() {
		name := typ.Field(i).Tag.Get("json")
		if !strings.Contains(evaluatorcontract.Text, "`"+name+"`") {
			t.Errorf("判定契约缺少指纹字段 %s", name)
		}
	}
}
