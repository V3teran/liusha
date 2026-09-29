package executor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/httpreplay"
)

// Extractor 是复现配方的参数化抽取器：从重发结果抽新鲜值（token/csrf/ID）
// 注入下一轮请求。抽不到必须报错——绝不静默放过（护栏2）。

func TestExtractor_Body(t *testing.T) {
	res := httpreplay.Result{ResponseBody: []byte(`{"data":{"token":"abc123"}}`)}

	t.Run("json 点路径取标量", func(t *testing.T) {
		e := Extractor{Name: "token", Source: "body", JSON: "data.token"}
		v, err := e.extract(res)
		require.NoError(t, err)
		assert.Equal(t, "abc123", v)
	})

	t.Run("json 数字归一化", func(t *testing.T) {
		e := Extractor{Name: "uid", Source: "body", JSON: "data.uid"}
		res2 := httpreplay.Result{ResponseBody: []byte(`{"data":{"uid":42}}`)}
		v, err := e.extract(res2)
		require.NoError(t, err)
		assert.Equal(t, "42", v, "整型不带小数点")
	})

	t.Run("body 整段", func(t *testing.T) {
		e := Extractor{Name: "raw", Source: "body"}
		res2 := httpreplay.Result{ResponseBody: []byte("plain-text")}
		v, err := e.extract(res2)
		require.NoError(t, err)
		assert.Equal(t, "plain-text", v)
	})
}

func TestExtractor_Header(t *testing.T) {
	res := httpreplay.Result{ResponseHeaders: map[string]string{"x-request-id": "req-7"}}

	t.Run("header 前缀", func(t *testing.T) {
		e := Extractor{Name: "rid", Source: "header:x-request-id"}
		v, err := e.extract(res)
		require.NoError(t, err)
		assert.Equal(t, "req-7", v)
	})

	t.Run("header 缺失报错", func(t *testing.T) {
		e := Extractor{Name: "rid", Source: "header:nope"}
		_, err := e.extract(res)
		require.ErrorContains(t, err, `响应无 header "nope"`)
	})
}

func TestExtractor_SetCookie(t *testing.T) {
	res := httpreplay.Result{ResponseHeaders: map[string]string{
		"set-cookie": "session=s%3Aabc.def; Path=/; HttpOnly, csrf=tok_1; Path=/",
	}}

	t.Run("取指定 cookie 值", func(t *testing.T) {
		e := Extractor{Name: "sess", Source: "set-cookie:session"}
		v, err := e.extract(res)
		require.NoError(t, err)
		assert.Equal(t, "s%3Aabc.def", v)
	})

	t.Run("cookie 缺失报错", func(t *testing.T) {
		e := Extractor{Name: "sess", Source: "set-cookie:nope"}
		_, err := e.extract(res)
		require.ErrorContains(t, err, "set-cookie 无 \"nope\"")
	})
}

func TestExtractor_Regex(t *testing.T) {
	res := httpreplay.Result{ResponseBody: []byte(`err: duplicate key "order-42"`)}

	t.Run("正则捕获组", func(t *testing.T) {
		e := Extractor{Name: "oid", Source: "body", Regex: `duplicate key "(.+)"`}
		v, err := e.extract(res)
		require.NoError(t, err)
		assert.Equal(t, "order-42", v)
	})

	t.Run("正则无命中报错", func(t *testing.T) {
		e := Extractor{Name: "oid", Source: "body", Regex: `nothing-(\d+)`}
		_, err := e.extract(res)
		require.ErrorContains(t, err, "无捕获组命中")
	})

	t.Run("非法正则报错", func(t *testing.T) {
		e := Extractor{Name: "bad", Source: "body", Regex: "(["}
		_, err := e.extract(res)
		require.ErrorContains(t, err, "正则编译失败")
	})
}

func TestExtractor_Guardrails(t *testing.T) {
	t.Run("Name 必填", func(t *testing.T) {
		_, err := Extractor{}.extract(httpreplay.Result{})
		require.ErrorContains(t, err, "必填")
	})
	t.Run("regex 与 json 互斥", func(t *testing.T) {
		e := Extractor{Name: "x", Source: "body", Regex: "a", JSON: "b"}
		_, err := e.extract(httpreplay.Result{ResponseBody: []byte(`{}`)})
		require.ErrorContains(t, err, "互斥")
	})
	t.Run("未知 source 报错", func(t *testing.T) {
		e := Extractor{Name: "x", Source: "query:foo"}
		_, err := e.extract(httpreplay.Result{})
		require.ErrorContains(t, err, "未知 source")
	})
	t.Run("空源值报错", func(t *testing.T) {
		e := Extractor{Name: "x", Source: "body"}
		_, err := e.extract(httpreplay.Result{})
		require.ErrorContains(t, err, "源值为空")
	})
	t.Run("json 路径命中非标量报错", func(t *testing.T) {
		e := Extractor{Name: "x", Source: "body", JSON: "data"}
		_, err := e.extract(httpreplay.Result{ResponseBody: []byte(`{"data":{"a":1}}`)})
		require.ErrorContains(t, err, "非标量值")
	})
}

func TestInjectResolved(t *testing.T) {
	strPtr := func(s string) *string { return &s }
	mods := httpreplay.Mods{
		URL:        "/api/orders/{{oid}}",
		Method:     "POST",
		Headers:    map[string]*string{"X-Token": strPtr("{{tok}}"), "Keep": strPtr("keep-me")},
		Query:      map[string]*string{"page": strPtr("1")},
		BodyFields: map[string]*string{"csrf": strPtr("{{tok}}"), "nil-field": nil},
		Body:       strPtr(`{"oid":"{{oid}}"}`),
	}
	// 语义：每次调用只注入一个占位符（多轮链式注入，每轮抽一个新鲜值）。
	out := injectResolved(mods, "tok", "42")

	assert.Equal(t, "/api/orders/{{oid}}", out.URL, "未匹配占位符保持原样")
	assert.Equal(t, "42", *out.Headers["X-Token"])
	assert.Equal(t, "keep-me", *out.Headers["Keep"], "无占位符的值不被改动")
	assert.Equal(t, "1", *out.Query["page"])
	assert.Equal(t, "42", *out.BodyFields["csrf"])
	require.Nil(t, out.BodyFields["nil-field"], "nil 值字段保持 nil")
	require.NotNil(t, out.Body)
	assert.Equal(t, `{"oid":"{{oid}}"}`, *out.Body, "未匹配占位符保持原样")
	assert.Equal(t, "POST", out.Method)

	// 二轮注入 oid
	out = injectResolved(out, "oid", "42")
	assert.Equal(t, "/api/orders/42", out.URL)
	assert.Equal(t, "42", *out.BodyFields["csrf"])
	require.NotNil(t, out.Body)
	assert.Equal(t, `{"oid":"42"}`, *out.Body)
}
