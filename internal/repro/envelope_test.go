package repro

import (
	"encoding/json"
	"strings"
	"testing"
)

// Registry.Normalize 是信封的唯一归一化入口（执行者落图时调用）——形状分派、
// 通用笔误矫正、产出统一信封；各形状自己的测试在各自的包。

func mustJSON(t *testing.T, v string) json.RawMessage {
	t.Helper()
	if !json.Valid([]byte(v)) {
		t.Fatalf("测试夹具非法 JSON: %s", v)
	}
	return json.RawMessage(v)
}

// steps 形状：steps 必填、assert.description 必填。
func TestNormalize_Steps(t *testing.T) {
	ok, _, err := NewRegistry().Normalize(mustJSON(t, `{"kind":"steps","recipe":{"steps":"1. 登录 2. 导出配置"},"assert":{"description":"见明文密码即坐实"}}`))
	if err != nil {
		t.Fatalf("steps 信封应通过: %v", err)
	}
	if !strings.Contains(string(ok), `"kind":"steps"`) {
		t.Fatalf("应保留 steps 形状: %s", ok)
	}

	if _, _, err := NewRegistry().Normalize(mustJSON(t, `{"kind":"steps","recipe":{},"assert":{"description":"x"}}`)); err == nil ||
		!strings.Contains(err.Error(), "steps") {
		t.Fatalf("缺 steps 应被拒: err=%v", err)
	}
	if _, _, err := NewRegistry().Normalize(mustJSON(t, `{"kind":"steps","recipe":{"steps":"do"},"assert":{}}`)); err == nil {
		t.Fatal("缺 assert 应被拒")
	}
}

// 引用流量而不自带请求的配方、未知形状、无信封裸配方拒绝。
func TestNormalize_Guards(t *testing.T) {
	// 配方必须自包含：只给流量 id 而不给请求，按缺信封拒绝并回示正确格式。
	if _, _, err := NewRegistry().Normalize(mustJSON(t, `{"traffic_id":42,"modifications":{},"assert":{"status_code":200}}`)); err == nil ||
		!strings.Contains(err.Error(), "缺少 kind") || !strings.Contains(err.Error(), "recipe") {
		t.Fatalf("引用流量的配方应按缺信封被拒: err=%v", err)
	}
	if _, _, err := NewRegistry().Normalize(mustJSON(t, `{"kind":"binary","recipe":{},"assert":{}}`)); err == nil ||
		!strings.Contains(err.Error(), "未知配方形状") {
		t.Fatalf("未知形状应被拒: err=%v", err)
	}
	if _, _, err := NewRegistry().Normalize(mustJSON(t, `{"assert":{"status_code":200}}`)); err == nil ||
		!strings.Contains(err.Error(), "kind") {
		t.Fatalf("无信封裸配方应被拒: err=%v", err)
	}
}

// assert 误放进 recipe 里（e2e 实测 12 连犯的笔误）：自动提升 + 教学提示。
func TestNormalize_AssertPromotedFromRecipe(t *testing.T) {
	in := mustJSON(t, `{"kind":"steps","recipe":{"steps":"1. 爬取 2. 测试","assert":{"description":"见到回显即坐实"}}}`)
	out, note, err := NewRegistry().Normalize(in)
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

// fakeKind 是测试用配方形状：按 marker 字段嗅探，原样收下 recipe/assert。
type fakeKind struct{}

func (fakeKind) Name() string { return "fake" }
func (fakeKind) Recognizes(f map[string]json.RawMessage) bool {
	return len(f["marker"]) > 0
}
func (fakeKind) Example() string { return `{"kind":"fake"}` }
func (fakeKind) Normalize(f map[string]json.RawMessage) (json.RawMessage, json.RawMessage, error) {
	return f["recipe"], f["assert"], nil
}

// 注册的形状按名分派，漏写 kind 时按形状特有字段嗅探；steps 始终可用。
func TestRegistry_DispatchAndSniff(t *testing.T) {
	r := NewRegistry(fakeKind{})
	out, _, err := r.Normalize(mustJSON(t, `{"marker":1,"recipe":{"x":1},"assert":{"y":2}}`))
	if err != nil || !strings.Contains(string(out), `"kind":"fake"`) {
		t.Fatalf("应嗅探为 fake 形状: %s err=%v", out, err)
	}
	if _, _, err := r.Normalize(mustJSON(t, `{"kind":"steps","recipe":{"steps":"s"},"assert":{"description":"d"}}`)); err != nil {
		t.Fatalf("steps 应始终可用: %v", err)
	}
	if _, _, err := r.Normalize(mustJSON(t, `{"kind":"http","recipe":{},"assert":{}}`)); err == nil ||
		!strings.Contains(err.Error(), "fake, steps") {
		t.Fatalf("未注册的形状应被拒并列出已支持形状: err=%v", err)
	}
}

// 写成步骤列表的配方转成 steps 信封。
func TestNormalize_StepListBecomesSteps(t *testing.T) {
	out, _, err := NewRegistry().Normalize(mustJSON(t, `["打开页面","提交表单"]`))
	if err != nil || !strings.Contains(string(out), `"kind":"steps"`) || !strings.Contains(string(out), "提交表单") {
		t.Fatalf("步骤列表应转 steps: %s err=%v", out, err)
	}
}
