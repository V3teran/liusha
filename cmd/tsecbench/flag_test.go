package main

import (
	"reflect"
	"testing"
)

// TestFlagExtractor_DefaultPattern 验证默认 flag{...} 模式能抓出 flag 并去重保序。
func TestFlagExtractor_DefaultPattern(t *testing.T) {
	ex, err := newFlagExtractor("")
	if err != nil {
		t.Fatalf("newFlagExtractor: %v", err)
	}
	got := ex.scan("拿到了 flag{abc_123} 和 flag{second}", "重复 flag{abc_123} 再来一次")
	want := []string{"flag{abc_123}", "flag{second}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scan = %v, want %v", got, want)
	}
}

// TestFlagExtractor_PreservesSourceOrder 验证来源顺序：按喂入顺序扫描多段文本时，
// 先出现的 flag 排在前面（solver 按探索图节点顺序依次提交）。
func TestFlagExtractor_PreservesSourceOrder(t *testing.T) {
	ex, _ := newFlagExtractor("")
	firstText := `{"statement":"拿到 flag{first}"}`
	secondText := `{"content":"tool output flag{second}"}`
	got := ex.scan(firstText, secondText)
	want := []string{"flag{first}", "flag{second}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scan = %v, want %v", got, want)
	}
}

// TestFlagExtractor_CustomPattern 验证按题目自定义格式覆盖默认正则。
func TestFlagExtractor_CustomPattern(t *testing.T) {
	ex, err := newFlagExtractor(`TSEC\{[^}]+\}`)
	if err != nil {
		t.Fatalf("newFlagExtractor: %v", err)
	}
	got := ex.scan("答案是 TSEC{x9y8} 这个", "flag{not_this_format}")
	want := []string{"TSEC{x9y8}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scan = %v, want %v", got, want)
	}
}

// TestFlagExtractor_InvalidPattern 验证非法正则直接报错，不静默吞。
func TestFlagExtractor_InvalidPattern(t *testing.T) {
	if _, err := newFlagExtractor(`flag\{[`); err == nil {
		t.Fatal("非法正则应报错，实际 nil")
	}
}

// TestFlagExtractor_IgnoresProse 验证默认模式不把 agent 推理文本里的 flag{ 字样当成 flag：
// 彩排中 observation 正文「flag{' 于 /home /srv /data /run 无命中}」被提交过；目标说明里的
// 格式示例 flag{...} 被 agent 复述时同理。真实 flag 不含空白与引号，占位符不是 flag。
func TestFlagExtractor_IgnoresProse(t *testing.T) {
	ex, _ := newFlagExtractor("")
	prose := []string{
		"grep 'flag{' 于 /home /srv /data /run 无命中，继续查 /opt",
		`搜索 "flag{" 关键字 }`,
		"目标要求形如 flag{...} 的字符串",
		"占位 flag{xxx} 与 flag{***}",
	}
	if got := ex.scan(prose...); len(got) != 0 {
		t.Fatalf("scan = %v, want 空", got)
	}
	// 同一段文字里的真 flag 照常命中。
	got := ex.scan("grep 'flag{' 无命中；读 /flag 得到 flag{d0c_v1ew3r-0k}")
	if want := []string{"flag{d0c_v1ew3r-0k}"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scan = %v, want %v", got, want)
	}
}

// TestFlagExtractor_NoMatch 验证无匹配时返回空（非 nil 比较也应为空集）。
func TestFlagExtractor_NoMatch(t *testing.T) {
	ex, _ := newFlagExtractor("")
	if got := ex.scan("这里没有旗子", ""); len(got) != 0 {
		t.Fatalf("scan = %v, want 空", got)
	}
}

// TestJSONStrings_DecodesEscapes 验证按解码后的字符串值扫：JSON 原文里的 &、\\ 转义不会
// 进入提交的 flag。
func TestJSONStrings_DecodesEscapes(t *testing.T) {
	ex, err := newFlagExtractor("")
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"statement":"拿到 flag{a&b}","evidence":{"lines":["flag{c\\d}"]}}`)
	got := ex.scan(jsonStrings(raw)...)
	if want := []string{`flag{c\d}`, "flag{a&b}"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := jsonStrings([]byte(`not json flag{x}`)); len(got) != 1 {
		t.Fatalf("非 JSON 应按原文返回: %q", got)
	}
}
