package llm

import (
	"encoding/json"
	"testing"
)

func TestParseJSON_PlainValid(t *testing.T) {
	var v struct {
		A int `json:"a"`
	}
	in := "前后闲聊\n```json\n{\"a\": 1}\n```\n尾随文本"
	if err := ParseJSON(in, &v); err != nil || v.A != 1 {
		t.Fatalf("err=%v v=%+v", err, v)
	}
}

func TestParseJSON_RawBackslashInString(t *testing.T) {
	// 模型把 Windows 路径原样写进 JSON 字符串：..\evil\.. 是非法转义（\e \w）
	var v struct {
		Cmd string `json:"cmd"`
	}
	in := `{"cmd": "type ..\evil\..\windows\win.ini"}`
	if err := ParseJSON(in, &v); err != nil {
		t.Fatalf("修复后仍解析失败: %v", err)
	}
	if v.Cmd != `type ..\\evil\\..windows\\win.ini` && v.Cmd != `type ..\evil\..\windows\win.ini` {
		t.Fatalf("意外归一结果: %q", v.Cmd)
	}
}

func TestParseJSON_TrailingCommaAndControlChar(t *testing.T) {
	var v struct {
		Items []string `json:"items"`
		Text  string   `json:"text"`
	}
	in := "{\"items\": [\"a\", \"b\",], \"text\": \"行1\n行2\"}"
	if err := ParseJSON(in, &v); err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(v.Items) != 2 || v.Text != "行1\n行2" {
		t.Fatalf("v=%+v", v)
	}
}

func TestParseJSON_LegalContentUntouched(t *testing.T) {
	// 合法转义（\n \/ \u4e2d）必须原样保留，修复层不得碰语义
	var v struct {
		P string `json:"p"`
	}
	in := `{"p": "a\nb\/c\u4e2d\\"}`
	if err := ParseJSON(in, &v); err != nil {
		t.Fatalf("err=%v", err)
	}
	if v.P != "a\nb/c中\\" {
		t.Fatalf("合法转义被改写: %q", v.P)
	}
}

func TestSanitizeJSON(t *testing.T) {
	legal := []byte(`{"a":1}`)
	if got := SanitizeJSON(legal); string(got) != string(legal) {
		t.Fatalf("合法输入被改写: %s", got)
	}
	// \P 与 \a 是非法转义（合法集只有 "\ / b f n r t u）——模型写 Windows
	// 路径的典型现场
	bad := []byte(`{"path": "C:\Program Files\app", "n": 1}`)
	if json.Valid(bad) {
		t.Fatal("测试前提失效：输入本是合法 JSON")
	}
	sanitized := SanitizeJSON(bad)
	if !json.Valid(sanitized) {
		t.Fatalf("修复后仍非法: %s", sanitized)
	}
	var v struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(sanitized, &v); err != nil || v.Path != `C:\Program Files\app` {
		t.Fatalf("v=%+v err=%v", v, err)
	}
}
