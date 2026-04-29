package proxify_consumer

import (
	"encoding/json"
	"testing"
)

// TestParseProxifyLine_ExtractsFields 验证：proxify 默认 JSONL schema 的最小子集解析正确。
// 字段名按 plan 给的 schema：request.method/url/headers/body, response.status_code/headers/body。
func TestParseProxifyLine_ExtractsFields(t *testing.T) {
	line := []byte(`{
	  "request":  {"method":"GET","url":"http://vulnapp:8001/api/profile","headers":{"Cookie":["session=admin_sess_a1b2c3"]},"body":""},
	  "response": {"status_code":200,"headers":{"Content-Type":["application/json"]},"body":"{\"me\":\"admin\"}"}
	}`)
	var raw proxifyRecord
	if err := json.Unmarshal(line, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	host, err := hostFromURL(raw.Request.URL)
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	if host != "vulnapp" {
		t.Fatalf("host=%q, expected %q", host, "vulnapp")
	}
	if raw.Response.StatusCode != 200 {
		t.Fatalf("status=%d, expected 200", raw.Response.StatusCode)
	}
	if raw.Request.Method != "GET" {
		t.Fatalf("method=%q", raw.Request.Method)
	}
	if got := raw.Request.Headers["Cookie"]; len(got) != 1 || got[0] != "session=admin_sess_a1b2c3" {
		t.Fatalf("cookie header=%v", got)
	}
}

// TestParseLine_BadJSON 验证：损坏的 JSON 应返回 error，不能 panic。
func TestParseLine_BadJSON(t *testing.T) {
	if _, err := parseLine([]byte("not json")); err == nil {
		t.Fatal("expected error on non-JSON input")
	}
}

// TestParseLine_OK 验证：合法 line 返回零错。
func TestParseLine_OK(t *testing.T) {
	line := []byte(`{"request":{"method":"POST","url":"http://h/x","headers":{},"body":""},"response":{"status_code":201,"headers":{},"body":""}}`)
	rec, err := parseLine(line)
	if err != nil {
		t.Fatalf("parseLine: %v", err)
	}
	if rec.Request.Method != "POST" || rec.Response.StatusCode != 201 {
		t.Fatalf("rec=%+v", rec)
	}
}

// TestHostFromURL_EmptyHost 验证：URL 缺 host 时报错。
func TestHostFromURL_EmptyHost(t *testing.T) {
	if _, err := hostFromURL("/path/only"); err == nil {
		t.Fatal("expected error for hostless URL")
	}
}

// TestHostFromURL_StripsPort 验证：host 不带 port（plan 用 host 作为 engagement scope_host）。
func TestHostFromURL_StripsPort(t *testing.T) {
	host, err := hostFromURL("http://vulnapp:8001/x")
	if err != nil {
		t.Fatal(err)
	}
	if host != "vulnapp" {
		t.Fatalf("host=%q", host)
	}
}

// TestFlattenHeaders_JoinsMultiValue 验证：多值 header 用 ", " 拼接为单字符串（jsonb 落库用）。
func TestFlattenHeaders_JoinsMultiValue(t *testing.T) {
	in := map[string][]string{"Set-Cookie": {"a=1", "b=2"}}
	out := flattenHeaders(in)
	if out["Set-Cookie"] != "a=1, b=2" {
		t.Fatalf("flatten=%q", out["Set-Cookie"])
	}
}

// TestFlattenHeaders_Empty 验证：空 map 返回空 map（不返 nil，避免下游解引用问题）。
func TestFlattenHeaders_Empty(t *testing.T) {
	out := flattenHeaders(map[string][]string{})
	if out == nil {
		t.Fatal("expected non-nil empty map")
	}
	if len(out) != 0 {
		t.Fatalf("expected empty, got %v", out)
	}
}

// TestAllowHost_Empty 验证：allow_hosts 空切片视为放行所有（v1 默认行为）。
func TestAllowHost_Empty(t *testing.T) {
	if !allowHost("evil.com", nil) {
		t.Fatal("nil allow list should permit all hosts")
	}
	if !allowHost("evil.com", []string{}) {
		t.Fatal("empty allow list should permit all hosts")
	}
}

// TestAllowHost_Match 验证：host 在白名单时放行，不在则拒绝。
func TestAllowHost_Match(t *testing.T) {
	allow := []string{"vulnapp", "internal.lab"}
	if !allowHost("vulnapp", allow) {
		t.Fatal("vulnapp should be allowed")
	}
	if allowHost("evil.com", allow) {
		t.Fatal("evil.com should be denied")
	}
}
