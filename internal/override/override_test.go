package override

import (
	"encoding/json"
	"slices"
	"testing"
)

func raw(s string) json.RawMessage { return json.RawMessage(s) }

func list(t *testing.T, r json.RawMessage) []string {
	t.Helper()
	var out []string
	if err := json.Unmarshal(r, &out); err != nil {
		t.Fatalf("解码列表 %s: %v", r, err)
	}
	return out
}

func change(on, off []string) json.RawMessage {
	return mustMarshal(SetChange{On: Strings(on), Off: Strings(off)})
}

var tools = []string{"a", "b", "c", "d"}

func schemaFor() Schema {
	return Schema{
		"prompt": {Kind: Replace},
		"limit":  {Kind: Replace},
		"tools":  {Kind: Set, Universe: func() []json.RawMessage { return Strings(tools) }},
		"hosts":  {Kind: Set},
	}
}

func defaultsFor(toolsDefault string) Document {
	return Document{
		"prompt": raw(`"出厂章程"`),
		"limit":  raw(`30`),
		"tools":  raw(toolsDefault),
		"hosts":  raw(`["x.com"]`),
	}
}

// ── Replace ─────────────────────────────────────────────────────────

func TestPatchReplaceRecordsValueAndBaseline(t *testing.T) {
	sc, def := schemaFor(), defaultsFor(`["a"]`)
	ov, err := Patch(sc, def, nil, Document{"prompt": raw(`"我的章程"`)})
	if err != nil {
		t.Fatal(err)
	}
	e := ov["prompt"]
	if string(e.Value) != `"我的章程"` || string(e.Baseline) != `"出厂章程"` {
		t.Fatalf("应记录值与基线: %+v", e)
	}
	if got := Apply(sc, def, ov)["prompt"]; string(got) != `"我的章程"` {
		t.Fatalf("生效值 = %s", got)
	}
}

// 保存时与出厂值相同不算自定义：数字书写差异也视为相同。
func TestPatchReplaceEqualToDefaultClearsOverride(t *testing.T) {
	sc, def := schemaFor(), defaultsFor(`["a"]`)
	ov := Overrides{"limit": {Value: raw(`50`), Baseline: raw(`30`)}}
	ov, err := Patch(sc, def, ov, Document{"limit": raw(`30.0`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ov["limit"]; ok {
		t.Fatal("与出厂相同应清除自定义")
	}
}

func TestPatchRejectsUnknownField(t *testing.T) {
	if _, err := Patch(schemaFor(), defaultsFor(`[]`), nil, Document{"name": raw(`"x"`)}); err == nil {
		t.Fatal("不可自定义字段应报错")
	}
}

func TestDescribeReplace(t *testing.T) {
	sc, def := schemaFor(), defaultsFor(`[]`)
	ov := Overrides{
		"prompt": {Value: raw(`"我的"`), Baseline: raw(`"旧出厂章程"`)}, // 出厂已更新
		"limit":  {Value: raw(`30`)},                             // 升级前修改且恰与出厂相同
	}
	st := Describe(sc, def, ov)
	if !st["prompt"].DefaultChanged || st["prompt"].MatchesDefault {
		t.Fatalf("prompt 状态错误: %+v", st["prompt"])
	}
	if st["limit"].DefaultChanged || !st["limit"].MatchesDefault || st["limit"].Baseline != nil {
		t.Fatalf("基线未知时不应判出厂已更新: %+v", st["limit"])
	}
}

// ── Set ─────────────────────────────────────────────────────────────

func TestSetOnOffAgainstExplicitDefault(t *testing.T) {
	sc, def := schemaFor(), defaultsFor(`["a","b"]`)
	ov, err := Patch(sc, def, nil, Document{"tools": change([]string{"c"}, []string{"a"})})
	if err != nil {
		t.Fatal(err)
	}
	if got := list(t, Apply(sc, def, ov)["tools"]); !slices.Equal(got, []string{"b", "c"}) {
		t.Fatalf("生效 = %v，应为 [b c]", got)
	}
	// 产品新增出厂项 d：用户的增删意图保留，新出厂项自动生效
	def2 := defaultsFor(`["a","b","d"]`)
	if got := list(t, Apply(sc, def2, ov)["tools"]); !slices.Equal(got, []string{"b", "d", "c"}) {
		t.Fatalf("出厂新增后生效 = %v，应为 [b d c]", got)
	}
}

// 再次保存只改动本次触及的项，早先的增删意图不丢。
func TestSetLaterChangePreservesEarlierIntent(t *testing.T) {
	sc, def := schemaFor(), defaultsFor(`["a","b"]`)
	ov, _ := Patch(sc, def, nil, Document{"tools": change(nil, []string{"a"})})
	ov, _ = Patch(sc, def, ov, Document{"tools": change([]string{"d"}, nil)})
	e := ov["tools"]
	if !slices.Equal(stringsOf(e.Removed), []string{"a"}) || !slices.Equal(stringsOf(e.Added), []string{"d"}) {
		t.Fatalf("记录 = %+v", e)
	}
}

// 勾回被删的出厂项、取消自己加的项：记录归零，自定义消失。
func TestSetUndoClearsOverride(t *testing.T) {
	sc, def := schemaFor(), defaultsFor(`["a"]`)
	ov, _ := Patch(sc, def, nil, Document{"tools": change([]string{"c"}, []string{"a"})})
	ov, _ = Patch(sc, def, ov, Document{"tools": change([]string{"a"}, []string{"c"})})
	if _, ok := ov["tools"]; ok {
		t.Fatalf("撤销后不应残留自定义: %+v", ov["tools"])
	}
}

// 「全部」状态下取消某项：按全集展开再减去，以后新增的项仍自动生效。
func TestWildcardOffOneKeepsAllOthers(t *testing.T) {
	sc, def := schemaFor(), defaultsFor(`["*"]`)
	ov, err := Toggle(sc, def, nil, "tools", raw(`"b"`), false)
	if err != nil {
		t.Fatal(err)
	}
	if got := list(t, Apply(sc, def, ov)["tools"]); !slices.Equal(got, []string{"a", "c", "d"}) {
		t.Fatalf("生效 = %v，应为全集去掉 b", got)
	}
	tools = append(tools, "e")
	defer func() { tools = tools[:4] }()
	if got := list(t, Apply(sc, def, ov)["tools"]); !slices.Contains(got, "e") {
		t.Fatalf("全集新增的 e 应自动生效: %v", got)
	}
	ov, _ = Toggle(sc, def, ov, "tools", raw(`"b"`), true)
	if _, ok := ov["tools"]; ok {
		t.Fatal("勾回后应回到跟随出厂")
	}
}

// 「清空再勾选」：先取消通配退出「全部」，结果只含勾选项，以后新增的不会混入。
func TestWildcardClearThenPickIsExplicit(t *testing.T) {
	sc, def := schemaFor(), defaultsFor(`["*"]`)
	ov, err := Patch(sc, def, nil, Document{"tools": change([]string{"a", "c"}, []string{"*"})})
	if err != nil {
		t.Fatal(err)
	}
	if got := list(t, Apply(sc, def, ov)["tools"]); !slices.Equal(got, []string{"a", "c"}) {
		t.Fatalf("生效 = %v，应只含 [a c]", got)
	}
	tools = append(tools, "e")
	defer func() { tools = tools[:4] }()
	if got := list(t, Apply(sc, def, ov)["tools"]); slices.Contains(got, "e") {
		t.Fatalf("退出「全部」后新增项不应混入: %v", got)
	}
}

// 出厂不是「全部」时选择「全部」：只记录勾选通配，并恢复此前删掉的项。
func TestSelectAllFromExplicitDefault(t *testing.T) {
	sc, def := schemaFor(), defaultsFor(`["a"]`)
	ov, _ := Patch(sc, def, nil, Document{"tools": change(nil, []string{"a"})})
	ov, err := Patch(sc, def, ov, Document{"tools": change([]string{"*", "a"}, nil)})
	if err != nil {
		t.Fatal(err)
	}
	if got := list(t, Apply(sc, def, ov)["tools"]); !slices.Equal(got, []string{"*"}) {
		t.Fatalf("生效 = %v，应为 [*]", got)
	}
	if !slices.Equal(stringsOf(ov["tools"].Added), []string{"*"}) || len(ov["tools"].Removed) != 0 {
		t.Fatalf("记录 = %+v", ov["tools"])
	}
}

func TestSetRejectsUnknownMemberAndContradiction(t *testing.T) {
	sc, def := schemaFor(), defaultsFor(`[]`)
	if _, err := Patch(sc, def, nil, Document{"tools": change([]string{"zzz"}, nil)}); err == nil {
		t.Fatal("全集外元素应报错")
	}
	if _, err := Patch(sc, def, nil, Document{"tools": change([]string{"a"}, []string{"a"})}); err == nil {
		t.Fatal("同一元素既勾选又取消应报错")
	}
	// 无全集约束的名单接受任意元素
	if _, err := Patch(sc, def, nil, Document{"hosts": change([]string{"any.example"}, nil)}); err != nil {
		t.Fatal(err)
	}
}

func TestSetWithIntegers(t *testing.T) {
	sc := Schema{"codes": {Kind: Set}}
	def := Document{"codes": raw(`[404, 500]`)}
	ov, err := Patch(sc, def, nil, Document{"codes": mustMarshal(SetChange{On: []json.RawMessage{raw(`302`)}, Off: []json.RawMessage{raw(`404`)}})})
	if err != nil {
		t.Fatal(err)
	}
	var got []int
	_ = json.Unmarshal(Apply(sc, def, ov)["codes"], &got)
	if !slices.Equal(got, []int{500, 302}) {
		t.Fatalf("生效 = %v", got)
	}
}

func TestDescribeSetFlagsStaleMembers(t *testing.T) {
	sc, def := schemaFor(), defaultsFor(`[]`)
	ov := Overrides{"tools": {Added: Strings([]string{"a", "gone"})}}
	st := Describe(sc, def, ov)["tools"]
	if !slices.Equal(stringsOf(st.Stale), []string{"gone"}) {
		t.Fatalf("stale = %v", stringsOf(st.Stale))
	}
}

func TestRemoveMemberAndReset(t *testing.T) {
	ov := Overrides{"tools": {Added: Strings([]string{"a"})}, "prompt": {Value: raw(`"x"`)}}
	ov2, changed := RemoveMember(ov, "tools", raw(`"a"`))
	if !changed || len(ov2) != 1 || len(ov) != 2 {
		t.Fatalf("RemoveMember 应返回新层且清掉空记录: %+v / 原层 %+v", ov2, ov)
	}
	if got := Reset(ov, "prompt"); len(got) != 1 || len(ov) != 2 {
		t.Fatalf("Reset 单字段: %+v", got)
	}
	if got := Reset(ov); len(got) != 0 {
		t.Fatal("Reset 无参应清空")
	}
}

// 出厂已移除的字段：残留自定义既不生效也不展示。
func TestRemovedFieldIgnored(t *testing.T) {
	sc, def := schemaFor(), defaultsFor(`[]`)
	ov := Overrides{"obsolete": {Value: raw(`1`)}}
	if _, ok := Apply(sc, def, ov)["obsolete"]; ok {
		t.Fatal("出厂已移除字段不应出现在生效值")
	}
	if len(Describe(sc, def, ov)) != 0 {
		t.Fatal("出厂已移除字段不应展示")
	}
}

func stringsOf(rs []json.RawMessage) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		var s string
		_ = json.Unmarshal(r, &s)
		out = append(out, s)
	}
	return out
}
