package webrepro

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/repro"
)

// normalize 是装配了 Web 领域的注册表归一化入口（与执行者落图同一条路径）。
var normalize = repro.NewRegistry(Domain{}).Normalize

func mustJSON(t *testing.T, v string) json.RawMessage {
	t.Helper()
	if !json.Valid([]byte(v)) {
		t.Fatalf("测试夹具非法 JSON: %s", v)
	}
	return json.RawMessage(v)
}

// 信封 web 配方原样通过（recipe/assert 已分离）。
func TestNormalize_WebEnvelope(t *testing.T) {
	in := mustJSON(t, `{"domain":"web","recipe":{"request":{"method":"GET","url":"http://h/api?id=1'","headers":{},"body":""}},"assert":{"body_contains":["SQL syntax error"]}}`)
	out, _, err := normalize(in)
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

// 扁平 web 形状（LLM 笔误：request/assert 写在顶层且漏写 domain）包装成信封。
func TestNormalize_FlatWebWrapped(t *testing.T) {
	in := mustJSON(t, `{"request":{"method":"POST","url":"http://h/login","headers":{"Content-Type":"application/x-www-form-urlencoded"},"body":"u=a"},"baseline":{"method":"POST","url":"http://h/login","headers":{},"body":"u=b"},"assert":{"status_code":302}}`)
	out, _, err := normalize(in)
	if err != nil {
		t.Fatalf("扁平 web 形状应包装通过: %v", err)
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

// 相对 URL 给出可行动错误（复现通道只能重发完整 URL）。
func TestNormalize_RelativeURLRejected(t *testing.T) {
	if _, _, err := normalize(mustJSON(t, `{"domain":"web","recipe":{"request":{"method":"GET","url":"/rel","headers":{},"body":""}},"assert":{"status_code":200}}`)); err == nil ||
		!strings.Contains(err.Error(), "完整 URL") {
		t.Fatalf("相对 URL 应被拒: err=%v", err)
	}
}

// 鉴别力：写入侧与复现门同一口径，只拒短子串（曾有 ["1","2","3"] 合规过审进 finding 的假阳性）；
// 常规状态码等弱断言放行并完成形状矫正，鉴别力交 LLM 评估者终判。
func TestNormalize_Discriminative(t *testing.T) {
	short := `{"domain":"web","recipe":{"request":{"method":"GET","url":"http://h/api?id=1'+UNION+SELECT+1,2,3--","headers":{},"body":""}},"assert":{"body_contains":["1","2","3"]}}`
	if _, _, err := normalize(mustJSON(t, short)); err == nil ||
		!strings.Contains(err.Error(), "无鉴别力") {
		t.Fatalf("短子串断言应被拒: err=%v", err)
	}

	// 无 baseline 的常规状态码：放行且矫正形状——若在此报错，收割侧会保留未矫正的
	// 原始配方（"200" 字符串），复现门解析失败直接判死，评估者根本看不到。
	bare := `{"domain":"web","recipe":{"request":{"method":"GET","url":"http://h/api","headers":{},"body":""}},"assert":{"status_code":"200"}}`
	out, _, err := normalize(mustJSON(t, bare))
	if err != nil {
		t.Fatalf("无 baseline 的常规状态码应放行（交评估者终判）: %v", err)
	}
	var env struct {
		Assert struct {
			StatusCode int `json:"status_code"`
		} `json:"assert"`
	}
	if err := json.Unmarshal(out, &env); err != nil || env.Assert.StatusCode != 200 {
		t.Fatalf("status_code 应矫正为数字 200: err=%v out=%s", err, out)
	}
}

// TestNormalize_HeaderContainsArray 回归：LLM 把 header_contains
// 写成数组时归一化成 map——形状放任会在复现门解析失败，该观察永远无法验证
// （e2e 实测同一形状漂移连烧 3 次验证预算后 invalid_recipe 出圈）。
func TestNormalize_HeaderContainsArray(t *testing.T) {
	// 对象数组形状
	in := `{"domain":"web","recipe":{"request":{"method":"GET","url":"http://t/x","headers":{},"body":""}},"assert":{"header_contains":[{"X-Flag":"admin"}],"status_code":"200"}}`
	out, _, err := normalize(json.RawMessage(in))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	var env struct {
		Assert struct {
			HeaderContains map[string]string `json:"header_contains"`
			StatusCode     *int              `json:"status_code"`
		} `json:"assert"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Assert.HeaderContains["x-flag"] != "admin" {
		t.Fatalf("header_contains 数组应合并成 map: %+v", env.Assert.HeaderContains)
	}
	if env.Assert.StatusCode == nil || *env.Assert.StatusCode != 200 {
		t.Fatalf("status_code 数字字符串应转数字: %+v", env.Assert.StatusCode)
	}

	// 字符串数组形状（"K: v"）
	in2 := `{"domain":"web","recipe":{"request":{"method":"GET","url":"http://t/x","headers":{},"body":""}},"assert":{"header_contains":["Set-Cookie: admin=1"]}}`
	out2, _, err := normalize(json.RawMessage(in2))
	if err != nil {
		t.Fatalf("normalize2: %v", err)
	}
	env2 := struct {
		Assert struct {
			HeaderContains map[string]string `json:"header_contains"`
		} `json:"assert"`
	}{}
	if err := json.Unmarshal(out2, &env2); err != nil {
		t.Fatalf("unmarshal2: %v", err)
	}
	if env2.Assert.HeaderContains["set-cookie"] != "admin=1" {
		t.Fatalf("字符串数组应按 k: v 拆解: %+v", env2.Assert.HeaderContains)
	}
}

// header_not_contains 的数组形状同样归一化成 map；只写头名（不带值）即要求该头不存在
// （空子串）——「缺失安全头」最常见的写法。header_contains 只写头名则要求头存在。
func TestNormalize_HeaderNotContainsArray(t *testing.T) {
	in := `{"domain":"web","recipe":{"request":{"method":"GET","url":"http://t/x","headers":{},"body":""}},` +
		`"assert":{"header_not_contains":["Content-Security-Policy","Set-Cookie: HttpOnly",{"X-Frame-Options":""}],"header_contains":["Server"]}}`
	out, _, err := normalize(json.RawMessage(in))
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	var env struct {
		Assert struct {
			HeaderContains    map[string]string `json:"header_contains"`
			HeaderNotContains map[string]string `json:"header_not_contains"`
		} `json:"assert"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]string{"content-security-policy": "", "set-cookie": "HttpOnly", "x-frame-options": ""}
	if !maps.Equal(env.Assert.HeaderNotContains, want) {
		t.Fatalf("header_not_contains = %v，期望 %v", env.Assert.HeaderNotContains, want)
	}
	if !maps.Equal(env.Assert.HeaderContains, map[string]string{"server": ""}) {
		t.Fatalf("header_contains 只写头名应要求头存在: %v", env.Assert.HeaderContains)
	}
}
