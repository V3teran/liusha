// Package heuristic 测试：通用规则 + 结构相似度。
package heuristic

import (
	"testing"

	"github.com/V3teran/liusha/internal/replay"
)

// ---------- AllDeniedByStatus ----------

func TestRule_AllDeniedByStatus(t *testing.T) {
	rs := []replay.Response{
		{StatusCode: 401},
		{StatusCode: 403},
		{StatusCode: 500},
	}
	skip, reason := AllDeniedByStatus(rs)
	if !skip {
		t.Fatalf("期望 skip=true（全部 4xx/5xx），实际 skip=%v reason=%q", skip, reason)
	}
	if reason == "" {
		t.Fatalf("期望非空 reason")
	}
}

func TestRule_AllDeniedByStatus_OneSuccess(t *testing.T) {
	rs := []replay.Response{
		{StatusCode: 401},
		{StatusCode: 200},
		{StatusCode: 500},
	}
	skip, _ := AllDeniedByStatus(rs)
	if skip {
		t.Fatalf("期望 skip=false（包含 200），实际 skip=true")
	}
}

// ---------- AllEmptyResponse ----------

func TestRule_AllEmptyResponse(t *testing.T) {
	rs := []replay.Response{
		{Body: []byte("")},
		{Body: []byte("{}")},
		{Body: []byte("[]")},
	}
	skip, reason := AllEmptyResponse(rs)
	if !skip {
		t.Fatalf("期望 skip=true（全部空 body / {} / []），实际 skip=%v reason=%q", skip, reason)
	}
}

func TestRule_AllEmptyResponse_OneNonEmpty(t *testing.T) {
	rs := []replay.Response{
		{Body: []byte("")},
		{Body: []byte(`{"data":1}`)},
		{Body: []byte("[]")},
	}
	skip, _ := AllEmptyResponse(rs)
	if skip {
		t.Fatalf("期望 skip=false（含非空 body），实际 skip=true")
	}
}

// ---------- AllAuthError ----------

func TestRule_AllAuthError_English(t *testing.T) {
	rs := []replay.Response{
		{Body: []byte(`{"msg":"Unauthorized"}`)},
		{Body: []byte(`{"msg":"Unauthorized access"}`)},
		{Body: []byte(`{"error":"Unauthorized"}`)},
	}
	skip, _ := AllAuthError(rs, nil)
	if !skip {
		t.Fatalf("期望 skip=true（全部含 Unauthorized）")
	}
}

func TestRule_AllAuthError_Chinese(t *testing.T) {
	rs := []replay.Response{
		{Body: []byte(`{"msg":"请先登录"}`)},
		{Body: []byte(`{"msg":"需要登录"}`)},
		{Body: []byte(`{"msg":"未登录"}`)},
	}
	skip, _ := AllAuthError(rs, nil)
	if !skip {
		t.Fatalf("期望 skip=true（全部含登录关键词）")
	}
}

func TestRule_AllAuthError_Mixed(t *testing.T) {
	rs := []replay.Response{
		{Body: []byte(`{"msg":"Forbidden"}`)},
		{Body: []byte(`{"msg":"权限不足"}`)},
		{Body: []byte(`{"msg":"access denied"}`)},
	}
	skip, _ := AllAuthError(rs, nil)
	if !skip {
		t.Fatalf("期望 skip=true（每个 body 各含一个鉴权关键词）")
	}
}

func TestRule_AllAuthError_OneNoMatch(t *testing.T) {
	rs := []replay.Response{
		{Body: []byte(`{"msg":"Forbidden"}`)},
		{Body: []byte(`{"data":[1,2,3]}`)}, // 无关键词
		{Body: []byte(`{"msg":"unauthorized"}`)},
	}
	skip, _ := AllAuthError(rs, nil)
	if skip {
		t.Fatalf("期望 skip=false（中间一条无关键词）")
	}
}

func TestRule_AllAuthError_CustomKeywords(t *testing.T) {
	rs := []replay.Response{
		{Body: []byte(`{"msg":"banned by policy"}`)},
		{Body: []byte(`{"msg":"BANNED"}`)},
	}
	skip, _ := AllAuthError(rs, []string{"banned"})
	if !skip {
		t.Fatalf("期望 skip=true（自定义 keywords=banned）")
	}
}

func TestDefaultAuthKeywords_Exported(t *testing.T) {
	if len(DefaultAuthKeywords) < 25 {
		t.Fatalf("期望至少 25 个默认关键词（spec §7.3），实际 %d", len(DefaultAuthKeywords))
	}
}

// ---------- StructuralSimilarity ----------

func TestStructuralSimilarity_IdenticalReturns1(t *testing.T) {
	if v := StructuralSimilarity("hello world foo bar", "hello world foo bar"); v != 1.0 {
		t.Fatalf("相同字符串应返回 1.0，实际 %v", v)
	}
}

func TestStructuralSimilarity_DifferentReturns0(t *testing.T) {
	if v := StructuralSimilarity("alpha beta gamma", "x y z"); v >= 0.3 {
		t.Fatalf("完全不同应 < 0.3，实际 %v", v)
	}
}

func TestStructuralSimilarity_EmptyVsEmpty(t *testing.T) {
	if v := StructuralSimilarity("", ""); v != 1.0 {
		t.Fatalf("两个空串应返回 1.0，实际 %v", v)
	}
}

func TestStructuralSimilarity_EmptyVsNonEmpty(t *testing.T) {
	if v := StructuralSimilarity("", "hello world"); v != 0.0 {
		t.Fatalf("一空一非空应返回 0.0，实际 %v", v)
	}
	if v := StructuralSimilarity("hello world", ""); v != 0.0 {
		t.Fatalf("一非空一空应返回 0.0，实际 %v", v)
	}
}

func TestStructuralSimilarity_SimilarReturnsHigh(t *testing.T) {
	a := "the quick brown fox jumps over the lazy dog"
	b := "the quick brown fox leaps over the lazy dog"
	v := StructuralSimilarity(a, b)
	if v <= 0.7 {
		t.Fatalf("差一个 token 应 > 0.7，实际 %v", v)
	}
}
