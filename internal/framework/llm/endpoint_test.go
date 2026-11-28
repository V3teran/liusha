package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// OperationURL 必须与 SDK 真实请求的地址一致（地址预览与诊断展示的正确性依赖于此）。
func TestOperationURL_MatchesSDKRequests(t *testing.T) {
	for _, tc := range []struct {
		protocol string
		path     string // 服务端挂载的前缀路径
		body     string
	}{
		{ProviderTypeOpenAIChatCompletions, "/api/paas/v4", openAIOK},
		{ProviderTypeAnthropicMessages, "/api/anthropic", anthropicSSE},
		{ProviderTypeAnthropicMessages, "", anthropicSSE},
	} {
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(tc.body))
		}))
		base := srv.URL + tc.path
		gen, err := BuildGeneratorWithKey(context.Background(), ProviderSpec{
			Code: "p", Type: tc.protocol, BaseURL: base, Model: "m", APIKey: "k", MaxTokens: 8,
		}, NewClientPool())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := gen.Generate(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil); err != nil {
			t.Fatalf("%s: %v", tc.protocol, err)
		}
		if want := OperationURL(tc.protocol, base+"/", OpChat); srv.URL+gotPath != want {
			t.Fatalf("%s: SDK 请求 %s，OperationURL 给出 %s", tc.protocol, srv.URL+gotPath, want)
		}
		srv.Close()
	}
}

// 2xx 但不是消息流（网关把错误页包成 200）：判为不符合协议；字段齐全或仅缺 id 的消息流照常接受。
func TestAnthropic_UnexpectedResponseOnlyWhenNotAMessageStream(t *testing.T) {
	cases := map[string]bool{ // 响应体 → 是否应判为不符合协议
		`{"code":500,"msg":"404 NOT_FOUND","success":false}`:          true, // 200 包着的错误页，不是事件流
		`<html><body>502 Bad Gateway</body></html>`:                   true,
		strings.Replace(anthropicSSE, `"id":"msg_1",`, `"id":"",`, 1): false, // 缺 id 的宽松网关
		anthropicSSE: false,
	}
	for body, unexpected := range cases {
		srv, _ := captureServer(t, body)
		gen, err := BuildGeneratorWithKey(context.Background(), ProviderSpec{
			Code: "p", Type: ProviderTypeAnthropicMessages, BaseURL: srv.URL, Model: "m", APIKey: "k", MaxTokens: 8,
		}, NewClientPool())
		if err != nil {
			t.Fatal(err)
		}
		_, err = gen.Generate(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil)
		if got := errors.Is(err, ErrUnexpectedResponse); got != unexpected {
			t.Fatalf("%s: 不符合协议判定 want %v, got err=%v", body[:min(60, len(body))], unexpected, err)
		}
	}
}

// Responses 适配器直接发 HTTP：对话请求落在 Base URL 后的 /responses，与 OperationURL 一致。
func TestResponses_OperationURLMatchesRequests(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		_, _ = w.Write([]byte(responsesOK))
	}))
	defer srv.Close()
	gen, err := BuildGeneratorWithKey(context.Background(), ProviderSpec{
		Code: "p", Type: ProviderTypeOpenAIResponses, BaseURL: srv.URL + "/v1", Model: "m", APIKey: "k",
		MaxTokens: 8, OutputLimitParam: OutputLimitMaxOutputTokens,
	}, NewClientPool())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gen.Generate(context.Background(), []Message{{Role: RoleUser, Content: "ping"}}, nil); err != nil {
		t.Fatal(err)
	}
	want := OperationURL(ProviderTypeOpenAIResponses, srv.URL+"/v1", OpChat)
	if len(paths) != 1 || want != "http://"+strings.TrimPrefix(srv.URL, "http://")+"/v1/responses" || paths[0] != "POST "+"/v1/responses" {
		t.Fatalf("适配器请求 %v，OperationURL 给出 %s", paths, want)
	}
	if got := OperationURL(ProviderTypeOpenAIResponses, srv.URL+"/v1", OpModels); got != srv.URL+"/v1/models" {
		t.Fatalf("Responses 的模型列表地址应为 %s，实际 %s", srv.URL+"/v1/models", got)
	}
}
