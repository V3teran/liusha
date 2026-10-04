package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// NormalizeReproEnvelope 是域信封的唯一归一化入口（write_observation 写入与
// executor 收割共用）——校验分域把关、历史形状包装、产出统一信封。

func mustJSON(t *testing.T, v string) json.RawMessage {
	t.Helper()
	if !json.Valid([]byte(v)) {
		t.Fatalf("测试夹具非法 JSON: %s", v)
	}
	return json.RawMessage(v)
}

// 信封 web 配方原样通过（recipe/assert 已分离）。
func TestNormalizeReproEnvelope_WebEnvelope(t *testing.T) {
	in := mustJSON(t, `{"domain":"web","recipe":{"request":{"method":"GET","url":"http://h/api?id=1'","headers":{},"body":""}},"assert":{"body_contains":["SQL syntax error"]}}`)
	out, _, err := NormalizeReproEnvelope(in)
	if err != nil {
		t.Fatalf("信封 web 应通过: %v", err)
	}
	var env struct {
		Domain string          `json:"domain"`
		Recipe json.RawMessage `json:"recipe"`
		Assert json.RawMessage `json:"assert"`
	}
	if err := json.Unmarshal(out, &env); err != nil || env.Domain != "web" || len(env.Recipe) == 0 || len(env.Assert) == 0 {
		t.Fatalf("归一化产物应为完整信封: %s err=%v", out, err)
	}
}

// 历史 web 形状（顶层 request/assert，无 domain）包装成信封。
func TestNormalizeReproEnvelope_LegacyWebWrapped(t *testing.T) {
	in := mustJSON(t, `{"request":{"method":"POST","url":"http://h/login","headers":{"Content-Type":"application/x-www-form-urlencoded"},"body":"u=a"},"baseline":{"method":"POST","url":"http://h/login","headers":{},"body":"u=b"},"assert":{"status_code":302}}`)
	out, _, err := NormalizeReproEnvelope(in)
	if err != nil {
		t.Fatalf("历史 web 形状应包装通过: %v", err)
	}
	var env struct {
		Domain string `json:"domain"`
		Recipe struct {
			Request  json.RawMessage `json:"request"`
			Baseline json.RawMessage `json:"baseline"`
		} `json:"recipe"`
	}
	if err := json.Unmarshal(out, &env); err != nil || env.Domain != "web" {
		t.Fatalf("应归一化为 web 信封: %s err=%v", out, err)
	}
	if len(env.Recipe.Request) == 0 || len(env.Recipe.Baseline) == 0 {
		t.Fatalf("request/baseline 应提进 recipe: %s", out)
	}
}

// generic 域：steps 必填、assert.description 必填。
func TestNormalizeReproEnvelope_Generic(t *testing.T) {
	ok, _, err := NormalizeReproEnvelope(mustJSON(t, `{"domain":"generic","recipe":{"steps":"1. 登录 2. 导出配置"},"assert":{"description":"见明文密码即坐实"}}`))
	if err != nil {
		t.Fatalf("generic 信封应通过: %v", err)
	}
	if !strings.Contains(string(ok), `"generic"`) {
		t.Fatalf("应保留 generic 域: %s", ok)
	}

	if _, _, err := NormalizeReproEnvelope(mustJSON(t, `{"domain":"generic","recipe":{},"assert":{"description":"x"}}`)); err == nil ||
		!strings.Contains(err.Error(), "steps") {
		t.Fatalf("generic 缺 steps 应被拒: err=%v", err)
	}
	if _, _, err := NormalizeReproEnvelope(mustJSON(t, `{"domain":"generic","recipe":{"steps":"do"},"assert":{}}`)); err == nil {
		t.Fatal("generic 缺 assert 应被拒")
	}
}

// 旧引用格式（traffic_id/modifications）与未知域拒绝；web 缺字段给出可行动错误。
func TestNormalizeReproEnvelope_Guards(t *testing.T) {
	if _, _, err := NormalizeReproEnvelope(mustJSON(t, `{"traffic_id":42,"modifications":{},"assert":{"status_code":200}}`)); err == nil ||
		!strings.Contains(err.Error(), "废弃") {
		t.Fatalf("旧引用格式应被拒: err=%v", err)
	}
	if _, _, err := NormalizeReproEnvelope(mustJSON(t, `{"domain":"binary","recipe":{},"assert":{}}`)); err == nil ||
		!strings.Contains(err.Error(), "未知复现域") {
		t.Fatalf("未知域应被拒: err=%v", err)
	}
	if _, _, err := NormalizeReproEnvelope(mustJSON(t, `{"domain":"web","recipe":{"request":{"method":"GET","url":"/rel","headers":{},"body":""}},"assert":{"status_code":200}}`)); err == nil ||
		!strings.Contains(err.Error(), "完整 URL") {
		t.Fatalf("相对 URL 应被拒: err=%v", err)
	}
	if _, _, err := NormalizeReproEnvelope(mustJSON(t, `{"assert":{"status_code":200}}`)); err == nil ||
		!strings.Contains(err.Error(), "domain") {
		t.Fatalf("无信封裸配方应被拒: err=%v", err)
	}
}

// 鉴别力教学：短子串与裸常规状态码（无 baseline）拒绝——曾有 ["1","2","3"] 合规过审进 finding 的假阳性。
func TestNormalizeReproEnvelope_DiscriminativeTeaching(t *testing.T) {
	short := `{"domain":"web","recipe":{"request":{"method":"GET","url":"http://h/api?id=1'+UNION+SELECT+1,2,3--","headers":{},"body":""}},"assert":{"body_contains":["1","2","3"]}}`
	if _, _, err := NormalizeReproEnvelope(mustJSON(t, short)); err == nil ||
		!strings.Contains(err.Error(), "无鉴别力") {
		t.Fatalf("短子串断言应被拒: err=%v", err)
	}

	bare := `{"domain":"web","recipe":{"request":{"method":"GET","url":"http://h/api","headers":{},"body":""}},"assert":{"status_code":200}}`
	if _, _, err := NormalizeReproEnvelope(mustJSON(t, bare)); err == nil ||
		!strings.Contains(err.Error(), "无鉴别力") {
		t.Fatalf("裸 200 断言（无 baseline）应被拒: err=%v", err)
	}

	// 有 baseline：鉴别力交运行时差分，写入侧放行
	withBase := `{"domain":"web","recipe":{"request":{"method":"GET","url":"http://h/api","headers":{},"body":""},"baseline":{"method":"GET","url":"http://h/api?id=1","headers":{},"body":""}},"assert":{"status_code":200}}`
	if _, _, err := NormalizeReproEnvelope(mustJSON(t, withBase)); err != nil {
		t.Fatalf("有 baseline 的常规状态码应放行（运行时差分裁决）: %v", err)
	}

	// 非常态状态码本身可为信号
	teapot := `{"domain":"web","recipe":{"request":{"method":"GET","url":"http://h/api","headers":{},"body":""}},"assert":{"status_code":500}}`
	if _, _, err := NormalizeReproEnvelope(mustJSON(t, teapot)); err != nil {
		t.Fatalf("非常态状态码应放行: %v", err)
	}
}

// assert 误放进 recipe 里（e2e 实测 12 连犯的笔误）：自动提升 + 教学提示。
func TestNormalizeReproEnvelope_AssertPromotedFromRecipe(t *testing.T) {
	in := mustJSON(t, `{"domain":"generic","recipe":{"steps":"1. 爬取 2. 测试","assert":{"description":"发现 XSS 输入点即坐实"}}}`)
	out, note, err := NormalizeReproEnvelope(in)
	if err != nil {
		t.Fatalf("recipe 内 assert 应自动提升: %v", err)
	}
	if note == "" || !strings.Contains(note, "顶层") {
		t.Fatalf("应返回位置教学提示: %q", note)
	}
	var env struct {
		Recipe json.RawMessage `json:"recipe"`
		Assert json.RawMessage `json:"assert"`
	}
	if err := json.Unmarshal(out, &env); err != nil || len(env.Assert) == 0 {
		t.Fatalf("提升后顶层 assert 应存在: %s", out)
	}
	if strings.Contains(string(env.Recipe), "assert") {
		t.Fatalf("recipe 内的 assert 应被剔除: %s", env.Recipe)
	}
}
