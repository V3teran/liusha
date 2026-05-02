package clip

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	cases := []struct {
		in  string
		n   int
		out string
	}{
		{"abc", 10, "abc"},
		{"abcdefghij", 10, "abcdefghij"},
		{"abcdefghijk", 10, "abcdefghij..."},
		{"", 5, ""},
	}
	for _, c := range cases {
		if got := String(c.in, c.n); got != c.out {
			t.Errorf("String(%q,%d)=%q want %q", c.in, c.n, got, c.out)
		}
	}
}

func TestStringMap(t *testing.T) {
	in := map[string]string{"k1": "short", "k2": strings.Repeat("a", 50)}
	out := StringMap(in, 10)
	if out["k1"] != "short" {
		t.Errorf("k1 should be unchanged, got %q", out["k1"])
	}
	if !strings.HasSuffix(out["k2"], "...") || len(out["k2"]) != 13 {
		t.Errorf("k2 should be truncated to 10+...., got %q (len=%d)", out["k2"], len(out["k2"]))
	}
	if StringMap(nil, 5) != nil {
		t.Error("nil input should return nil")
	}
}

func TestJSONValue(t *testing.T) {
	in := map[string]any{
		"name":    "AliceSuperLongName",
		"age":     42,
		"tags":    []any{"short", "anothersuperlongtag"},
		"nested":  map[string]any{"deep": "verylongvaluehere"},
		"boolish": true,
	}
	out := JSONValue(in, 5).(map[string]any)
	if out["name"].(string) != "Alice..." {
		t.Errorf("name truncated wrong: %v", out["name"])
	}
	if out["age"].(int) != 42 {
		t.Errorf("age should preserve int: %v", out["age"])
	}
	tags := out["tags"].([]any)
	if tags[0].(string) != "short" {
		t.Errorf("short string should not get ... suffix: %v", tags[0])
	}
	if tags[1].(string) != "anoth..." {
		t.Errorf("long tag should truncate: %v", tags[1])
	}
	deep := out["nested"].(map[string]any)["deep"].(string)
	if deep != "veryl..." {
		t.Errorf("nested deep should truncate: %v", deep)
	}
	if out["boolish"].(bool) != true {
		t.Errorf("bool should preserve: %v", out["boolish"])
	}
}

func TestRequestBody(t *testing.T) {
	if string(RequestBody(nil, 10, 100)) != "{}" {
		t.Error("empty body should return {}")
	}
	in := []byte(`{"name":"Alice","email":"alice@example.com"}`)
	out := RequestBody(in, 5, 100)
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if parsed["name"].(string) != "Alice" {
		t.Errorf("name should be exactly 5 chars: %v", parsed["name"])
	}
	if parsed["email"].(string) != "alice..." {
		t.Errorf("email should be truncated: %v", parsed["email"])
	}
	raw := []byte("name=Alice&email=alice@example.com&age=42")
	out2 := RequestBody(raw, 5, 20)
	var wrap map[string]string
	if err := json.Unmarshal(out2, &wrap); err != nil {
		t.Fatalf("non-JSON output not valid: %v", err)
	}
	if !strings.HasPrefix(wrap["_raw"], "name=Alice&email=ali") {
		t.Errorf("_raw should be original truncated to 20: %v", wrap["_raw"])
	}
}

func TestResponseBody(t *testing.T) {
	if ResponseBody(nil, 10, 5) != "" {
		t.Error("empty body should return empty")
	}
	if got := ResponseBody([]byte("hello"), 100, 20); got != "hello" {
		t.Errorf("short body should not change: %q", got)
	}
	body := []byte(strings.Repeat("a", 50) + strings.Repeat("z", 50))
	out := ResponseBody(body, 30, 10)
	if !strings.Contains(out, "...<truncated>...") {
		t.Errorf("should contain marker: %q", out)
	}
	if !strings.HasPrefix(out, strings.Repeat("a", 20)) {
		t.Errorf("should keep head (max-tail=20 a's): %q", out)
	}
	if !strings.HasSuffix(out, strings.Repeat("z", 10)) {
		t.Errorf("should keep tail (10 z's): %q", out)
	}
	out2 := ResponseBody([]byte(strings.Repeat("a", 100)), 10, 20)
	if out2 != strings.Repeat("a", 10) {
		t.Errorf("tail>=max fail-safe should be all prefix: %q", out2)
	}
}

func TestRedact(t *testing.T) {
	if got := Redact("session=abc123"); got != "<redacted len=14>" {
		t.Errorf("Redact unexpected: %q", got)
	}
	if got := Redact(""); got != "<redacted len=0>" {
		t.Errorf("empty redact: %q", got)
	}
}
