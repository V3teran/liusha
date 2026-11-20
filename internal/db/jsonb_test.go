package db

import (
	"bytes"
	"encoding/json"
	"testing"
)

// 快路径：不含 NUL 的合法 JSON 零拷贝返回（同一底层数组）。
func TestScrubJSONB_FastPathIdentity(t *testing.T) {
	raw := []byte(`{"a":"普通","b":[1,2],"c":"无标记"}`)
	if got := ScrubJSONB(raw); &got[0] != &raw[0] {
		t.Fatal("不含 NUL 时应零拷贝返回")
	}
}

// 慢路径：字符串值中的 \u0000 转义（Go 编码器对 NUL 的产出形态）被剔除，
// 其余内容原样保留——PG 拒收的是字符串值里的 NUL，不是这六个字符的字面文本。
func TestScrubJSONB_StripsEscapedNULInStrings(t *testing.T) {
	raw := []byte(`{"url":"http://t/","body":"bin\u0000ary","nested":{"k":"x\u0000y"},"arr":["\u0000"]}`)
	got := ScrubJSONB(raw)
	var v map[string]any
	if err := json.Unmarshal(got, &v); err != nil {
		t.Fatalf("清洗后应为合法 JSON: %v", err)
	}
	if v["body"] != "binary" {
		t.Errorf("body = %v", v["body"])
	}
	if nested := v["nested"].(map[string]any); nested["k"] != "xy" {
		t.Errorf("nested.k = %v", nested["k"])
	}
	if bytes.Contains(got, []byte("\u0000")) {
		t.Error("清洗后不应再含 NUL")
	}
	// 字面转义文本（双反斜杠开头）解码后是普通文本，不含 NUL，必须原样保留
	var lit any
	litRaw := []byte(`{"t":"\\u0000 字面"}`)
	if json.Unmarshal(ScrubJSONB(litRaw), &lit) != nil || lit.(map[string]any)["t"] != "\\u0000 字面" {
		t.Errorf("字面 \\u0000 文本不应被改写: %s", ScrubJSONB(litRaw))
	}
}

// 裸 0x00 字节不是合法 JSON：按原样返回，让数据库校验给出真实错误。
func TestScrubJSONB_InvalidJSONPassthrough(t *testing.T) {
	raw := []byte("{\x00}")
	if got := ScrubJSONB(raw); !bytes.Equal(got, raw) {
		t.Error("非法 JSON 应原样返回")
	}
}
